package voiceassets

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestConfiguredSlotsKeepScopedInventoryWhenSharedIsMissingOrMalformed(t *testing.T) {
	for _, shared := range []any{nil, "invalid", []any{"S_bad", 42}, []string{"S_shared", "S_shared"}} {
		extra := map[string]any{"voice_clone_speaker_ids": shared, "voice_clone_speaker_ids_by_user": map[string]any{"7": []string{"S_private", "S_private"}, "8": []string{"S_other"}}}
		slots := ConfiguredSlots(4, 7, extra)
		require.Contains(t, slots, Slot{AccountID: 4, SpeakerID: "S_private"})
		require.NotContains(t, slots, Slot{AccountID: 4, SpeakerID: "S_other"})
		require.Len(t, SortedSlots(append(slots, slots...)), len(slots))
	}
}

func TestVoiceSlotChangeValidationAndStableDigest(t *testing.T) {
	good := SlotChange{RequestID: uuid.NewString(), ExpectedUserID: 7, Total: 2, Reason: "contract adjustment"}
	require.NoError(t, good.Validate())
	require.Equal(t, good.Digest(), good.Digest())
	for _, mutate := range []func(*SlotChange){
		func(c *SlotChange) { c.Total = -1 }, func(c *SlotChange) { c.Total = 1_000_000_001 }, func(c *SlotChange) { c.ExpectedVersion = -1 },
		func(c *SlotChange) { c.ExpectedUserID = 0 }, func(c *SlotChange) { c.RequestID = "not-a-uuid" }, func(c *SlotChange) { c.Reason = " " }, func(c *SlotChange) { c.Reason = strings.Repeat("字", 501) },
	} {
		bad := good
		mutate(&bad)
		require.ErrorIs(t, bad.Validate(), ErrSlotRequest)
	}
	different := good
	different.Total++
	require.NotEqual(t, good.Digest(), different.Digest())
}

type slotAdminFixture struct {
	Store
	reads, changes int
	slots          []Slot
}

func (f *slotAdminFixture) SlotSummary(_ context.Context, userID int64, slots []Slot) (*SlotSummary, error) {
	f.reads++
	f.slots = slots
	return &SlotSummary{UserID: userID, Allocatable: int64(len(slots))}, nil
}
func (f *slotAdminFixture) SetSlots(_ context.Context, userID int64, slots []Slot, c SlotChange, _ int64) (*SlotSummary, error) {
	f.changes++
	return &SlotSummary{UserID: userID, Managed: true, Version: c.ExpectedVersion + 1, Total: c.Total, Available: c.Total, RequestID: c.RequestID}, nil
}
func TestOrganizationSlotSummaryNeverInvokesSupplierOrWrites(t *testing.T) {
	store := &slotAdminFixture{}
	svc := Service{Store: store, Routes: &fixtureRoutes{slots: []Slot{{AccountID: 7, SpeakerID: "S_prepaid"}}}}
	summary, err := svc.OrganizationSlots(context.Background(), 7, []int64{1, 2})
	require.NoError(t, err)
	require.EqualValues(t, 1, summary.Allocatable)
	require.Equal(t, 1, store.reads)
	require.Zero(t, store.changes)
	data, err := json.Marshal(summary)
	require.NoError(t, err)
	require.NotContains(t, string(data), "S_prepaid")
	_, err = svc.SetOrganizationSlots(context.Background(), 7, []int64{1}, SlotChange{RequestID: uuid.NewString(), ExpectedUserID: 8, Reason: "wrong owner"}, 4)
	require.ErrorIs(t, err, ErrSlotIdentity)
	require.Zero(t, store.changes)
}
