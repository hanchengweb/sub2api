package voiceassets

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	sync.Mutex
	values map[string]*Asset
	used   map[Slot]bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{values: map[string]*Asset{}, used: map[Slot]bool{}}
}
func owned(a *Asset, o mediatasks.Owner) bool {
	return a.Owner.UserID == o.UserID && a.Owner.ActorID == o.ActorID && !a.Deleted
}
func cloneAsset(a *Asset) *Asset { v := *a; return &v }
func (s *memoryStore) List(_ context.Context, o mediatasks.Owner) ([]*Asset, error) {
	s.Lock()
	defer s.Unlock()
	v := []*Asset{}
	for _, a := range s.values {
		if owned(a, o) {
			v = append(v, cloneAsset(a))
		}
	}
	return v, nil
}
func (s *memoryStore) Get(_ context.Context, o mediatasks.Owner, id string) (*Asset, error) {
	s.Lock()
	defer s.Unlock()
	a := s.values[id]
	if a == nil || !owned(a, o) {
		return nil, mediatasks.ErrNotFound
	}
	return cloneAsset(a), nil
}
func (s *memoryStore) CreateOfficial(_ context.Context, a *Asset) error {
	s.Lock()
	defer s.Unlock()
	s.values[a.ID] = cloneAsset(a)
	return nil
}
func (s *memoryStore) ClaimClone(_ context.Context, a *Asset, slots []Slot) (*Asset, bool, error) {
	s.Lock()
	defer s.Unlock()
	for _, old := range s.values {
		if old.Owner.UserID == a.Owner.UserID && old.Owner.ActorID == a.Owner.ActorID && old.RequestID == a.RequestID {
			if old.Digest != a.Digest {
				return nil, false, mediatasks.ErrConflict
			}
			if old.Deleted {
				return nil, false, mediatasks.ErrNotFound
			}
			return cloneAsset(old), false, nil
		}
	}
	for _, slot := range slots {
		if !s.used[slot] {
			s.used[slot] = true
			a.AccountID, a.SpeakerID = slot.AccountID, slot.SpeakerID
			s.values[a.ID] = cloneAsset(a)
			return a, true, nil
		}
	}
	return nil, false, ErrNoSlot
}
func (s *memoryStore) Patch(ctx context.Context, o mediatasks.Owner, id string, name *string, enabled *bool) (*Asset, error) {
	a, e := s.Get(ctx, o, id)
	if e != nil {
		return nil, e
	}
	s.Lock()
	defer s.Unlock()
	if name != nil {
		a.Name = *name
	}
	if enabled != nil {
		a.Enabled = *enabled
	}
	s.values[id] = cloneAsset(a)
	return a, nil
}
func (s *memoryStore) Delete(ctx context.Context, o mediatasks.Owner, id string) error {
	a, e := s.Get(ctx, o, id)
	if e != nil {
		return e
	}
	s.Lock()
	defer s.Unlock()
	a.Deleted = true
	a.Enabled = false
	s.values[id] = a
	return nil
}
func (s *memoryStore) UpdateTraining(_ context.Context, a *Asset) error {
	s.Lock()
	defer s.Unlock()
	s.values[a.ID] = cloneAsset(a)
	return nil
}
func (s *memoryStore) HasFreeSlot(_ context.Context, slots []Slot) (bool, error) {
	s.Lock()
	defer s.Unlock()
	for _, slot := range slots {
		if !s.used[slot] {
			return true, nil
		}
	}
	return false, nil
}

type fixtureRoutes struct {
	slots     []Slot
	pinned    int64
	reopenErr error
}

func (r *fixtureRoutes) Resolve(context.Context, mediatasks.Owner, string, string) (mediatasks.Provider, error) {
	return mediatasks.Provider{AccountID: 99, Kind: mediatasks.Audio, Key: "fixture-key"}, nil
}
func (r *fixtureRoutes) Reopen(_ context.Context, _ mediatasks.Owner, t *mediatasks.Task) (mediatasks.Provider, error) {
	if r.reopenErr != nil {
		return mediatasks.Provider{}, r.reopenErr
	}
	r.pinned = t.AccountID
	return mediatasks.Provider{AccountID: t.AccountID, Kind: mediatasks.Audio, Key: "fixture-key"}, nil
}
func (r *fixtureRoutes) CloneSlots(context.Context, mediatasks.Owner) ([]Slot, error) {
	return r.slots, nil
}

type fixtureSupplier struct {
	trains, queries atomic.Int32
	result          TrainingResult
	t               *testing.T
	existing        bool
}

func (s *fixtureSupplier) Train(_ context.Context, p mediatasks.Provider, _ string, speaker, format string, b []byte, _ string) TrainingResult {
	s.trains.Add(1)
	if s.t != nil {
		require.Equal(s.t, int64(7), p.AccountID)
		require.Equal(s.t, "S_prepaid", speaker)
		require.Equal(s.t, "wav", format)
		require.True(s.t, ValidSample(b, "wav"))
	}
	return s.result
}
func (s *fixtureSupplier) Query(context.Context, mediatasks.Provider, string, string) TrainingResult {
	if s.queries.Add(1) == 1 && !s.existing {
		return TrainingResult{Status: "untrained"}
	}
	return TrainingResult{Status: "ready"}
}
func wavSample() []byte {
	b := make([]byte, 44+32000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], 1)
	binary.LittleEndian.PutUint32(b[24:28], 16000)
	binary.LittleEndian.PutUint32(b[28:32], 32000)
	binary.LittleEndian.PutUint16(b[32:34], 2)
	binary.LittleEndian.PutUint16(b[34:36], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:44], 32000)
	return b
}
func fixtureService() (*Service, *fixtureSupplier, mediatasks.Owner) {
	supplier := &fixtureSupplier{result: TrainingResult{Status: "indeterminate", ErrorCode: "voice_clone_unconfirmed"}}
	s := &Service{Store: newMemoryStore(), Routes: &fixtureRoutes{slots: []Slot{{7, "S_prepaid"}}}, Supplier: supplier, Decode: func(_ context.Context, b []byte, _ string) ([]byte, error) { return b, nil }}
	return s, supplier, mediatasks.Owner{UserID: 1, GroupID: 3, ActorID: uuid.NewString()}
}
func TestCloneUnknownReplayRefreshAndAccountPinning(t *testing.T) {
	s, provider, o := fixtureService()
	provider.t = t
	ctx := context.Background()
	id := uuid.NewString()
	a, e := s.Clone(ctx, o, id, "我的声音", "wav", "这是测试语音", wavSample(), true)
	require.NoError(t, e)
	require.Equal(t, "indeterminate", a.Status)
	require.False(t, a.CanSynthesize)
	replay, e := s.Clone(ctx, o, id, "我的声音", "wav", "这是测试语音", wavSample(), true)
	require.NoError(t, e)
	require.Equal(t, a.ID, replay.ID)
	require.Equal(t, int32(1), provider.trains.Load())
	a, e = s.Refresh(ctx, o, a.ID)
	require.NoError(t, e)
	require.True(t, a.CanSynthesize)
	require.Equal(t, int32(2), provider.queries.Load())
	r, p, e := s.ResolveVoiceAsset(ctx, o, mediatasks.Request{Model: mediatasks.CloneModel, VoiceAssetID: a.ID})
	require.NoError(t, e)
	require.Equal(t, int64(7), p.AccountID)
	require.Equal(t, "S_prepaid", r.ResolvedVoice)
	require.Equal(t, mediatasks.CloneModel, r.ResourceID)
	b, e := json.Marshal(a)
	require.NoError(t, e)
	for _, private := range []string{"S_prepaid", "fixture-key", "account_id", "speaker_id"} {
		require.NotContains(t, string(b), private)
	}
	other := o
	other.ActorID = uuid.NewString()
	_, e = s.Get(ctx, other, a.ID)
	require.ErrorIs(t, e, mediatasks.ErrNotFound)
	disabled := false
	_, e = s.Patch(ctx, o, a.ID, nil, &disabled)
	require.NoError(t, e)
	_, _, e = s.ResolveVoiceAsset(ctx, o, mediatasks.Request{Model: a.Model, VoiceAssetID: a.ID})
	require.ErrorIs(t, e, mediatasks.ErrRoute)
	require.NoError(t, s.Delete(ctx, o, a.ID))
	_, e = s.Clone(ctx, o, uuid.NewString(), "另一声音", "wav", "这是测试语音", wavSample(), true)
	require.ErrorIs(t, e, ErrNoSlot)
	require.Equal(t, int32(1), provider.trains.Load())
}
func TestCloneRejectsBeforeProviderAndNeverPurchases(t *testing.T) {
	s, p, o := fixtureService()
	for _, test := range []struct {
		consent            bool
		name, format, demo string
		audio              []byte
		target             error
	}{{false, "声音", "wav", "", wavSample(), ErrConsent}, {true, "声音", "wav", "短", wavSample(), mediatasks.ErrInvalid}, {true, "声音", "wav", "", []byte("<html>bad</html>"), mediatasks.ErrInvalid}, {true, strings.Repeat("声", 81), "wav", "", wavSample(), mediatasks.ErrInvalid}} {
		_, e := s.Clone(context.Background(), o, uuid.NewString(), test.name, test.format, test.demo, test.audio, test.consent)
		require.ErrorIs(t, e, test.target)
	}
	require.Zero(t, p.trains.Load())
}
func TestCloneOneSlotConcurrentClaims(t *testing.T) {
	s, p, o := fixtureService()
	var wg sync.WaitGroup
	var success, full atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Clone(context.Background(), o, uuid.NewString(), "声音", "wav", "", wavSample(), true)
			if e == nil {
				success.Add(1)
			} else if errors.Is(e, ErrNoSlot) {
				full.Add(1)
			} else {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), success.Load())
	require.Equal(t, int32(7), full.Load())
	require.Equal(t, int32(1), p.trains.Load())
}
func TestDecodeWavAndRejectMalformedSamples(t *testing.T) {
	require.True(t, ValidSample(wavSample(), "wav"))
	require.False(t, ValidSample(wavSample()[:45], "wav"))
	require.False(t, ValidSample(make([]byte, MaxSampleBytes+1), "mp3"))
	if !DecoderAvailable() {
		t.Skip("ffmpeg not installed")
	}
	b, e := DecodeSample(context.Background(), wavSample(), "wav")
	require.NoError(t, e)
	require.True(t, ValidSample(b, "wav"))
	require.Len(t, b, 32044)
}
func TestPreexistingSupplierVoiceIsNeverOverwrittenOrAdopted(t *testing.T) {
	s, p, o := fixtureService()
	p.existing = true
	a, e := s.Clone(context.Background(), o, uuid.NewString(), "个人声音", "wav", "", wavSample(), true)
	require.NoError(t, e)
	require.Equal(t, "failed", a.Status)
	require.Zero(t, p.trains.Load())
	require.False(t, a.CanSynthesize)
	a, e = s.Refresh(context.Background(), o, a.ID)
	require.NoError(t, e)
	require.False(t, a.CanSynthesize)
	require.Equal(t, int32(1), p.queries.Load())
}
func TestRouteFailureCannotAdoptAnUnverifiedExistingSupplierVoice(t *testing.T) {
	s, p, o := fixtureService()
	routes := s.Routes.(*fixtureRoutes)
	routes.reopenErr = mediatasks.ErrRoute
	p.existing = true
	a, e := s.Clone(context.Background(), o, uuid.NewString(), "个人声音", "wav", "", wavSample(), true)
	require.NoError(t, e)
	require.False(t, a.CanSynthesize)
	require.Equal(t, "voice_clone_verification_pending", a.Error.Code)
	routes.reopenErr = nil
	a, e = s.Refresh(context.Background(), o, a.ID)
	require.NoError(t, e)
	require.False(t, a.CanSynthesize)
	require.Zero(t, p.queries.Load())
	require.Zero(t, p.trains.Load())
}
func TestDecodeMP3AndM4AFromMemoryOnly(t *testing.T) {
	if !DecoderAvailable() {
		t.Skip("ffmpeg not installed")
	}
	for _, format := range []string{"mp3", "m4a"} {
		t.Run(format, func(t *testing.T) {
			args := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "pipe", "-i", "pipe:0"}
			if format == "mp3" {
				args = append(args, "-f", "mp3", "pipe:1")
			} else {
				args = append(args, "-c:a", "aac", "-movflags", "frag_keyframe+empty_moov", "-brand", "M4A ", "-f", "mp4", "pipe:1")
			}
			cmd := exec.Command("ffmpeg", args...)
			cmd.Stdin = bytes.NewReader(wavSample())
			encoded, e := cmd.Output()
			require.NoError(t, e)
			require.True(t, ValidSample(encoded, format))
			canonical, e := DecodeSample(context.Background(), encoded, format)
			require.NoError(t, e)
			require.True(t, ValidSample(canonical, "wav"))
		})
	}
}
