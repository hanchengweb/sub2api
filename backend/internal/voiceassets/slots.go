package voiceassets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/google/uuid"
)

var (
	ErrSlotRequest     = errors.New("voice_clone_slot_request_invalid")
	ErrSlotIdentity    = errors.New("organization_identity_mismatch")
	ErrSlotVersion     = errors.New("voice_clone_slot_version_conflict")
	ErrSlotBelowUsed   = errors.New("voice_clone_slot_below_used")
	ErrSlotInventory   = errors.New("voice_clone_slot_inventory_insufficient")
	ErrSlotIdempotency = errors.New("voice_clone_slot_idempotency_conflict")
)

type SlotSummary struct {
	UserID      int64  `json:"user_id"`
	Managed     bool   `json:"managed"`
	Version     int64  `json:"version"`
	Total       int64  `json:"total_slots"`
	Used        int64  `json:"used_slots"`
	Available   int64  `json:"available_slots"`
	Allocatable int64  `json:"allocatable_slots"`
	RequestID   string `json:"request_id,omitempty"`
}

type SlotChange struct {
	RequestID       string `json:"request_id"`
	ExpectedUserID  int64  `json:"expected_user_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Total           int64  `json:"total_slots"`
	Reason          string `json:"reason"`
}

func (c SlotChange) Validate() error {
	id, err := uuid.Parse(c.RequestID)
	if err != nil || id == uuid.Nil || id.String() != c.RequestID || c.ExpectedUserID <= 0 || c.ExpectedVersion < 0 || c.ExpectedVersion == math.MaxInt64 || c.Total < 0 || c.Total > 1_000_000_000 || !utf8.ValidString(c.Reason) || strings.TrimSpace(c.Reason) != c.Reason || utf8.RuneCountInString(c.Reason) < 1 || utf8.RuneCountInString(c.Reason) > 500 {
		return ErrSlotRequest
	}
	return nil
}

func (c SlotChange) Digest() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SlotAdministration is separate from asset reads; catalog and GET never allocate.
type SlotAdministration interface {
	SlotSummary(context.Context, int64, []Slot) (*SlotSummary, error)
	SetSlots(context.Context, int64, []Slot, SlotChange, int64) (*SlotSummary, error)
}

type SlotManager interface {
	OrganizationSlots(context.Context, int64, []int64) (*SlotSummary, error)
	SetOrganizationSlots(context.Context, int64, []int64, SlotChange, int64) (*SlotSummary, error)
}

func (s *Service) organizationSlots(ctx context.Context, userID int64, groups []int64) ([]Slot, error) {
	var slots []Slot
	for _, group := range groups {
		candidates, err := s.Routes.CloneSlots(ctx, mediatasks.Owner{UserID: userID, GroupID: group})
		if errors.Is(err, mediatasks.ErrRoute) || errors.Is(err, mediatasks.ErrCredentials) {
			continue
		}
		if err != nil {
			return nil, err
		}
		slots = append(slots, candidates...)
	}
	return SortedSlots(slots), nil
}

func (s *Service) OrganizationSlots(ctx context.Context, userID int64, groups []int64) (*SlotSummary, error) {
	store, ok := s.Store.(SlotAdministration)
	if !ok {
		return nil, mediatasks.ErrUnavailable
	}
	slots, err := s.organizationSlots(ctx, userID, groups)
	if err != nil {
		return nil, err
	}
	return store.SlotSummary(ctx, userID, slots)
}

func (s *Service) SetOrganizationSlots(ctx context.Context, userID int64, groups []int64, change SlotChange, actorID int64) (*SlotSummary, error) {
	if err := change.Validate(); err != nil {
		return nil, err
	}
	if change.ExpectedUserID != userID {
		return nil, ErrSlotIdentity
	}
	if actorID <= 0 {
		return nil, ErrSlotRequest
	}
	store, ok := s.Store.(SlotAdministration)
	if !ok {
		return nil, mediatasks.ErrUnavailable
	}
	slots, err := s.organizationSlots(ctx, userID, groups)
	if err != nil {
		return nil, err
	}
	return store.SetSlots(ctx, userID, slots, change, actorID)
}

var prepaidSpeakerID = regexp.MustCompile(`^S_[A-Za-z0-9_-]{1,120}$`)

// ConfiguredSlots reads only the operator's existing prepaid allowlists.
func ConfiguredSlots(accountID, userID int64, extra map[string]any) []Slot {
	raw, _ := json.Marshal(extra["voice_clone_speaker_ids"])
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		ids = nil
	}
	if userID > 0 {
		raw, _ = json.Marshal(extra["voice_clone_speaker_ids_by_user"])
		var byUser map[string]json.RawMessage
		if json.Unmarshal(raw, &byUser) == nil {
			var scoped []string
			if json.Unmarshal(byUser[strconv.FormatInt(userID, 10)], &scoped) == nil {
				ids = append(ids, scoped...)
			}
		}
	}
	seen := map[string]bool{}
	slots := make([]Slot, 0)
	for _, id := range ids {
		if prepaidSpeakerID.MatchString(id) && !seen[id] {
			slots = append(slots, Slot{AccountID: accountID, SpeakerID: id})
			seen[id] = true
		}
	}
	return slots
}

// All writers acquire inventory rows in this global order, independent of priority.
func SortedSlots(slots []Slot) []Slot {
	found := make(map[Slot]bool)
	out := make([]Slot, 0, len(slots))
	for _, slot := range slots {
		if slot.AccountID > 0 && prepaidSpeakerID.MatchString(slot.SpeakerID) && !found[slot] {
			found[slot] = true
			out = append(out, slot)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].SpeakerID < out[j].SpeakerID
	})
	return out
}
