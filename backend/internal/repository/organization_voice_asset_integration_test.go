package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOrganizationVoiceSlotsAtomicAcrossOwnersAndNeverReused(t *testing.T) {
	db, _ := mediaQuotaTestDB(t)
	tx, e := db.Begin()
	require.NoError(t, e)
	for _, file := range []string{"247_organization_audio_model3d.sql", "249_organization_voice_assets.sql", "252_organization_voice_clone_slots.sql"} {
		migration, e := os.ReadFile("../../migrations/" + file)
		require.NoError(t, e)
		_, e = tx.Exec(string(migration))
		require.NoError(t, e)
	}
	require.NoError(t, tx.Commit())
	_, e = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,extra JSONB NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ); INSERT INTO accounts(id,extra) VALUES(7,'{"voice_clone_speaker_ids":["S_prepaid"]}')`)
	require.NoError(t, e)
	store := NewOrganizationVoiceAssetRepository(db)
	ctx := context.Background()
	slot := []voiceassets.Slot{{AccountID: 7, SpeakerID: "S_prepaid"}}
	var claimed atomic.Int32
	var wg sync.WaitGroup
	var winner *voiceassets.Asset
	var lock sync.Mutex
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o := mediatasks.Owner{UserID: int64(i%2 + 1), GroupID: 3, ActorID: uuid.NewString()}
			a := &voiceassets.Asset{ID: uuid.NewString(), Owner: o, Name: "音色", Kind: "clone", Model: mediatasks.CloneModel, RequestID: uuid.NewString(), Digest: strings.Repeat("a", 64)}
			a, created, e := store.ClaimClone(ctx, a, slot)
			if e == nil && created {
				claimed.Add(1)
				lock.Lock()
				winner = a
				lock.Unlock()
			} else if !errors.Is(e, voiceassets.ErrNoSlot) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(1), claimed.Load())
	require.NotNil(t, winner)
	replay, created, e := store.ClaimClone(ctx, winner, slot)
	require.NoError(t, e)
	require.False(t, created)
	require.Equal(t, winner.ID, replay.ID)
	other := winner.Owner
	other.ActorID = uuid.NewString()
	_, e = store.Get(ctx, other, winner.ID)
	require.ErrorIs(t, e, mediatasks.ErrNotFound)
	require.NoError(t, store.Delete(ctx, winner.Owner, winner.ID))
	free, e := store.HasFreeSlot(ctx, winner.Owner, slot)
	require.NoError(t, e)
	require.False(t, free)
	_, _, e = store.ClaimClone(ctx, winner, slot)
	require.ErrorIs(t, e, mediatasks.ErrNotFound)
}
func TestOrganizationMediaTaskActorScopeAndQuotaIdentity(t *testing.T) {
	db, q := mediaQuotaTestDB(t)
	tx, e := db.Begin()
	require.NoError(t, e)
	for _, file := range []string{"247_organization_audio_model3d.sql", "249_organization_voice_assets.sql"} {
		b, e := os.ReadFile("../../migrations/" + file)
		require.NoError(t, e)
		_, e = tx.Exec(string(b))
		require.NoError(t, e)
	}
	require.NoError(t, tx.Commit())
	grant := mediaGrant("actor-grant", 0, 0)
	grant.AudioCharacters = 20
	_, e = q.Grant(context.Background(), 1, grant)
	require.NoError(t, e)
	store := NewOrganizationMediaTaskRepository(db)
	supplier := &mediaTaskTestSupplier{}
	engine := mediatasks.NewEngine(store, mediaTaskTestQuota{q}, &mediaTaskTestRoute{}, supplier)
	ctx := context.Background()
	o := mediatasks.Owner{UserID: 1, APIKeyID: 7, GroupID: 5, ActorID: uuid.NewString()}
	other := o
	other.ActorID = uuid.NewString()
	requestID := uuid.NewString()
	request := mediatasks.Request{Model: mediatasks.SpeechModel, Input: "你好", Speed: 1}
	a, e := engine.Create(ctx, o, mediatasks.Audio, requestID, request)
	require.NoError(t, e)
	b, e := engine.Create(ctx, other, mediatasks.Audio, requestID, request)
	require.NoError(t, e)
	require.NotEqual(t, a.ID, b.ID)
	require.Eventually(t, func() bool {
		a, _ := engine.Get(ctx, o, mediatasks.Audio, a.ID)
		b, _ := engine.Get(ctx, other, mediatasks.Audio, b.ID)
		return a != nil && b != nil && a.Status == "succeeded" && b.Status == "succeeded"
	}, 3*time.Second, 10*time.Millisecond)
	_, e = engine.Get(ctx, other, mediatasks.Audio, a.ID)
	require.ErrorIs(t, e, mediatasks.ErrNotFound)
	_, _, e = engine.Content(ctx, other, mediatasks.Audio, a.ID)
	require.ErrorIs(t, e, mediatasks.ErrNotFound)
	legacy := o
	legacy.ActorID = ""
	_, e = engine.Get(ctx, legacy, mediatasks.Audio, a.ID)
	require.ErrorIs(t, e, mediatasks.ErrNotFound)
	replay, e := engine.Create(ctx, o, mediatasks.Audio, requestID, request)
	require.NoError(t, e)
	require.Equal(t, a.ID, replay.ID)
	require.Equal(t, int32(2), supplier.submits.Load())
	summary, e := q.Summary(ctx, 1, 1)
	require.NoError(t, e)
	require.EqualValues(t, 4, summary.Audio.Consumed)
}
