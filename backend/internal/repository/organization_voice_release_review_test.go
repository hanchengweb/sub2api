package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestVoiceSlotConcurrentDeleteReleasesOnce(t *testing.T) {
	db, store, _ := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()[:1]
	a, _, err := store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	a.Status = "ready"
	require.NoError(t, store.UpdateTraining(ctx, a))
	var deleted, claimed atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			err := store.Delete(ctx, a.Owner, a.ID)
			if err == nil {
				deleted.Add(1)
			} else if !errors.Is(err, mediatasks.ErrNotFound) {
				t.Error(err)
			}
		})
		wg.Go(func() {
			<-start
			_, created, err := store.ClaimClone(ctx, voiceClaim(2), slots)
			if err == nil && created {
				claimed.Add(1)
			} else if !errors.Is(err, voiceassets.ErrNoSlot) {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if claimed.Load() == 0 {
		_, created, err := store.ClaimClone(ctx, voiceClaim(2), slots)
		require.NoError(t, err)
		require.True(t, created)
		claimed.Add(1)
	}
	require.EqualValues(t, 1, deleted.Load())
	require.EqualValues(t, 1, claimed.Load())
	var owner int64
	require.NoError(t, db.QueryRow(`SELECT a.user_id FROM organization_voice_speaker_slots s JOIN organization_voice_assets a ON a.id=s.asset_id WHERE s.speaker_id='S_a'`).Scan(&owner))
	require.EqualValues(t, 2, owner, "late deletes cannot release the new owner's speaker")
}

func TestVoiceSlotRollbackStepRestoresOldAccounting(t *testing.T) {
	db, store, manager := voiceSlotTestDB(t, true)
	ctx := context.Background()
	slots := voiceInventory()
	_, err := manager.SetSlots(ctx, 1, slots, voiceChange(1, 0, 1), 9)
	require.NoError(t, err)
	first, _, err := store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	first.Status = "ready"
	require.NoError(t, store.UpdateTraining(ctx, first))
	require.NoError(t, store.Delete(ctx, first.Owner, first.ID))
	_, _, err = store.ClaimClone(ctx, voiceClaim(1), slots)
	require.NoError(t, err)
	retired, _, err := store.ClaimClone(ctx, voiceClaim(2), slots)
	require.NoError(t, err)
	retired.Status, retired.RetireSlot = "failed", true
	retired.Error = &mediatasks.TaskError{Code: "voice_clone_slot_not_verified_empty"}
	require.NoError(t, store.UpdateTraining(ctx, retired))
	require.NoError(t, store.Delete(ctx, retired.Owner, retired.ID))

	raw, err := os.ReadFile("../../../docs/organization-voice-3d.md")
	require.NoError(t, err)
	_, section, ok := strings.Cut(strings.ReplaceAll(string(raw), "\r\n", "\n"), "Rollback below 253")
	require.True(t, ok)
	_, section, ok = strings.Cut(section, "```sql\n")
	require.True(t, ok)
	rollback, _, ok := strings.Cut(section, "```")
	require.True(t, ok)
	_, err = db.Exec(rollback)
	require.NoError(t, err)
	var restored bool
	require.NoError(t, db.QueryRow(`SELECT COALESCE(asset_id=$1,FALSE) FROM organization_voice_speaker_slots WHERE speaker_id=$2`, retired.ID, retired.SpeakerID).Scan(&restored))
	require.True(t, restored, "rollback must also restore retired, unclaimed speakers without released_at")
	// Reuse retains two historical assets for one speaker: no non-destructive SQL
	// can make the old binary's row count equal its claimed speaker count.
	var legacyUsed, held int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_assets WHERE user_id=1 AND kind='clone'`).Scan(&legacyUsed))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM organization_voice_speaker_slots s JOIN organization_voice_assets a ON a.id=s.asset_id WHERE a.user_id=1`).Scan(&held))
	require.Equal(t, 2, legacyUsed)
	require.Equal(t, 1, held)
}

func TestVoiceSlotMigration253HistoricalCountsAndReplay(t *testing.T) {
	db, _, _ := voiceSlotTestDB(t, false)
	applyVoiceMigration(t, db, "252_organization_voice_clone_slots.sql")
	_, err := db.Exec(`INSERT INTO organization_voice_slot_policies(user_id,total_slots,version) VALUES(1,6,1)`)
	require.NoError(t, err)
	for i, row := range []struct {
		status, code string
		deleted      bool
	}{
		{"ready", "", true}, {"failed", "voice_clone_training_failed", true},
		{"failed", "voice_clone_slot_not_verified_empty", true},
		{"training", "", true}, {"indeterminate", "voice_clone_unconfirmed", true}, {"ready", "", false},
	} {
		id, speaker := uuid.NewString(), "S_"+string(rune('a'+i))
		_, err = db.Exec(`INSERT INTO organization_voice_assets(id,user_id,actor_id,group_id,kind,name,model,speaker_id,account_id,status,error_code,deleted_at)
		VALUES($1,1,$2,4,'clone','synthetic','seed-icl-2.0',$3,7,$4,$5,CASE WHEN $6 THEN NOW() END)`, id, uuid.NewString(), speaker, row.status, row.code, row.deleted)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO organization_voice_speaker_slots(account_id,speaker_id,asset_id,allocated_user_id) VALUES(7,$1,$2,1)`, speaker, id)
		require.NoError(t, err)
	}
	raw, err := os.ReadFile("../../migrations/253_organization_voice_slot_release.sql")
	require.NoError(t, err)
	fs := fstest.MapFS{"253_organization_voice_slot_release.sql": &fstest.MapFile{Data: raw}}
	require.NoError(t, applyMigrationsFS(context.Background(), db, fs))
	require.NoError(t, applyMigrationsFS(context.Background(), db, fs), "runner replay skips the matching checksum")
	var held, released, reserved int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FILTER(WHERE asset_id IS NOT NULL),COUNT(*) FILTER(WHERE released_at IS NOT NULL),COUNT(*) FILTER(WHERE allocated_user_id=1) FROM organization_voice_speaker_slots`).Scan(&held, &released, &reserved))
	require.Equal(t, 3, held)
	require.Equal(t, 2, released)
	require.Equal(t, 6, reserved)
	t.Logf("migration 253: 6 synthetic claims, 3 unbound, 2 reusable markers, 6 managed reservations retained")
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(raw))
	require.ErrorContains(t, err, "already exists")
	require.NoError(t, tx.Rollback())
}
