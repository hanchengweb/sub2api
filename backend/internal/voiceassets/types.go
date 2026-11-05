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
	AccountID int64
	SpeakerID string
}
type Store interface {
	List(context.Context, mediatasks.Owner) ([]*Asset, error)
	Get(context.Context, mediatasks.Owner, string) (*Asset, error)
	CreateOfficial(context.Context, *Asset) error
	ClaimClone(context.Context, *Asset, []Slot) (*Asset, bool, error)
	Patch(context.Context, mediatasks.Owner, string, *string, *bool) (*Asset, error)
	Delete(context.Context, mediatasks.Owner, string) error
	UpdateTraining(context.Context, *Asset) error
	HasFreeSlot(context.Context, []Slot) (bool, error)
}
type Routes interface {
	Resolve(context.Context, mediatasks.Owner, string, string) (mediatasks.Provider, error)
	Reopen(context.Context, mediatasks.Owner, *mediatasks.Task) (mediatasks.Provider, error)
	CloneSlots(context.Context, mediatasks.Owner) ([]Slot, error)
}
type TrainingResult struct{ Status, ErrorCode string }
type Supplier interface {
	Train(context.Context, mediatasks.Provider, string, string, string, []byte, string) TrainingResult
	Query(context.Context, mediatasks.Provider, string, string) TrainingResult
}
