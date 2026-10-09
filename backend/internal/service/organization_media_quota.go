package service

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMediaQuotaExceeded = errors.New("media_quota_exceeded")
	ErrMediaQuotaConflict = errors.New("media_quota_conflict")
	ErrMediaQuotaReview   = errors.New("media_quota_review_required")
)

type MediaQuotaGrant struct {
	RequestID       string    `json:"request_id" binding:"required,min=8,max=180"`
	ExpectedVersion int64     `json:"expected_version" binding:"gte=0"`
	ContractRef     string    `json:"contract_ref" binding:"required,max=160"`
	Reason          string    `json:"reason" binding:"required,max=500"`
	Images          int64     `json:"images" binding:"gte=0,lte=1000000000"`
	VideoSeconds    int64     `json:"video_seconds" binding:"gte=0,lte=1000000000"`
	EffectiveAt     time.Time `json:"effective_at" binding:"required"`
	ExpiresAt       time.Time `json:"expires_at" binding:"required"`
}
type MediaQuotaBalance struct {
	Granted   int64 `json:"granted"`
	Reserved  int64 `json:"reserved"`
	Consumed  int64 `json:"consumed"`
	Available int64 `json:"available"`
}
type MediaQuotaReservation struct {
	ID          int64      `json:"id"`
	RequestID   string     `json:"request_id"`
	Kind        string     `json:"kind"`
	Model       string     `json:"model"`
	Units       int64      `json:"units"`
	ActualUnits *int64     `json:"actual_units"`
	TaskID      string     `json:"task_id"`
	Status      string     `json:"status"`
	Measurement string     `json:"measurement"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Replay      bool       `json:"-"`
}
type MediaQuotaSummary struct {
	Configured bool                    `json:"configured"`
	Version    int64                   `json:"version"`
	Blocked    bool                    `json:"blocked"`
	Images     MediaQuotaBalance       `json:"images"`
	Videos     MediaQuotaBalance       `json:"videos"`
	Grants     []MediaQuotaGrant       `json:"grants"`
	Items      []MediaQuotaReservation `json:"items"`
	HasMore    bool                    `json:"has_more"`
	UpdatedAt  time.Time               `json:"updated_at"`
}
type MediaQuotaClaim struct {
	UserID, APIKeyID               int64
	RequestID, Digest, Kind, Model string
	Units                          int64
}

// All writes serialize on the organization user row. Credentials and generation
// contents never enter this independent quantity ledger.
type OrganizationMediaQuotaRepository interface {
	Enabled(context.Context, int64) (bool, error)
	Grant(context.Context, int64, MediaQuotaGrant) (int64, error)
	Summary(context.Context, int64, int) (*MediaQuotaSummary, error)
	Reserve(context.Context, MediaQuotaClaim) (*MediaQuotaReservation, error)
	Bind(context.Context, int64, string, string) error
	Settle(context.Context, int64, string, string, int64, string) error
	ValidateDispatch(context.Context, int64, string) error
	FailVerifiedTask(context.Context, string) error
}

// Reuse the gateway's output counter, including Responses-style image outputs.
func MediaQuotaImageUnits(body []byte) int64 {
	return int64(countOpenAIResponseImageOutputsFromJSONBytes(body))
}

// A bounded output is required before a contractual video budget can be reserved.
func MediaQuotaVideoUnits(model string, duration int) (int64, error) {
	limit, known := videoUpstreamLimitForModel(model)
	if !known || duration < 0 {
		return 0, errors.New("media_quota_requires_fixed_video_duration")
	}
	if duration == 0 {
		duration = videoDefaultDurationForModel(model)
	}
	if duration < limit.MinSeconds || duration > limit.MaxSeconds {
		return 0, errors.New("media_quota_invalid_video_duration")
	}
	return int64(duration), nil
}
