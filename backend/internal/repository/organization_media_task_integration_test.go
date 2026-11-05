package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type mediaTaskTestQuota struct {
	service.OrganizationMediaQuotaRepository
}

func (q mediaTaskTestQuota) Reserve(ctx context.Context, o mediatasks.Owner, id, digest, kind, model string, n int64) error {
	v, err := q.OrganizationMediaQuotaRepository.Reserve(ctx, service.MediaQuotaClaim{UserID: o.UserID, APIKeyID: o.APIKeyID, RequestID: id, Digest: digest, Kind: kind, Model: model, Units: n})
	if err != nil {
		return err
	}
	if v == nil {
		return mediatasks.ErrQuotaRequired
	}
	return nil
}
func (q mediaTaskTestQuota) Validate(ctx context.Context, user int64, id string) error {
	return q.ValidateDispatch(ctx, user, id)
}

type mediaTaskTestRoute struct{ calls atomic.Int32 }

func (r *mediaTaskTestRoute) Resolve(context.Context, mediatasks.Owner, string, string) (mediatasks.Provider, error) {
	return mediatasks.Provider{AccountID: 9, Kind: mediatasks.Audio, Key: "fixture"}, nil
}
func (r *mediaTaskTestRoute) Reopen(context.Context, mediatasks.Owner, *mediatasks.Task) (mediatasks.Provider, error) {
	r.calls.Add(1)
	return mediatasks.Provider{AccountID: 9, Kind: mediatasks.Model3D, Key: "fixture"}, nil
}

type mediaTaskTestSupplier struct{ submits, polls atomic.Int32 }
type mediaTaskFailCreateStore struct {
	mediatasks.Store
	failed atomic.Bool
}

func (s *mediaTaskFailCreateStore) Create(ctx context.Context, t *mediatasks.Task) (*mediatasks.Task, bool, error) {
	if s.failed.CompareAndSwap(false, true) {
		return nil, false, mediatasks.ErrUnavailable
	}
	return s.Store.Create(ctx, t)
}

func (s *mediaTaskTestSupplier) Submit(_ context.Context, _ mediatasks.Provider, _ mediatasks.Owner, _ string, r mediatasks.Request) mediatasks.Outcome {
	s.submits.Add(1)
	if r.Model == mediatasks.TripoModel {
		return mediatasks.Outcome{Status: "running", ProviderID: "fixture-provider-id"}
	}
	b := make([]byte, 417)
	copy(b, []byte{255, 251, 144, 0})
	return mediatasks.Outcome{Status: "succeeded", Content: b, ContentType: "audio/mpeg", Progress: 100}
}
func (s *mediaTaskTestSupplier) Poll(context.Context, mediatasks.Provider, string) mediatasks.Outcome {
	s.polls.Add(1)
	return mediatasks.Outcome{Status: "indeterminate", ProviderID: "fixture-provider-id", ErrorCode: "fixture_transport_interrupted"}
}

func TestOrganizationMediaTasksConcurrentReplayRestartAndIsolation(t *testing.T) {
	db, q := mediaQuotaTestDB(t)
	migration, err := os.ReadFile("../../migrations/247_organization_audio_model3d.sql")
	require.NoError(t, err)
	actorMigration, err := os.ReadFile("../../migrations/249_organization_voice_assets.sql")
	require.NoError(t, err)
	migration = append(migration, actorMigration...)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	ctx := context.Background()
	g := mediaGrant("audio-fixture-grant", 0, 0)
	g.AudioCharacters = 20
	g.Model3DTasks = 1
	_, err = q.Grant(ctx, 1, g)
	require.NoError(t, err)
	store := NewOrganizationMediaTaskRepository(db)
	routes := &mediaTaskTestRoute{}
	supplier := &mediaTaskTestSupplier{}
	engine := mediatasks.NewEngine(store, mediaTaskTestQuota{q}, routes, supplier)
	owner := mediatasks.Owner{UserID: 1, APIKeyID: 7, GroupID: 5}
	requestID := "123e4567-e89b-42d3-a456-426614174000"
	input := mediatasks.Request{Model: mediatasks.SpeechModel, Input: "你好", Speed: 1}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 50; attempt++ {
				task, e := engine.Create(ctx, owner, mediatasks.Audio, requestID, input)
				if errors.Is(e, mediatasks.ErrBusy) {
					time.Sleep(time.Millisecond)
					continue
				}
				if e != nil {
					t.Error(e)
					return
				}
				ids <- task.ID
				return
			}
			t.Error("capacity never released")
		}()
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id == "" {
			id = got
		}
		require.Equal(t, id, got)
	}
	require.NotEmpty(t, id)
	require.Eventually(t, func() bool {
		task, e := engine.Get(ctx, owner, mediatasks.Audio, id)
		return e == nil && task.Status == "succeeded"
	}, 3*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, supplier.submits.Load())
	summary, err := q.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, summary.Audio.Consumed)
	require.Zero(t, summary.Audio.Reserved)
	require.EqualValues(t, 18, summary.Audio.Available)
	require.Equal(t, "characters", summary.Audio.Unit)
	require.EqualValues(t, 20, summary.Grants[0].AudioCharacters)
	changed := input
	changed.Emotion = "happy"
	_, err = engine.Create(ctx, owner, mediatasks.Audio, requestID, changed)
	require.ErrorIs(t, err, mediatasks.ErrConflict)
	// A new engine represents process restart. Private content and ownership are
	// recovered from PostgreSQL; there is no extra supplier submission.
	restarted := mediatasks.NewEngine(NewOrganizationMediaTaskRepository(db), mediaTaskTestQuota{q}, routes, supplier)
	content, typ, err := restarted.Content(ctx, owner, mediatasks.Audio, id)
	require.NoError(t, err)
	require.Len(t, content, 417)
	require.Equal(t, "audio/mpeg", typ)
	rotated := mediatasks.Owner{UserID: owner.UserID, APIKeyID: 88, GroupID: 17}
	replayed, err := restarted.Create(ctx, rotated, mediatasks.Audio, requestID, input)
	require.NoError(t, err)
	require.Equal(t, id, replayed.ID)
	content, typ, err = restarted.Content(ctx, rotated, mediatasks.Audio, id)
	require.NoError(t, err)
	require.Len(t, content, 417)
	require.Equal(t, "audio/mpeg", typ)
	require.EqualValues(t, 1, supplier.submits.Load())
	_, err = restarted.Get(ctx, mediatasks.Owner{UserID: 2, APIKeyID: 7, GroupID: 5}, mediatasks.Audio, id)
	require.ErrorIs(t, err, mediatasks.ErrNotFound)
	_, err = restarted.Get(ctx, owner, mediatasks.Model3D, id)
	require.ErrorIs(t, err, mediatasks.ErrNotFound)
	require.Zero(t, routes.calls.Load())
	// Last standard 3D task cannot be overspent by concurrent independent IDs.
	var accepted atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rid := fmt.Sprintf("123e4567-e89b-42d3-a456-%012d", i+1)
			for attempt := 0; attempt < 50; attempt++ {
				_, e := engine.Create(ctx, owner, mediatasks.Model3D, rid, mediatasks.Request{Model: mediatasks.TripoModel, Prompt: "a toy"})
				if errors.Is(e, mediatasks.ErrBusy) {
					time.Sleep(time.Millisecond)
					continue
				}
				if e == nil {
					accepted.Add(1)
				} else if !errors.Is(e, service.ErrMediaQuotaExceeded) {
					t.Error(e)
				}
				return
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 1, accepted.Load())
	summary, err = q.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, summary.Model3D.Reserved)
	require.Zero(t, summary.Model3D.Available)
}

func TestOrganizationMediaTasksRequireGrantAndPreserveUncertain(t *testing.T) {
	db, q := mediaQuotaTestDB(t)
	migration, err := os.ReadFile("../../migrations/247_organization_audio_model3d.sql")
	require.NoError(t, err)
	actorMigration, err := os.ReadFile("../../migrations/249_organization_voice_assets.sql")
	require.NoError(t, err)
	migration = append(migration, actorMigration...)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	ctx := context.Background()
	owner := mediatasks.Owner{UserID: 1, APIKeyID: 7, GroupID: 5}
	supplier := &mediaTaskTestSupplier{}
	routes := &mediaTaskTestRoute{}
	store := NewOrganizationMediaTaskRepository(db)
	engine := mediatasks.NewEngine(store, mediaTaskTestQuota{q}, routes, supplier)
	request := mediatasks.Request{Model: mediatasks.TripoModel, Prompt: "a toy"}
	requestID := "123e4567-e89b-42d3-a456-426614174001"
	_, err = engine.Create(ctx, owner, mediatasks.Model3D, requestID, request)
	require.ErrorIs(t, err, mediatasks.ErrQuotaRequired)
	require.Zero(t, supplier.submits.Load())
	g := mediaGrant("image-only-grant", 1, 0)
	_, err = q.Grant(ctx, 1, g)
	require.NoError(t, err)
	_, err = engine.Create(ctx, owner, mediatasks.Model3D, requestID, request)
	require.ErrorIs(t, err, service.ErrMediaQuotaExceeded)
	require.Zero(t, supplier.submits.Load())
	g = mediaGrant("3d-only-grant", 0, 0)
	g.ExpectedVersion = 1
	g.Model3DTasks = 1
	_, err = q.Grant(ctx, 1, g)
	require.NoError(t, err)
	task, err := engine.Create(ctx, owner, mediatasks.Model3D, requestID, request)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		v, e := store.Get(ctx, owner, mediatasks.Model3D, task.ID)
		return e == nil && v.ProviderID != ""
	}, 3*time.Second, 10*time.Millisecond)
	restarted := mediatasks.NewEngine(store, mediaTaskTestQuota{q}, routes, supplier)
	task, err = restarted.Get(ctx, owner, mediatasks.Model3D, task.ID)
	require.NoError(t, err)
	require.Equal(t, "indeterminate", task.Status)
	require.EqualValues(t, 1, supplier.submits.Load())
	require.EqualValues(t, 1, supplier.polls.Load())
	summary, err := q.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, summary.Model3D.Reserved)
	require.Zero(t, summary.Model3D.Consumed)
	_, err = restarted.Create(ctx, owner, mediatasks.Model3D, requestID, request)
	require.NoError(t, err)
	require.EqualValues(t, 1, supplier.submits.Load())
	// A process lost before submitting leaves no provider ID. Expired execution
	// leases become indeterminate, never automatically resubmitted or refunded.
	_, err = db.Exec(`UPDATE organization_media_tasks SET provider_id='',lease_until=NOW()-INTERVAL '1 minute',status='running' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	task, err = restarted.Get(ctx, owner, mediatasks.Model3D, task.ID)
	require.NoError(t, err)
	require.Equal(t, "indeterminate", task.Status)
	require.EqualValues(t, 1, supplier.submits.Load())
	var stored string
	err = db.QueryRow(`SELECT digest||quota_request_id FROM organization_media_tasks WHERE id=$1`, task.ID).Scan(&stored)
	require.NoError(t, err)
	require.False(t, strings.Contains(stored, "a toy"))
}

func TestOrganizationMediaTasksCreateFailureRetriesOriginalReservation(t *testing.T) {
	db, q := mediaQuotaTestDB(t)
	migration, err := os.ReadFile("../../migrations/247_organization_audio_model3d.sql")
	require.NoError(t, err)
	actorMigration, err := os.ReadFile("../../migrations/249_organization_voice_assets.sql")
	require.NoError(t, err)
	migration = append(migration, actorMigration...)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	ctx := context.Background()
	g := mediaGrant("audio-create-failure", 0, 0)
	g.AudioCharacters = 2
	_, err = q.Grant(ctx, 1, g)
	require.NoError(t, err)
	store := &mediaTaskFailCreateStore{Store: NewOrganizationMediaTaskRepository(db)}
	supplier := &mediaTaskTestSupplier{}
	engine := mediatasks.NewEngine(store, mediaTaskTestQuota{q}, &mediaTaskTestRoute{}, supplier)
	o := mediatasks.Owner{UserID: 1, APIKeyID: 7, GroupID: 5}
	requestID := "123e4567-e89b-42d3-a456-426614174005"
	input := mediatasks.Request{Model: mediatasks.SpeechModel, Input: "你好", Speed: 1}
	_, err = engine.Create(ctx, o, mediatasks.Audio, requestID, input)
	require.ErrorIs(t, err, mediatasks.ErrUnavailable)
	require.Zero(t, supplier.submits.Load())
	summary, err := q.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, summary.Audio.Reserved)
	o.APIKeyID = 88
	task, err := engine.Create(ctx, o, mediatasks.Audio, requestID, input)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		v, e := engine.Get(ctx, o, mediatasks.Audio, task.ID)
		return e == nil && v.Status == "succeeded"
	}, 3*time.Second, 10*time.Millisecond)
	summary, err = q.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.Len(t, summary.Items, 1)
	require.EqualValues(t, 2, summary.Audio.Consumed)
	require.Zero(t, summary.Audio.Reserved)
	require.EqualValues(t, 1, supplier.submits.Load())
}
