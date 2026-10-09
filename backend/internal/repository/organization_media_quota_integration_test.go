package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Only an explicitly disposable database is accepted. Never uses application DSNs.
func mediaQuotaTestDB(t *testing.T) (*sql.DB, service.OrganizationMediaQuotaRepository) {
	t.Helper()
	dsn := os.Getenv("MEDIA_QUOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("MEDIA_QUOTA_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/media_quota_test", u.Path)
	require.Equal(t, "127.0.0.1", u.Hostname())
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("media_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(24)
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,account_type TEXT,deleted_at TIMESTAMPTZ); INSERT INTO users VALUES(1,'organization_service',NULL),(2,'organization_service',NULL),(3,'personal',NULL); CREATE TABLE media_task_charges(user_id BIGINT,task_id TEXT);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/238_organization_media_quotas.sql")
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return db, NewOrganizationMediaQuotaRepository(db)
}
func mediaGrant(id string, images, seconds int64) service.MediaQuotaGrant {
	return service.MediaQuotaGrant{RequestID: id, ContractRef: "fixture-contract", Reason: "fixture", Images: images, VideoSeconds: seconds, EffectiveAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour)}
}
func mediaClaim(id, kind string, n int64) service.MediaQuotaClaim {
	return service.MediaQuotaClaim{UserID: 1, APIKeyID: 7, RequestID: id, Digest: strings.Repeat("a", 64), Kind: kind, Model: "fixture-model", Units: n}
}
func TestOrganizationMediaQuotaConcurrentLastUnit(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		t.Run(kind, func(t *testing.T) {
			_, r := mediaQuotaTestDB(t)
			ctx := context.Background()
			g := mediaGrant("grant-one", 1, 5)
			_, err := r.Grant(ctx, 1, g)
			require.NoError(t, err)
			units := int64(1)
			if kind == "video" {
				units = 5
			}
			var accepted atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, e := r.Reserve(ctx, mediaClaim(fmt.Sprintf("parallel-%d", i), kind, units))
					if e == nil {
						accepted.Add(1)
					} else if !errors.Is(e, service.ErrMediaQuotaExceeded) {
						t.Error(e)
					}
				}(i)
			}
			wg.Wait()
			require.EqualValues(t, 1, accepted.Load())
			s, err := r.Summary(ctx, 1, 1)
			require.NoError(t, err)
			b := s.Images
			if kind == "video" {
				b = s.Videos
			}
			require.EqualValues(t, 0, b.Available)
			require.Equal(t, units, b.Reserved)
		})
	}
}
func TestOrganizationMediaQuotaGrantReplayAndOwnerIsolation(t *testing.T) {
	_, r := mediaQuotaTestDB(t)
	ctx := context.Background()
	g := mediaGrant("grant-replay", 5, 0)
	for i := 0; i < 2; i++ {
		v, e := r.Grant(ctx, 1, g)
		require.NoError(t, e)
		require.EqualValues(t, 1, v)
	}
	changed := g
	changed.Images = 6
	_, err := r.Grant(ctx, 1, changed)
	require.ErrorIs(t, err, service.ErrMediaQuotaConflict)
	s, err := r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 5, s.Images.Available)
	require.Len(t, s.Grants, 1)
	_, err = r.Grant(ctx, 3, g)
	require.Error(t, err)
	c := mediaClaim("cross-org-task", "image", 1)
	_, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	require.NoError(t, r.Bind(ctx, 1, c.RequestID, "provider-unique"))
	_, err = r.Grant(ctx, 2, g)
	require.NoError(t, err)
	require.NoError(t, r.Settle(ctx, 2, "", "provider-unique", 1, "provider_output"))
	s, err = r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, s.Images.Reserved)
	_, err = r.Reserve(ctx, mediaClaim("video-no-grant", "video", 5))
	require.ErrorIs(t, err, service.ErrMediaQuotaExceeded)
}
func TestOrganizationMediaQuotaPartialFailureUnknownAndReplay(t *testing.T) {
	_, r := mediaQuotaTestDB(t)
	ctx := context.Background()
	_, err := r.Grant(ctx, 1, mediaGrant("grant-five", 5, 0))
	require.NoError(t, err)
	c := mediaClaim("partial-result", "image", 5)
	_, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	require.NoError(t, r.Bind(ctx, 1, c.RequestID, "task-partial"))
	v, err := r.Reserve(ctx, c)
	require.NoError(t, err)
	require.True(t, v.Replay)
	require.Equal(t, "task-partial", v.TaskID)
	changed := c
	changed.Digest = strings.Repeat("b", 64)
	_, err = r.Reserve(ctx, changed)
	require.ErrorIs(t, err, service.ErrMediaQuotaConflict)
	for i := 0; i < 3; i++ {
		require.NoError(t, r.Settle(ctx, 1, "", "task-partial", 3, "provider_output"))
	}
	s, err := r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, service.MediaQuotaBalance{Granted: 5, Consumed: 3, Available: 2}, s.Images)
	c = mediaClaim("unknown-result", "image", 2)
	_, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	v, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	require.True(t, v.Replay)
	_, err = r.Reserve(ctx, mediaClaim("new-not-retry", "image", 1))
	require.ErrorIs(t, err, service.ErrMediaQuotaExceeded)
	for i := 0; i < 2; i++ {
		require.NoError(t, r.Settle(ctx, 1, c.RequestID, "", 0, "provider_failure"))
	}
	s, err = r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, s.Images.Available)
	require.Zero(t, s.Images.Reserved)
}
func TestOrganizationMediaQuotaExpiryAndLateResultStayOnOriginalContract(t *testing.T) {
	db, r := mediaQuotaTestDB(t)
	ctx := context.Background()
	g := mediaGrant("old-contract", 4, 5)
	_, err := r.Grant(ctx, 1, g)
	require.NoError(t, err)
	c := mediaClaim("old-inflight", "image", 4)
	_, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE organization_media_grants SET expires_at=NOW()-INTERVAL '1 second' WHERE user_id=1`)
	require.NoError(t, err)
	require.ErrorIs(t, r.ValidateDispatch(ctx, 1, c.RequestID), service.ErrMediaQuotaExceeded)
	g = mediaGrant("new-contract", 8, 10)
	g.ExpectedVersion = 1
	_, err = r.Grant(ctx, 1, g)
	require.NoError(t, err)
	require.NoError(t, r.Settle(ctx, 1, c.RequestID, "", 3, "provider_output"))
	s, err := r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 8, s.Images.Available)
	require.Zero(t, s.Images.Consumed)
	var consumed int64
	require.NoError(t, db.QueryRow(`SELECT l.consumed FROM organization_media_lots l JOIN organization_media_grants g ON l.grant_id=g.id WHERE g.request_id='old-contract' AND l.kind='image'`).Scan(&consumed))
	require.EqualValues(t, 3, consumed)
	c = mediaClaim("fixed-video", "video", 5)
	_, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	require.NoError(t, r.Settle(ctx, 1, c.RequestID, "", -1, "fixed_duration"))
	s, err = r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 5, s.Videos.Consumed)
}
func TestOrganizationMediaQuotaProviderOverrunFailsClosed(t *testing.T) {
	_, r := mediaQuotaTestDB(t)
	ctx := context.Background()
	_, err := r.Grant(ctx, 1, mediaGrant("grant-review", 3, 0))
	require.NoError(t, err)
	c := mediaClaim("overrun-request", "image", 1)
	_, err = r.Reserve(ctx, c)
	require.NoError(t, err)
	require.NoError(t, r.Settle(ctx, 1, c.RequestID, "", 2, "provider_output"))
	s, err := r.Summary(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, s.Blocked)
	require.EqualValues(t, 1, s.Images.Reserved)
	_, err = r.Reserve(ctx, mediaClaim("blocked-request", "image", 1))
	require.ErrorIs(t, err, service.ErrMediaQuotaReview)
}
