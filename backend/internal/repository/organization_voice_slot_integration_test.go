package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func voiceSlotTestDB(t *testing.T, migrate bool) (*sql.DB, voiceassets.Store, voiceassets.SlotAdministration) {
	t.Helper()
	db, _ := mediaQuotaTestDB(t)
	tx, err := db.Begin()
	require.NoError(t, err)
	files := []string{"247_organization_audio_model3d.sql", "249_organization_voice_assets.sql"}
	if migrate {
		files = append(files, "252_organization_voice_clone_slots.sql", "253_organization_voice_slot_release.sql")
	}
	for _, file := range files {
		raw, e := os.ReadFile("../../migrations/" + file)
		require.NoError(t, e)
		_, e = tx.Exec(string(raw))
		require.NoError(t, e)
	}
	require.NoError(t, tx.Commit())
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,extra JSONB NOT NULL,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
        INSERT INTO accounts(id,extra) VALUES(7,'{"voice_clone_speaker_ids":["S_a","S_b","S_c"]}')`)
	require.NoError(t, err)
	store := NewOrganizationVoiceAssetRepository(db)
	return db, store, store.(voiceassets.SlotAdministration)
}
func voiceInventory() []voiceassets.Slot {
	return []voiceassets.Slot{{AccountID: 7, SpeakerID: "S_a"}, {AccountID: 7, SpeakerID: "S_b"}, {AccountID: 7, SpeakerID: "S_c"}}
}
func voiceChange(user, version, total int64) voiceassets.SlotChange {
	return voiceassets.SlotChange{RequestID: uuid.NewString(), ExpectedUserID: user, ExpectedVersion: version, Total: total, Reason: "fixture contract"}
}
func voiceClaim(user int64) *voiceassets.Asset {
	return &voiceassets.Asset{ID: uuid.NewString(), Owner: mediatasks.Owner{UserID: user, GroupID: 4, ActorID: uuid.NewString()}, Kind: "clone", Model: mediatasks.CloneModel, Name: "synthetic voice", RequestID: uuid.NewString(), Digest: strings.Repeat("a", 64)}
}

func TestVoiceSlotManagementGetIsReadOnlyAndReceiptsReplay(t *testing.T) {
	db, _, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()
	before, err := manager.SlotSummary(ctx, 1, slots)
	require.NoError(t, err)
	require.False(t, before.Managed)
	require.Zero(t, before.Total)
	require.EqualValues(t, 3, before.Allocatable)
	for _, table := range []string{"organization_voice_speaker_slots", "organization_voice_slot_policies", "organization_voice_slot_changes"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		require.Zero(t, n)
	}
	change := voiceChange(1, 0, 2)
	first, err := manager.SetSlots(ctx, 1, slots, change, 9)
	require.NoError(t, err)
	require.True(t, first.Managed)
	require.EqualValues(t, 1, first.Version)
	require.EqualValues(t, 2, first.Total)
	require.EqualValues(t, 2, first.Available)
	require.EqualValues(t, 1, first.Allocatable)
	replay, err := manager.SetSlots(ctx, 1, nil, change, 9)
	require.NoError(t, err)
	require.Equal(t, first, replay)
	other, err := manager.SlotSummary(ctx, 2, slots)
	require.NoError(t, err)
	require.EqualValues(t, 1, other.Allocatable)
	_, err = manager.SetSlots(ctx, 1, slots, voiceChange(1, 1, 1), 9)
	require.NoError(t, err)
	replay, err = manager.SetSlots(ctx, 1, nil, change, 10)
	require.NoError(t, err)
	require.Equal(t, first, replay, "replay is the original committed receipt")
	changed := change
	changed.Reason = "changed request"
	_, err = manager.SetSlots(ctx, 1, slots, changed, 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotIdempotency)
	_, err = manager.SetSlots(ctx, 1, slots, voiceChange(1, 1, 2), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotVersion)
	wrong := voiceChange(2, 2, 2)
	_, err = manager.SetSlots(ctx, 1, slots, wrong, 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotIdentity)
	same := voiceChange(1, 2, 1)
	last, err := manager.SetSlots(ctx, 1, slots, same, 9)
	require.NoError(t, err)
	require.EqualValues(t, 3, last.Version)
	var receipts, version int64
	require.NoError(t, db.QueryRow(`SELECT COUNT(*),MAX(version) FROM organization_voice_slot_changes WHERE user_id=1`).Scan(&receipts, &version))
	require.EqualValues(t, 3, receipts)
	require.EqualValues(t, 3, version)
	var actor int64
	var reason string
	require.NoError(t, db.QueryRow(`SELECT actor_user_id,reason FROM organization_voice_slot_changes WHERE user_id=1 AND request_id=$1`, change.RequestID).Scan(&actor, &reason))
	require.EqualValues(t, 9, actor)
	require.Equal(t, change.Reason, reason)
}

func TestVoiceSlotManagedClaimsAcrossActorsAndSoftDeletion(t *testing.T) {
	_, store, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()
	_, err := manager.SetSlots(ctx, 1, slots, voiceChange(1, 0, 2), 9)
	require.NoError(t, err)
	var successes atomic.Int32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var claimed []*voiceassets.Asset
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			asset, created, e := store.ClaimClone(ctx, voiceClaim(1), slots)
			if e == nil && created {
				successes.Add(1)
				mu.Lock()
				claimed = append(claimed, asset)
				mu.Unlock()
			} else if !errors.Is(e, voiceassets.ErrNoSlot) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 2, successes.Load())
	summary, err := manager.SlotSummary(ctx, 1, slots)
	require.NoError(t, err)
	require.EqualValues(t, 2, summary.Used)
	require.Zero(t, summary.Available)
	free, err := store.HasFreeSlot(ctx, claimed[0].Owner, slots)
	require.NoError(t, err)
	require.False(t, free, "catalog cannot see the unreserved shared third slot")
	legacy, created, err := store.ClaimClone(ctx, voiceClaim(2), slots)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "S_c", legacy.SpeakerID)
	require.NoError(t, store.Delete(ctx, claimed[0].Owner, claimed[0].ID))
	summary, err = manager.SlotSummary(ctx, 1, slots)
	require.NoError(t, err)
	require.EqualValues(t, 2, summary.Used)
	_, err = manager.SetSlots(ctx, 1, slots, voiceChange(1, 1, 1), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotBelowUsed)
	_, _, err = store.ClaimClone(ctx, voiceClaim(1), slots)
	require.ErrorIs(t, err, voiceassets.ErrNoSlot)
	_, err = store.Get(ctx, claimed[1].Owner, claimed[1].ID)
	require.NoError(t, err, "existing asset remains readable")
	other := claimed[1].Owner
	other.ActorID = uuid.NewString()
	_, err = store.Get(ctx, other, claimed[1].ID)
	require.ErrorIs(t, err, mediatasks.ErrNotFound)
}

func TestVoiceSlotConcurrentAllocationAndLegacyCannotBorrowReservation(t *testing.T) {
	db, store, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()
	var successes atomic.Int32
	var winner atomic.Int64
	var wg sync.WaitGroup
	for _, user := range []int64{1, 2} {
		wg.Add(1)
		go func(user int64) {
			defer wg.Done()
			reversed := []voiceassets.Slot{slots[2], slots[1], slots[0]}
			_, err := manager.SetSlots(ctx, user, reversed, voiceChange(user, 0, 2), 9)
			if err == nil {
				successes.Add(1)
				winner.Store(user)
			} else if !errors.Is(err, voiceassets.ErrSlotInventory) {
				t.Error(err)
			}
		}(user)
	}
	wg.Wait()
	require.EqualValues(t, 1, successes.Load())
	loser := int64(3) - winner.Load()
	first, created, err := store.ClaimClone(ctx, voiceClaim(loser), slots)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "S_c", first.SpeakerID)
	_, _, err = store.ClaimClone(ctx, voiceClaim(loser), slots)
	require.ErrorIs(t, err, voiceassets.ErrNoSlot)
	var allocated int64
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_speaker_slots WHERE allocated_user_id IS NOT NULL`).Scan(&allocated))
	require.EqualValues(t, 3, allocated)
	change := voiceChange(winner.Load(), 1, 2)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := manager.SetSlots(ctx, winner.Load(), slots, change, 9)
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	var receipts int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_slot_changes WHERE user_id=$1`, winner.Load()).Scan(&receipts))
	require.Equal(t, 2, receipts)
}

func TestVoiceSlotReductionReleasesOnlyUnusedAndZeroBlocksSharedPool(t *testing.T) {
	_, store, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()
	_, err := manager.SetSlots(ctx, 1, slots, voiceChange(1, 0, 2), 9)
	require.NoError(t, err)
	value, err := manager.SetSlots(ctx, 1, slots, voiceChange(1, 1, 0), 9)
	require.NoError(t, err)
	require.Zero(t, value.Total)
	require.EqualValues(t, 3, value.Allocatable)
	free, err := store.HasFreeSlot(ctx, mediatasks.Owner{UserID: 1}, slots)
	require.NoError(t, err)
	require.False(t, free)
	_, _, err = store.ClaimClone(ctx, voiceClaim(1), slots)
	require.ErrorIs(t, err, voiceassets.ErrNoSlot)
	_, created, err := store.ClaimClone(ctx, voiceClaim(2), slots)
	require.NoError(t, err)
	require.True(t, created)
	value, err = manager.SetSlots(ctx, 1, slots, voiceChange(1, 2, 1), 9)
	require.NoError(t, err)
	require.EqualValues(t, 1, value.Available)
	claimed, created, err := store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "S_b", claimed.SpeakerID)
}

func TestVoiceSlotMigrationAdoptsHistoricalDeletedClaims(t *testing.T) {
	db, store, manager := voiceSlotTestDB(t, false)
	id := uuid.NewString()
	actor := uuid.NewString()
	_, err := db.Exec(`INSERT INTO organization_voice_assets(id,user_id,actor_id,group_id,kind,name,model,speaker_id,account_id,status,deleted_at)
        VALUES($1,1,$2,4,'clone','synthetic historical','seed-icl-2.0','S_historical',7,'ready',NOW())`, id, actor)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO organization_voice_speaker_slots(account_id,speaker_id,asset_id) VALUES(7,'S_historical',$1)`, id)
	require.NoError(t, err)
	applyVoiceMigration(t, db, "252_organization_voice_clone_slots.sql")
	var owner int64
	require.NoError(t, db.QueryRow(`SELECT allocated_user_id FROM organization_voice_speaker_slots WHERE speaker_id='S_historical'`).Scan(&owner))
	require.EqualValues(t, 1, owner)
	// 253 returns the finished deleted claim; a legacy organization gives it back
	// to the shared pool, marked as trained by this platform.
	applyVoiceMigration(t, db, "253_organization_voice_slot_release.sql")
	var allocated sql.NullInt64
	var released bool
	require.NoError(t, db.QueryRow(`SELECT allocated_user_id,released_at IS NOT NULL FROM organization_voice_speaker_slots WHERE speaker_id='S_historical' AND asset_id IS NULL`).Scan(&allocated, &released))
	require.False(t, allocated.Valid)
	require.True(t, released)
	value, err := manager.SlotSummary(context.Background(), 1, voiceInventory())
	require.NoError(t, err)
	require.False(t, value.Managed)
	require.Zero(t, value.Used)
	_, err = store.Get(context.Background(), mediatasks.Owner{UserID: 1, ActorID: actor}, id)
	require.ErrorIs(t, err, mediatasks.ErrNotFound, "soft-deleted asset remains private and unavailable")
}

func applyVoiceMigration(t *testing.T, db *sql.DB, file string) {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/" + file)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(raw))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func TestVoiceSlotMigrationReturnsManagedDeletedClaimsOnly(t *testing.T) {
	db, _, manager := voiceSlotTestDB(t, false)
	applyVoiceMigration(t, db, "252_organization_voice_clone_slots.sql")
	_, err := db.Exec(`INSERT INTO organization_voice_slot_policies(user_id,total_slots,version) VALUES(1,3,1)`)
	require.NoError(t, err)
	actor := uuid.NewString()
	for _, row := range []struct {
		speaker, status, code string
		deleted               bool
	}{{"S_a", "ready", "", true}, {"S_b", "indeterminate", "voice_clone_unconfirmed", true}, {"S_c", "ready", "", false}} {
		id := uuid.NewString()
		_, err = db.Exec(`INSERT INTO organization_voice_assets(id,user_id,actor_id,group_id,kind,name,model,speaker_id,account_id,status,error_code,deleted_at)
            VALUES($1,1,$2,4,'clone','synthetic','seed-icl-2.0',$3,7,$4,$5,CASE WHEN $6 THEN NOW() END)`, id, actor, row.speaker, row.status, row.code, row.deleted)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO organization_voice_speaker_slots(account_id,speaker_id,asset_id,allocated_user_id) VALUES(7,$1,$2,1)`, row.speaker, id)
		require.NoError(t, err)
	}
	var held int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_speaker_slots WHERE asset_id IS NOT NULL`).Scan(&held))
	require.Equal(t, 3, held)
	applyVoiceMigration(t, db, "253_organization_voice_slot_release.sql")
	after, err := manager.SlotSummary(context.Background(), 1, voiceInventory())
	require.NoError(t, err)
	require.EqualValues(t, 2, after.Used, "only the finished deleted clone is returned")
	require.EqualValues(t, 1, after.Available)
	var owner int64
	require.NoError(t, db.QueryRow(`SELECT allocated_user_id FROM organization_voice_speaker_slots WHERE speaker_id='S_a' AND asset_id IS NULL AND released_at IS NOT NULL`).Scan(&owner))
	require.EqualValues(t, 1, owner, "managed reservation stays with its organization")
}

func TestVoiceSlotDeleteReturnsFinishedCloneAndRetiresDeadSpeakers(t *testing.T) {
	db, store, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()
	_, err := manager.SetSlots(ctx, 1, slots, voiceChange(1, 0, 2), 9)
	require.NoError(t, err)
	first, _, err := store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	require.False(t, first.Recycled)
	second, _, err := store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	first.Status = "ready"
	require.NoError(t, store.UpdateTraining(ctx, first))

	require.NoError(t, store.Delete(ctx, first.Owner, first.ID))
	summary, err := manager.SlotSummary(ctx, 1, slots)
	require.NoError(t, err)
	require.EqualValues(t, 1, summary.Used)
	require.EqualValues(t, 1, summary.Available)
	free, err := store.HasFreeSlot(ctx, first.Owner, slots)
	require.NoError(t, err)
	require.True(t, free)
	_, _, err = store.ClaimClone(ctx, voiceClaim(2), slots)
	require.NoError(t, err, "legacy organization takes the unreserved shared speaker")
	again, created, err := store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, first.SpeakerID, again.SpeakerID)
	require.True(t, again.Recycled)

	// Baseline survives later updates; supplier facts reach the speaker row.
	baseline, remaining := 14, 13
	again.Status = "indeterminate"
	again.TrainingBaseline, again.Remaining = &baseline, &remaining
	require.NoError(t, store.UpdateTraining(ctx, again))
	got, err := store.Get(ctx, again.Owner, again.ID)
	require.NoError(t, err)
	require.Equal(t, 14, *got.TrainingBaseline)
	var stored int
	require.NoError(t, db.QueryRow(`SELECT remaining_trainings FROM organization_voice_speaker_slots WHERE asset_id=$1`, again.ID).Scan(&stored))
	require.Equal(t, 13, stored)

	// The training is still unsettled: deletion keeps the speaker held.
	require.NoError(t, store.Delete(ctx, second.Owner, second.ID))
	summary, err = manager.SlotSummary(ctx, 1, slots)
	require.NoError(t, err)
	require.EqualValues(t, 2, summary.Used)

	// A retired speaker is neither offered nor counted as allocatable.
	again.Status, again.RetireSlot = "failed", true
	again.Error = &mediatasks.TaskError{Code: "voice_clone_slot_not_verified_empty"}
	require.NoError(t, store.UpdateTraining(ctx, again))
	require.NoError(t, store.Delete(ctx, again.Owner, again.ID))
	free, err = store.HasFreeSlot(ctx, again.Owner, slots)
	require.NoError(t, err)
	require.False(t, free)
	_, _, err = store.ClaimClone(ctx, voiceClaim(1), slots)
	require.ErrorIs(t, err, voiceassets.ErrNoSlot)
	_, err = manager.SetSlots(ctx, 1, slots, voiceChange(1, 1, 2), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotInventory, "a dead speaker cannot fill a reservation")
}

func TestVoiceSlotInventoryRechecksOnlyConfiguredSupplierSlots(t *testing.T) {
	db, _, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	candidate := []voiceassets.Slot{{AccountID: 7, SpeakerID: "S_forged"}}
	value, err := manager.SlotSummary(ctx, 1, candidate)
	require.NoError(t, err)
	require.Zero(t, value.Allocatable)
	_, err = manager.SetSlots(ctx, 1, candidate, voiceChange(1, 0, 1), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotInventory)
	_, err = db.Exec(`UPDATE accounts SET extra='{"voice_clone_speaker_ids_by_user":{"1":["S_private1"],"2":["S_private2"]}}'`)
	require.NoError(t, err)
	scoped := []voiceassets.Slot{{AccountID: 7, SpeakerID: "S_private1"}, {AccountID: 7, SpeakerID: "S_private2"}}
	value, err = manager.SlotSummary(ctx, 1, scoped)
	require.NoError(t, err)
	require.EqualValues(t, 1, value.Allocatable)
	_, err = manager.SetSlots(ctx, 1, scoped, voiceChange(1, 0, 2), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotInventory)
	_, err = db.Exec(`UPDATE accounts SET extra='{}'`)
	require.NoError(t, err)
	_, err = manager.SetSlots(ctx, 1, scoped, voiceChange(1, 0, 1), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotInventory, "stale route metadata cannot create inventory")
}
