package voiceassets

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
)

// shortcut: previews list one batch; add cursor paging when operators need more than 100 candidates.
const ReclaimBatchLimit = 100

// ReclaimTrackingGap is how long the idle-tracking heartbeat may be missing
// before the observation window restarts.
const ReclaimTrackingGap = 24 * time.Hour

type ReclaimConfig struct {
	MemberEnabled, MemberExecute bool
	ExpiryEnabled, ExpiryExecute bool
	IdleDays                     int
}

func ReclaimConfigFromEnv() (ReclaimConfig, error) {
	c := ReclaimConfig{
		MemberEnabled: os.Getenv("WXM_VOICE_MEMBER_RECLAIM_ENABLED") == "true",
		MemberExecute: os.Getenv("WXM_VOICE_MEMBER_RECLAIM_EXECUTE") == "true",
		ExpiryEnabled: os.Getenv("WXM_VOICE_EXPIRY_RECLAIM_ENABLED") == "true",
		ExpiryExecute: os.Getenv("WXM_VOICE_EXPIRY_RECLAIM_EXECUTE") == "true",
		IdleDays:      180,
	}
	if raw := os.Getenv("WXM_VOICE_EXPIRY_IDLE_DAYS"); c.ExpiryEnabled && raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 1 || days > 36500 {
			c.ExpiryEnabled, c.ExpiryExecute = false, false
			return c, errors.New("WXM_VOICE_EXPIRY_IDLE_DAYS must be an integer from 1 to 36500; expiry reclaim disabled")
		}
		c.IdleDays = days
	}
	return c, nil
}

type MemberState struct {
	ActorID string `json:"actor_id"`
	State   string `json:"state"`
	Version int64  `json:"version"`
}

type ReclaimCandidate struct {
	UserID     int64     `json:"user_id"`
	ActorID    string    `json:"actor_id"`
	AssetID    string    `json:"asset_id"`
	Reason     string    `json:"reason"`
	LastUsedAt time.Time `json:"last_used_at"`
	Reclaimed  bool      `json:"reclaimed"`
	AccountID  int64     `json:"-"`
	SpeakerID  string    `json:"-"`
}

type ReclaimStore interface {
	RecordMemberState(context.Context, int64, MemberState) error
	Reclaim(context.Context, int64, ReclaimConfig) ([]ReclaimCandidate, error)
	ReclaimOrganizations(context.Context) ([]int64, error)
	TouchVoiceUse(context.Context, mediatasks.Owner, string) error
	// TrackVoiceUsage records that idle tracking is running and returns the start
	// of the unbroken observation window.
	TrackVoiceUsage(context.Context) (time.Time, error)
}

type Reclaimer struct {
	Store  ReclaimStore
	Config ReclaimConfig
}

func (r *Reclaimer) MemberState(ctx context.Context, userID int64, state MemberState) ([]ReclaimCandidate, error) {
	if !r.Config.MemberEnabled {
		return nil, mediatasks.ErrRoute
	}
	if userID <= 0 || !validOwner(mediatasks.Owner{ActorID: state.ActorID}) || state.Version < 1 || (state.State != "active" && state.State != "disabled" && state.State != "deleted") {
		return nil, mediatasks.ErrInvalid
	}
	if r.Store == nil {
		return nil, mediatasks.ErrUnavailable
	}
	if err := r.Store.RecordMemberState(ctx, userID, state); err != nil {
		return nil, err
	}
	return r.Run(ctx, userID)
}

func (r *Reclaimer) Run(ctx context.Context, userID int64) ([]ReclaimCandidate, error) {
	if !r.Config.MemberEnabled && !r.Config.ExpiryEnabled {
		return []ReclaimCandidate{}, nil
	}
	if userID <= 0 || (r.Config.ExpiryEnabled && (r.Config.IdleDays < 1 || r.Config.IdleDays > 36500)) {
		return nil, mediatasks.ErrInvalid
	}
	if r.Store == nil {
		return nil, mediatasks.ErrUnavailable
	}
	return r.Store.Reclaim(ctx, userID, r.Config)
}

func (r *Reclaimer) Preview(ctx context.Context, userID int64) ([]ReclaimCandidate, error) {
	preview := *r
	preview.Config.MemberExecute, preview.Config.ExpiryExecute = false, false
	return preview.Run(ctx, userID)
}

func (r *Reclaimer) sweep(ctx context.Context) error {
	var failures []error
	// Idle time counts only while use is being recorded, so mark that first. A
	// failure here must not stop member reclamation.
	if r.Config.ExpiryEnabled {
		since, err := r.Store.TrackVoiceUsage(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("idle tracking: %w", err))
		} else {
			log.Printf("voice_reclaim idle_tracking since=%s earliest_idle_candidates=%s", since.UTC().Format(time.RFC3339), since.AddDate(0, 0, r.Config.IdleDays).UTC().Format(time.RFC3339))
		}
	}
	users, err := r.Store.ReclaimOrganizations(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, userID := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		items, err := r.Run(ctx, userID)
		if err != nil {
			failures = append(failures, fmt.Errorf("organization %d: %w", userID, err))
			continue
		}
		for _, item := range items {
			log.Printf("voice_reclaim user_id=%d actor_id=%s asset_id=%s reason=%s reclaimed=%t", item.UserID, item.ActorID, item.AssetID, item.Reason, item.Reclaimed)
		}
		if len(items) == ReclaimBatchLimit {
			log.Printf("voice_reclaim user_id=%d limit_reached=true limit=%d", userID, ReclaimBatchLimit)
		}
	}
	return errors.Join(failures...)
}

// Start returns an idempotent stop function; no worker exists with both gates off.
func (r *Reclaimer) Start() func() {
	if r.Store == nil || (!r.Config.MemberEnabled && !r.Config.ExpiryEnabled) {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			call, stop := context.WithTimeout(ctx, time.Minute)
			err := r.sweep(call)
			stop()
			if err != nil && ctx.Err() == nil {
				log.Printf("voice_reclaim scan failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
