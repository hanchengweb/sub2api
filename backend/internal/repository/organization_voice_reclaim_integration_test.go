package repository

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func voiceReclaimTestDB(t *testing.T, config voiceassets.ReclaimConfig) (*sql.DB, voiceassets.Store, *voiceassets.Reclaimer) {
	t.Helper()
	db, store, _ := voiceSlotTestDB(t, true)
	applyVoiceMigration(t, db, "254_organization_voice_reclaim.sql")
	store.(*organizationVoiceAssetRepository).memberReclaim = config.MemberEnabled && config.MemberExecute
	return db, store, &voiceassets.Reclaimer{Store: store.(voiceassets.ReclaimStore), Config: config}
}

func reclaimAsset(t *testing.T, db *sql.DB, user int64, actor, kind, status, speaker string, days int) string {
	t.Helper()
	id := uuid.NewString()
	_, err := db.Exec(`INSERT INTO organization_voice_assets(id,user_id,actor_id,group_id,kind,name,model,speaker_id,account_id,status,error_code,last_used_at)
	VALUES($1,$2,$3,4,$4,'synthetic','seed-icl-2.0',$5,7,$6::VARCHAR,CASE WHEN $6::VARCHAR='failed' THEN 'voice_clone_training_failed' ELSE '' END,NOW()-$7::INTEGER*INTERVAL '1 day')`, id, user, actor, kind, speaker, status, days)
	require.NoError(t, err)
	if kind == "clone" {
		_, err = db.Exec(`INSERT INTO organization_voice_speaker_slots(account_id,speaker_id,asset_id,allocated_user_id) VALUES(7,$1,$2,$3)`, speaker, id, user)
		require.NoError(t, err)
	}
	return id
}

func TestVoiceMemberReclaimPreviewExecuteReplayAndReenable(t *testing.T) {
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{MemberEnabled: true, IdleDays: 180})
	actor, otherActor := uuid.NewString(), uuid.NewString()
	_, err := db.Exec(`INSERT INTO organization_voice_slot_policies(user_id,total_slots,version) VALUES(1,3,1)`)
	require.NoError(t, err)
	ready := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_a", 0)
	failed := reclaimAsset(t, db, 1, actor, "clone", "failed", "S_b", 0)
	pending := reclaimAsset(t, db, 1, actor, "clone", "training", "S_c", 0)
	official := reclaimAsset(t, db, 1, actor, "official", "ready", "official", 999)
	other := reclaimAsset(t, db, 2, actor, "clone", "ready", "S_other", 0)
	otherMember := reclaimAsset(t, db, 1, otherActor, "clone", "ready", "S_other_member", 0)
	event := voiceassets.MemberState{ActorID: actor, State: "disabled", Version: 1}
	preview, err := r.MemberState(ctx, 1, event)
	require.NoError(t, err)
	require.Len(t, preview, 2)
	for _, item := range preview {
		require.False(t, item.Reclaimed)
		require.Equal(t, "member_disabled", item.Reason)
	}
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_reclaims`).Scan(&n))
	require.Zero(t, n)
	r.Config.MemberExecute = true
	store.(*organizationVoiceAssetRepository).memberReclaim = true
	items, err := r.MemberState(ctx, 1, event)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, item := range items {
		require.True(t, item.Reclaimed)
	}
	for _, id := range []string{ready, failed} {
		_, err = store.Get(ctx, mediatasks.Owner{UserID: 1, ActorID: actor}, id)
		require.ErrorIs(t, err, mediatasks.ErrNotFound)
	}
	for _, value := range []struct {
		user      int64
		actor, id string
	}{{1, actor, pending}, {1, actor, official}, {2, actor, other}, {1, otherActor, otherMember}} {
		_, err = store.Get(ctx, mediatasks.Owner{UserID: value.user, ActorID: value.actor}, value.id)
		require.NoError(t, err)
	}
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_speaker_slots WHERE allocated_user_id=1 AND asset_id IS NULL AND released_at IS NOT NULL`).Scan(&n))
	require.Equal(t, 2, n, "managed organization retains its released reservations")
	items, err = r.MemberState(ctx, 1, event)
	require.NoError(t, err)
	require.Empty(t, items)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_reclaims WHERE user_id=1 AND actor_id=$1 AND account_id=7 AND speaker_id IN ('S_a','S_b') AND reason='member_disabled' AND reclaimed_at IS NOT NULL`, actor).Scan(&n))
	require.Equal(t, 2, n)
	claim := voiceClaim(1)
	claim.Owner.ActorID = actor
	_, _, err = store.ClaimClone(ctx, claim, voiceInventory())
	require.ErrorIs(t, err, mediatasks.ErrRoute, "inactive member cannot consume another slot")
	_, err = r.MemberState(ctx, 1, voiceassets.MemberState{ActorID: actor, State: "active", Version: 2})
	require.NoError(t, err)
	_, _, err = store.ClaimClone(ctx, claim, voiceInventory())
	require.NoError(t, err, "reactivation allows a new recording")
	_, err = store.Get(ctx, claim.Owner, ready)
	require.ErrorIs(t, err, mediatasks.ErrNotFound, "reactivation never restores an overwritten voice")
	_, err = r.MemberState(ctx, 1, event)
	require.ErrorIs(t, err, mediatasks.ErrConflict, "stale disabled event cannot overwrite active v2")
	_, err = r.MemberState(ctx, 1, voiceassets.MemberState{ActorID: actor, State: "deleted", Version: 2})
	require.ErrorIs(t, err, mediatasks.ErrConflict)
}

func TestVoiceExpiryReclaimTracksUseAndNeverDeletesInPreview(t *testing.T) {
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{ExpiryEnabled: true, IdleDays: 180})
	actor := uuid.NewString()
	o := mediatasks.Owner{UserID: 1, ActorID: actor}
	old := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_old", 181)
	touched := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_touched", 181)
	fresh := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_fresh", 179)
	pending := reclaimAsset(t, db, 1, actor, "clone", "indeterminate", "S_pending", 999)
	official := reclaimAsset(t, db, 1, actor, "official", "ready", "official", 999)
	wrong := o
	wrong.ActorID = uuid.NewString()
	require.ErrorIs(t, r.Store.TouchVoiceUse(ctx, wrong, touched), mediatasks.ErrNotFound)
	require.NoError(t, r.Store.TouchVoiceUse(ctx, o, touched))
	r.Config.ExpiryExecute = true
	items, err := r.Preview(ctx, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, old, items[0].AssetID)
	require.False(t, items[0].Reclaimed)
	items, err = r.Run(ctx, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Reclaimed)
	require.Equal(t, "idle_expired", items[0].Reason)
	for _, id := range []string{touched, fresh, pending, official} {
		_, err = store.Get(ctx, o, id)
		require.NoError(t, err)
	}
	require.ErrorIs(t, r.Store.TouchVoiceUse(ctx, o, old), mediatasks.ErrNotFound)
	var allocated sql.NullInt64
	require.NoError(t, db.QueryRow(`SELECT allocated_user_id FROM organization_voice_speaker_slots WHERE speaker_id='S_old' AND asset_id IS NULL`).Scan(&allocated))
	require.False(t, allocated.Valid, "legacy release returns to the shared pool")
	items, err = r.Run(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestVoiceReclaimConcurrencyAndAuditFailureAreAtomic(t *testing.T) {
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{MemberEnabled: true, MemberExecute: true, ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180})
	actor := uuid.NewString()
	id := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_a", 181)
	o := mediatasks.Owner{UserID: 1, ActorID: actor}
	// A failed audit must undo both the deletion and the speaker release.
	_, err := db.Exec(`ALTER TABLE organization_voice_reclaims ADD CONSTRAINT reject_fixture CHECK (reason <> 'idle_expired')`)
	require.NoError(t, err)
	_, err = r.Run(ctx, 1)
	require.Error(t, err)
	_, err = store.Get(ctx, o, id)
	require.NoError(t, err)
	var held bool
	require.NoError(t, db.QueryRow(`SELECT asset_id=$1 FROM organization_voice_speaker_slots WHERE speaker_id='S_a'`, id).Scan(&held))
	require.True(t, held)
	_, err = db.Exec(`ALTER TABLE organization_voice_reclaims DROP CONSTRAINT reject_fixture`)
	require.NoError(t, err)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			<-start
			_, e := r.MemberState(ctx, 1, voiceassets.MemberState{ActorID: actor, State: "deleted", Version: 1})
			if e != nil {
				t.Error(e)
			}
		})
		wg.Go(func() {
			<-start
			_, e := r.Run(ctx, 1)
			if e != nil {
				t.Error(e)
			}
		})
	}
	close(start)
	wg.Wait()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_reclaims WHERE asset_id=$1`, id).Scan(&n))
	require.Equal(t, 1, n)
	_, err = store.Get(ctx, o, id)
	require.ErrorIs(t, err, mediatasks.ErrNotFound)
}

func TestVoiceExpiryMigrationGivesHistoricalAssetsFullGrace(t *testing.T) {
	db, store, _ := voiceSlotTestDB(t, true)
	a, _, err := store.ClaimClone(context.Background(), voiceClaim(1), voiceInventory())
	require.NoError(t, err)
	a.Status = "ready"
	require.NoError(t, store.UpdateTraining(context.Background(), a))
	_, err = db.Exec(`UPDATE organization_voice_assets SET created_at=NOW()-INTERVAL '900 days'`)
	require.NoError(t, err)
	before := time.Now().Add(-time.Second)
	applyVoiceMigration(t, db, "254_organization_voice_reclaim.sql")
	var last time.Time
	require.NoError(t, db.QueryRow(`SELECT last_used_at FROM organization_voice_assets WHERE id=$1`, a.ID).Scan(&last))
	require.True(t, last.After(before))
	r := &voiceassets.Reclaimer{Store: store.(voiceassets.ReclaimStore), Config: voiceassets.ReclaimConfig{ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180}}
	items, err := r.Run(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestVoiceExpiryUseAndReclaimSerialize(t *testing.T) {
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180})
	actor := uuid.NewString()
	id := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_a", 181)
	o := mediatasks.Owner{UserID: 1, ActorID: actor}
	start := make(chan struct{})
	var touchErr, runErr error
	var items []voiceassets.ReclaimCandidate
	var wg sync.WaitGroup
	wg.Go(func() { <-start; touchErr = r.Store.TouchVoiceUse(ctx, o, id) })
	wg.Go(func() { <-start; items, runErr = r.Run(ctx, 1) })
	close(start)
	wg.Wait()
	require.NoError(t, runErr)
	_, err := store.Get(ctx, o, id)
	if touchErr == nil {
		require.NoError(t, err)
		require.Empty(t, items, "a committed use cannot be reclaimed with a stale preview")
	} else {
		require.ErrorIs(t, touchErr, mediatasks.ErrNotFound)
		require.ErrorIs(t, err, mediatasks.ErrNotFound)
		require.Len(t, items, 1)
	}
}

func TestVoiceReclaimDisabledAndMemberPreviewCannotBeBypassed(t *testing.T) {
	ctx := context.Background()
	db, store, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{MemberExecute: true, ExpiryExecute: true, IdleDays: 180})
	actor := uuid.NewString()
	id := reclaimAsset(t, db, 1, actor, "clone", "ready", "S_a", 181)
	items, err := r.Run(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, items)
	_, err = r.MemberState(ctx, 1, voiceassets.MemberState{ActorID: actor, State: "disabled", Version: 1})
	require.ErrorIs(t, err, mediatasks.ErrRoute)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_member_states`).Scan(&n))
	require.Zero(t, n)
	r.Config = voiceassets.ReclaimConfig{MemberEnabled: true, ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180}
	items, err = r.MemberState(ctx, 1, voiceassets.MemberState{ActorID: actor, State: "disabled", Version: 1})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, items[0].Reclaimed, "expiry execution cannot bypass member preview")
	_, err = store.Get(ctx, mediatasks.Owner{UserID: 1, ActorID: actor}, id)
	require.NoError(t, err)
}

func TestVoiceReclaimLocksSpeakersInGlobalOrder(t *testing.T) {
	ctx := context.Background()
	db, _, r := voiceReclaimTestDB(t, voiceassets.ReclaimConfig{ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180})
	actor := uuid.NewString()
	reclaimAsset(t, db, 1, actor, "clone", "ready", "S_a", 181)
	reclaimAsset(t, db, 1, actor, "clone", "ready", "S_b", 182)
	blocker, err := db.Begin()
	require.NoError(t, err)
	defer blocker.Rollback()
	_, err = blocker.Exec(`SET LOCAL statement_timeout='5s'; SELECT id FROM users WHERE id=2 FOR UPDATE; SELECT speaker_id FROM organization_voice_speaker_slots WHERE speaker_id='S_a' FOR UPDATE`)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, e := r.Run(ctx, 1); done <- e }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE organization_voice_speaker_slots%')`).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	// Slot administration holds S_a before S_b. Reclamation must do the same,
	// even when S_b is the older candidate selected first for expiry.
	_, err = blocker.Exec(`SELECT speaker_id FROM organization_voice_speaker_slots WHERE speaker_id='S_b' FOR UPDATE`)
	require.NoError(t, err)
	require.NoError(t, blocker.Commit())
	require.NoError(t, <-done)
}
