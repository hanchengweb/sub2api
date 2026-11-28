package voiceassets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type reclaimStoreFixture struct {
	ReclaimStore
	calls, states, touches int
	tracked                int
	config                 ReclaimConfig
	err                    error
	scanned                chan ReclaimConfig
}

func (f *reclaimStoreFixture) Reclaim(_ context.Context, _ int64, c ReclaimConfig) ([]ReclaimCandidate, error) {
	f.calls++
	f.config = c
	if f.scanned != nil {
		f.scanned <- c
	}
	return []ReclaimCandidate{}, f.err
}
func (f *reclaimStoreFixture) TrackVoiceUsage(context.Context) (time.Time, error) {
	f.tracked++
	return time.Unix(0, 0), f.err
}
func (f *reclaimStoreFixture) ReclaimOrganizations(context.Context) ([]int64, error) {
	return []int64{1}, nil
}
func (f *reclaimStoreFixture) RecordMemberState(context.Context, int64, MemberState) error {
	f.states++
	return f.err
}
func (f *reclaimStoreFixture) TouchVoiceUse(context.Context, mediatasks.Owner, string) error {
	f.touches++
	return f.err
}

func TestVoiceReclaimConfigAndIndependentSafetyGates(t *testing.T) {
	for _, key := range []string{"WXM_VOICE_MEMBER_RECLAIM_ENABLED", "WXM_VOICE_MEMBER_RECLAIM_EXECUTE", "WXM_VOICE_EXPIRY_RECLAIM_ENABLED", "WXM_VOICE_EXPIRY_RECLAIM_EXECUTE", "WXM_VOICE_EXPIRY_IDLE_DAYS"} {
		t.Setenv(key, "")
	}
	c, err := ReclaimConfigFromEnv()
	require.NoError(t, err)
	require.Equal(t, 180, c.IdleDays)
	require.False(t, c.MemberEnabled || c.MemberExecute || c.ExpiryEnabled || c.ExpiryExecute)
	t.Setenv("WXM_VOICE_EXPIRY_RECLAIM_ENABLED", "true")
	for _, days := range []string{"0", "-1", "1.5", "bad", "36501"} {
		t.Setenv("WXM_VOICE_EXPIRY_IDLE_DAYS", days)
		c, err = ReclaimConfigFromEnv()
		require.Error(t, err, days)
		require.False(t, c.ExpiryEnabled, "invalid expiry must fail closed")
	}
	ctx := context.Background()
	store := &reclaimStoreFixture{}
	r := &Reclaimer{Store: store, Config: ReclaimConfig{MemberExecute: true, ExpiryExecute: true}}
	_, err = r.Run(ctx, 1)
	require.NoError(t, err)
	_, err = r.MemberState(ctx, 1, MemberState{ActorID: uuid.NewString(), State: "disabled", Version: 1})
	require.ErrorIs(t, err, mediatasks.ErrRoute)
	r.Start()()
	require.Zero(t, store.calls+store.states+store.tracked, "disabled and execute-only gates must not touch storage")
	r.Config = ReclaimConfig{MemberEnabled: true, MemberExecute: true, ExpiryEnabled: true, ExpiryExecute: true, IdleDays: 180}
	_, err = r.Preview(ctx, 1)
	require.NoError(t, err)
	require.False(t, store.config.MemberExecute || store.config.ExpiryExecute, "explicit preview always ignores execution flags")
	for _, state := range []MemberState{{ActorID: "bad", State: "disabled", Version: 1}, {ActorID: uuid.NewString(), State: "unknown", Version: 1}, {ActorID: uuid.NewString(), State: "deleted", Version: 0}} {
		_, err = r.MemberState(ctx, 1, state)
		require.ErrorIs(t, err, mediatasks.ErrInvalid)
	}
	require.Zero(t, store.states)
	require.Zero(t, store.tracked, "previews and member events never start idle tracking")
}

func TestVoiceUseOnlyTracksAuthorizedCloneResolutionWhenEnabled(t *testing.T) {
	s, _, _ := fixtureService()
	o := mediatasks.Owner{UserID: 1, GroupID: 4, ActorID: uuid.NewString()}
	a := &Asset{ID: uuid.NewString(), Owner: o, Kind: "clone", Model: mediatasks.CloneModel, SpeakerID: "S_prepaid", AccountID: 7, Enabled: true, Status: "ready"}
	assets, ok := s.Store.(*memoryStore)
	require.True(t, ok)
	assets.values[a.ID] = a
	store := &reclaimStoreFixture{}
	s.Reclaim = &Reclaimer{Store: store}
	r := mediatasks.Request{Model: a.Model, VoiceAssetID: a.ID}
	_, _, err := s.ResolveVoiceAsset(context.Background(), o, r)
	require.NoError(t, err)
	require.Zero(t, store.touches)
	s.Reclaim.Config.ExpiryEnabled = true
	_, _, err = s.ResolveVoiceAsset(context.Background(), o, r)
	require.NoError(t, err)
	require.Equal(t, 1, store.touches)
	a.Enabled = false
	_, _, err = s.ResolveVoiceAsset(context.Background(), o, r)
	require.ErrorIs(t, err, mediatasks.ErrRoute)
	require.Equal(t, 1, store.touches)
	a.Enabled = true
	store.err = errors.New("use record failed")
	_, _, err = s.ResolveVoiceAsset(context.Background(), o, r)
	require.ErrorIs(t, err, store.err, "never resolve an asset if the atomic usage check failed")
}

func TestVoiceReclaimStartupScanAndStop(t *testing.T) {
	store := &reclaimStoreFixture{scanned: make(chan ReclaimConfig, 1)}
	r := &Reclaimer{Store: store, Config: ReclaimConfig{ExpiryEnabled: true, IdleDays: 180}}
	stop := r.Start()
	t.Cleanup(stop)
	select {
	case c := <-store.scanned:
		require.True(t, c.ExpiryEnabled)
		require.False(t, c.ExpiryExecute, "startup with only ENABLED must preview")
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not scan")
	}
	stop()
	stop()
	require.Equal(t, 1, store.calls)
	require.Equal(t, 1, store.tracked, "the idle scan records that tracking is running")
}

func TestVoiceMemberOnlyScanDoesNotTrackIdleUse(t *testing.T) {
	store := &reclaimStoreFixture{}
	r := &Reclaimer{Store: store, Config: ReclaimConfig{MemberEnabled: true}}
	require.NoError(t, r.sweep(context.Background()))
	require.Equal(t, 1, store.calls)
	require.Zero(t, store.tracked)
}
