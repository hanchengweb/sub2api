package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Use is only recorded while idle reclamation is enabled. Enabling it long after
// migration 254 must not treat the unrecorded months as proof that a voice is idle.
func TestVoiceExpiryNeedsAFullObservedWindowBeforeReclaiming(t *testing.T) {
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180})
	_, err := db.Exec(`DELETE FROM organization_voice_usage_tracking`)
	require.NoError(t, err)
	actor := uuid.NewString()
	o := mediatasks.Owner{UserID: 1, ActorID: actor}
	id := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_unobserved", 400)
	kept := func(why string) {
		t.Helper()
		for _, run := range []func(context.Context, int64) ([]voiceassets.ReclaimCandidate, error){r.Preview, r.Run} {
			items, err := run(ctx, 1)
			require.NoError(t, err)
			require.Empty(t, items, why)
		}
		_, err := store.Get(ctx, o, id)
		require.NoError(t, err, why)
	}

	kept("nothing has been observed yet")
	var rows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_usage_tracking`).Scan(&rows))
	require.Zero(t, rows, "preview and scans of one organization never start tracking")

	started, err := r.Store.TrackVoiceUsage(ctx)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), started, time.Minute)
	kept("tracking has only just started")

	observeVoiceUse(t, db, 179*24*time.Hour, time.Hour)
	kept("the observation window is still shorter than the idle period")

	// Switched off for longer than the tolerated gap: the old start is worthless,
	// both before and after the next heartbeat.
	observeVoiceUse(t, db, 400*24*time.Hour, voiceassets.ReclaimTrackingGap+time.Hour)
	kept("a stale heartbeat proves nothing")
	restarted, err := r.Store.TrackVoiceUsage(ctx)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), restarted, time.Minute)
	kept("a lapsed heartbeat restarts the window")

	// An unbroken window longer than the idle period survives the next heartbeat.
	observeVoiceUse(t, db, 181*24*time.Hour, time.Hour)
	continued, err := r.Store.TrackVoiceUsage(ctx)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(-181*24*time.Hour), continued, time.Minute)
	items, err := r.Run(ctx, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Reclaimed)
	_, err = store.Get(ctx, o, id)
	require.ErrorIs(t, err, mediatasks.ErrNotFound)
}

// The published schema removal must leave the migration runnable again.
func TestVoiceReclaimSchemaRemovalStepAllowsReapplying254(t *testing.T) {
	ctx := context.Background()
	db, _, _ := voiceSlotTestDB(t, true)
	const name = "254_organization_voice_reclaim.sql"
	migration, err := os.ReadFile("../../migrations/" + name)
	require.NoError(t, err)
	files := fstest.MapFS{name: &fstest.MapFile{Data: migration}}
	require.NoError(t, applyMigrationsFS(ctx, db, files))

	raw, err := os.ReadFile("../../../docs/organization-voice-3d.md")
	require.NoError(t, err)
	_, section, ok := strings.Cut(strings.ReplaceAll(string(raw), "\r\n", "\n"), "Removing migration 254")
	require.True(t, ok)
	_, section, ok = strings.Cut(section, "```sql\n")
	require.True(t, ok)
	removal, _, ok := strings.Cut(section, "```")
	require.True(t, ok)
	_, err = db.Exec(removal)
	require.NoError(t, err)

	var left int
	require.NoError(t, db.QueryRow(`SELECT
	(SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('organization_voice_reclaims','organization_voice_member_states','organization_voice_usage_tracking'))
	+(SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='organization_voice_assets' AND column_name='last_used_at')
	+(SELECT COUNT(*) FROM schema_migrations WHERE filename=$1)`, name).Scan(&left))
	require.Zero(t, left)
	require.NoError(t, applyMigrationsFS(ctx, db, files))
}

// A clone whose training result arrives late has not been idle: its period starts
// when it becomes usable, and later refreshes do not extend it.
func TestVoiceExpiryStartsWhenACloneBecomesUsable(t *testing.T) {
	t.Setenv("WXM_VOICE_EXPIRY_RECLAIM_ENABLED", "true")
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180})
	actor := uuid.NewString()
	o := mediatasks.Owner{UserID: 1, ActorID: actor}
	id := reclaimAsset(t, db, 1, actor, "clone", "indeterminate", "S_late", 400)
	asset, err := store.Get(ctx, o, id)
	require.NoError(t, err)
	asset.Status, asset.Error = "ready", nil
	require.NoError(t, store.UpdateTraining(ctx, asset))
	items, err := r.Run(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, items, "a voice that only now became usable cannot be idle")

	_, err = db.Exec(`UPDATE organization_voice_assets SET last_used_at=NOW()-INTERVAL '181 days' WHERE id=$1`, id)
	require.NoError(t, err)
	require.NoError(t, store.UpdateTraining(ctx, asset))
	items, err = r.Run(ctx, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Reclaimed)
}
