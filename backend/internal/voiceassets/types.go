// Package voiceassets owns private, account-bound official and cloned voices.
package voiceassets

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"time"
)

const MaxSampleBytes = 8 << 20

var (
	ErrNoSlot  = errors.New("voice_clone_slot_unavailable")
	ErrConsent = errors.New("voice_clone_consent_required")
)

type Asset struct {
	ID            string                `json:"id"`
	Kind          string                `json:"kind"`
	Name          string                `json:"name"`
	Model         string                `json:"model"`
	SourceVoice   string                `json:"source_voice,omitempty"`
	Enabled       bool                  `json:"enabled"`
	Status        string                `json:"status"`
	CanSynthesize bool                  `json:"can_synthesize"`
	Error         *mediatasks.TaskError `json:"error,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Owner         mediatasks.Owner      `json:"-"`
	SpeakerID     string                `json:"-"`
	AccountID     int64                 `json:"-"`
	RequestID     string                `json:"-"`
	Digest        string                `json:"-"`
	Deleted       bool                  `json:"-"`
	// Recycled is set by a claim of a speaker this platform trained and released.
	Recycled bool `json:"-"`
	// TrainingBaseline is the supplier's remaining trainings before overwriting a
	// recycled speaker; a later "ready" only counts once that number has dropped.
	TrainingBaseline *int `json:"-"`
	// Remaining and RetireSlot carry supplier facts to the slot row on update.
	Remaining  *int `json:"-"`
	RetireSlot bool `json:"-"`
}

func (a *Asset) Public() *Asset {
	v := *a
	v.CanSynthesize = v.Enabled && !v.Deleted && v.Status == "ready"
	if v.Kind == "official" {
		v.SourceVoice = v.SpeakerID
	}
	return &v
}

type Slot struct {
	AccountID int64  `json:"account_id"`
	SpeakerID string `json:"speaker_id"`
}
type Store interface {
	List(context.Context, mediatasks.Owner) ([]*Asset, error)
	Get(context.Context, mediatasks.Owner, string) (*Asset, error)
	CreateOfficial(context.Context, *Asset) error
	ClaimClone(context.Context, *Asset, []Slot) (*Asset, bool, error)
	Patch(context.Context, mediatasks.Owner, string, *string, *bool) (*Asset, error)
	Delete(context.Context, mediatasks.Owner, string) error
	UpdateTraining(context.Context, *Asset) error
	HasFreeSlot(context.Context, mediatasks.Owner, []Slot) (bool, error)
}
type Routes interface {
	Resolve(context.Context, mediatasks.Owner, string, string) (mediatasks.Provider, error)
	Reopen(context.Context, mediatasks.Owner, *mediatasks.Task) (mediatasks.Provider, error)
	CloneSlots(context.Context, mediatasks.Owner) ([]Slot, error)
}
type TrainingResult struct {
	Status, ErrorCode string
	// Activated speakers are permanently fixed by the supplier and cannot retrain.
	Activated bool
	// Remaining is the supplier's available_training_times, when reported.
	Remaining *int
}

// Reusable reports whether a speaker this platform already trained may be
// trained again: training has finished, it is not activated, and attempts remain.
func (r TrainingResult) Reusable() bool {
	return (r.Status == "ready" || r.ErrorCode == "voice_clone_training_failed") && !r.Activated && r.Remaining != nil && *r.Remaining > 0
}

// Unusable reports a definitive supplier answer that the speaker can never be
// trained by a new clone, so the slot must not be offered again.
func (r TrainingResult) Unusable() bool {
	return r.Activated || (r.Remaining != nil && *r.Remaining == 0)
}

type Supplier interface {
	Train(context.Context, mediatasks.Provider, string, string, string, []byte, string) TrainingResult
	Query(context.Context, mediatasks.Provider, string, string) TrainingResult
}
