package handler

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/stretchr/testify/require"
)

func cloneSlotRoutes(extra map[string]any) mediaTaskRoutes {
	a := nativeMediaAccount(mediatasks.Audio)
	a.Credentials["model_mapping"] = map[string]any{mediatasks.CloneModel: mediatasks.CloneModel}
	for key, value := range extra {
		a.Extra[key] = value
	}
	return mediaTaskRoutes{
		accounts: &mediaTaskAccountRepoFixture{accounts: []service.Account{a}},
		groups:   mediaTaskGroupRepoFixture{group: publishedMediaGroup(mediatasks.CloneModel)},
	}
}

func TestCloneSlotsScopePrepaidPoolByAuthenticatedUser(t *testing.T) {
	routes := cloneSlotRoutes(map[string]any{
		"voice_clone_speaker_ids": []string{"S_shared", "S_shared", "invalid"},
		"voice_clone_speaker_ids_by_user": map[string]any{
			"62": []string{"S_private62", "S_shared", "S_private62", "custom_speaker_id"},
			"63": []string{"S_private63"},
			"*":  []string{"S_wildcard"},
			"0":  []string{"S_zero"},
		},
	})
	ctx := context.Background()
	shared := voiceassets.Slot{AccountID: 9, SpeakerID: "S_shared"}
	private := voiceassets.Slot{AccountID: 9, SpeakerID: "S_private62"}
	for _, owner := range []mediatasks.Owner{
		{UserID: 62, APIKeyID: 130, GroupID: 7, ActorID: "11111111-1111-4111-8111-111111111111"},
		{UserID: 62, APIKeyID: 999, GroupID: 7, ActorID: "22222222-2222-4222-8222-222222222222"},
	} {
		slots, err := routes.CloneSlots(ctx, owner)
		require.NoError(t, err)
		require.Equal(t, []voiceassets.Slot{shared, private}, slots)
	}
	// A matching API key ID or actor cannot borrow another authenticated user's pool.
	for _, userID := range []int64{0, -1, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73} {
		slots, err := routes.CloneSlots(ctx, mediatasks.Owner{UserID: userID, APIKeyID: 62, GroupID: 7, ActorID: "11111111-1111-4111-8111-111111111111"})
		require.NoError(t, err)
		require.Equal(t, []voiceassets.Slot{shared}, slots, "user %d", userID)
	}
	slots, err := routes.CloneSlots(ctx, mediatasks.Owner{UserID: 63, GroupID: 7})
	require.NoError(t, err)
	require.Equal(t, []voiceassets.Slot{shared, {AccountID: 9, SpeakerID: "S_private63"}}, slots)
}

func TestCloneSlotsRejectMalformedScopedConfiguration(t *testing.T) {
	for name, scoped := range map[string]any{
		"missing":         nil,
		"empty":           map[string]any{},
		"wrong map type":  []string{"S_private"},
		"missing user":    map[string]any{"63": []string{"S_other"}},
		"wrong list type": map[string]any{"62": "S_private"},
		"mixed list":      map[string]any{"62": []any{"S_private", 62}},
		"invalid IDs":     map[string]any{"62": []string{"", "S_", "custom_speaker_id", "https://example.invalid/S_voice"}},
	} {
		t.Run(name, func(t *testing.T) {
			routes := cloneSlotRoutes(map[string]any{"voice_clone_speaker_ids": []string{"S_shared"}, "voice_clone_speaker_ids_by_user": scoped})
			slots, err := routes.CloneSlots(context.Background(), mediatasks.Owner{UserID: 62, GroupID: 7})
			require.NoError(t, err)
			require.Equal(t, []voiceassets.Slot{{AccountID: 9, SpeakerID: "S_shared"}}, slots)
		})
	}
	for name, flat := range map[string]any{"missing": nil, "empty": []string{}} {
		t.Run(name+" flat pool", func(t *testing.T) {
			routes := cloneSlotRoutes(map[string]any{"voice_clone_speaker_ids": flat, "voice_clone_speaker_ids_by_user": map[string]any{"62": []string{"S_private"}}})
			slots, err := routes.CloneSlots(context.Background(), mediatasks.Owner{UserID: 62, GroupID: 7})
			require.NoError(t, err)
			require.Equal(t, []voiceassets.Slot{{AccountID: 9, SpeakerID: "S_private"}}, slots)
		})
	}
	routes := cloneSlotRoutes(map[string]any{"voice_clone_speaker_ids": "S_invalid_flat", "voice_clone_speaker_ids_by_user": map[string]any{"62": []string{"S_private"}}})
	slots, err := routes.CloneSlots(context.Background(), mediatasks.Owner{UserID: 62, GroupID: 7})
	require.NoError(t, err)
	require.Equal(t, []voiceassets.Slot{{AccountID: 9, SpeakerID: "S_private"}}, slots)
}

type cloneSlotCaptureStore struct {
	voiceassets.Store
	availability []voiceassets.Slot
	claim        []voiceassets.Slot
	stop         error
}

func (s *cloneSlotCaptureStore) HasFreeSlot(_ context.Context, _ mediatasks.Owner, slots []voiceassets.Slot) (bool, error) {
	s.availability = append([]voiceassets.Slot(nil), slots...)
	for _, slot := range slots {
		if slot.SpeakerID == "S_private62" {
			return true, nil
		}
	}
	return false, nil
}

func (s *cloneSlotCaptureStore) ClaimClone(_ context.Context, _ *voiceassets.Asset, slots []voiceassets.Slot) (*voiceassets.Asset, bool, error) {
	s.claim = append([]voiceassets.Slot(nil), slots...)
	return nil, false, s.stop
}

func TestCloneAvailabilityAndClaimUseSameUserScopedSlots(t *testing.T) {
	routes := cloneSlotRoutes(map[string]any{
		"voice_clone_speaker_ids":         []string{"S_shared"},
		"voice_clone_speaker_ids_by_user": map[string]any{"62": []string{"S_private62"}},
	})
	// A valid in-memory one-second WAV reaches the real service's ClaimClone path.
	audio := make([]byte, 44+32000)
	copy(audio, "RIFF")
	binary.LittleEndian.PutUint32(audio[4:8], uint32(len(audio)-8))
	copy(audio[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(audio[16:20], 16)
	binary.LittleEndian.PutUint16(audio[20:22], 1)
	binary.LittleEndian.PutUint16(audio[22:24], 1)
	binary.LittleEndian.PutUint32(audio[24:28], 16000)
	binary.LittleEndian.PutUint32(audio[28:32], 32000)
	binary.LittleEndian.PutUint16(audio[32:34], 2)
	binary.LittleEndian.PutUint16(audio[34:36], 16)
	copy(audio[36:], "data")
	binary.LittleEndian.PutUint32(audio[40:44], 32000)
	for _, userID := range []int64{62, 64} {
		store := &cloneSlotCaptureStore{stop: errors.New("stop before provider calls")}
		svc := voiceassets.Service{Routes: routes, Store: store, Decode: func(_ context.Context, b []byte, _ string) ([]byte, error) { return b, nil }}
		owner := mediatasks.Owner{UserID: userID, APIKeyID: 130, GroupID: 7, ActorID: "11111111-1111-4111-8111-111111111111"}
		available, err := svc.Cloning(context.Background(), owner)
		require.NoError(t, err)
		require.Equal(t, userID == 62, available)
		_, err = svc.Clone(context.Background(), owner, "33333333-3333-4333-8333-333333333333", "Voice", "wav", "", audio, true)
		require.ErrorIs(t, err, store.stop)
		require.Equal(t, store.availability, store.claim)
		if userID != 62 {
			require.Equal(t, []voiceassets.Slot{{AccountID: 9, SpeakerID: "S_shared"}}, store.claim)
		}
	}
}
