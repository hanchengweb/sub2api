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
		files = append(files, "252_organization_voice_clone_slots.sql")
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
	raw, err := os.ReadFile("../../migrations/252_organization_voice_clone_slots.sql")
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(raw))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	var owner int64
	require.NoError(t, db.QueryRow(`SELECT allocated_user_id FROM organization_voice_speaker_slots WHERE speaker_id='S_historical'`).Scan(&owner))
	require.EqualValues(t, 1, owner)
	value, err := manager.SlotSummary(context.Background(), 1, voiceInventory())
	require.NoError(t, err)
	require.False(t, value.Managed)
	require.EqualValues(t, 1, value.Used)
	require.EqualValues(t, 1, value.Total)
	_, err = manager.SetSlots(context.Background(), 1, voiceInventory(), voiceChange(1, 0, 0), 9)
	require.ErrorIs(t, err, voiceassets.ErrSlotBelowUsed)
	value, err = manager.SetSlots(context.Background(), 1, voiceInventory(), voiceChange(1, 0, 1), 9)
	require.NoError(t, err)
	require.EqualValues(t, 1, value.Total)
	require.Zero(t, value.Available)
	_, err = store.Get(context.Background(), mediatasks.Owner{UserID: 1, ActorID: actor}, id)
	require.ErrorIs(t, err, mediatasks.ErrNotFound, "soft-delete ownership remains private and unavailable")
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
