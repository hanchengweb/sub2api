// Package mediatasks implements the bounded official-provider task contract.
// It never falls back to a chat, reseller or free speech route.
package mediatasks

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	Audio                  = "audio"
	Model3D                = "model3d"
	SpeechModel            = "seed-tts-2.0"
	CloneModel             = "seed-icl-2.0"
	TripoModel             = "tripo-v3.1-standard"
	TripoNativeModel       = "v3.1-20260211"
	MaxAudioBytes          = 16 << 20
	MaxModelBytes          = 128 << 20
	MaxModelImageBytes     = 20 << 20
	MaxModelImageDimension = 8192
	MaxModelImagePixels    = 32_000_000
	MaxModel3DRequestBytes = 4*((MaxModelImageBytes+2)/3) + (16 << 10)
)

var (
	ErrNotFound         = errors.New("media_task_not_found")
	ErrConflict         = errors.New("media_task_conflict")
	ErrInvalid          = errors.New("media_request_invalid")
	ErrRoute            = errors.New("media_route_unavailable")
	ErrCredentials      = errors.New("media_credentials_missing")
	ErrQuotaRequired    = errors.New("media_quota_required")
	ErrBusy             = errors.New("media_provider_busy")
	ErrUnavailable      = errors.New("media_task_unavailable")
	ErrImageInvalid     = errors.New("model3d_image_invalid")
	ErrImageUnavailable = errors.New("model3d_image_unavailable")
)

type Owner struct {
	UserID, APIKeyID, GroupID int64
	ActorID                   string
}
type Request struct {
	Model          string          `json:"model"`
	Input          string          `json:"input,omitempty"`
	Voice          string          `json:"voice,omitempty"`
	VoiceAssetID   string          `json:"voice_asset_id,omitempty"`
	ResolvedVoice  string          `json:"-"`
	ResourceID     string          `json:"-"`
	Scene          string          `json:"scene,omitempty"`
	Emotion        string          `json:"emotion,omitempty"`
	Instruction    string          `json:"instruction,omitempty"`
	Speed          float64         `json:"speed,omitempty"`
	ResponseFormat string          `json:"response_format,omitempty"`
	Prompt         string          `json:"prompt,omitempty"`
	Image          json.RawMessage `json:"image,omitempty"`
	// Validated upload material stays in this execution only, never in task storage.
	imageFile []byte
	imageMIME string
}
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ErrorMessage(code string) string {
	switch code {
	case "media_provider_unauthorized":
		return "官方模型凭据或音色授权无效，请联系管理员。"
	case "media_provider_balance_insufficient":
		return "官方供应商账户余额不足，请联系管理员。"
	case "media_provider_rate_limited":
		return "官方服务当前限流，本次生成未受理。"
	case "media_provider_rejected":
		return "官方服务拒绝了本次参数，请检查生成设置。"
	case "model3d_upload_failed":
		return "参考图片上传失败，本次未提交3D生成，请保留原任务编号。"
	default:
		return "生成结果需要核对，请查询原任务，勿重复生成。"
	}
}

type Usage struct {
	Quantity      int64           `json:"quantity"`
	Unit          string          `json:"unit"`
	ProviderUsage json.RawMessage `json:"provider_usage,omitempty"`
}
type Task struct {
	ID              string     `json:"id"`
	Object          string     `json:"object"`
	Kind            string     `json:"kind"`
	Model           string     `json:"model"`
	Status          string     `json:"status"`
	Progress        int        `json:"progress"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ContentType     string     `json:"content_type,omitempty"`
	Error           *TaskError `json:"error,omitempty"`
	Usage           Usage      `json:"usage"`
	Owner           Owner      `json:"-"`
	RequestID       string     `json:"-"`
	Digest          string     `json:"-"`
	QuotaRequestID  string     `json:"-"`
	AccountID       int64      `json:"-"`
	ProviderID      string     `json:"-"`
	LeaseUntil      time.Time  `json:"-"`
	SettlementUnits *int64     `json:"-"`
}

// No task input, credentials or signed download URL is persisted by this store.
type Store interface {
	Find(context.Context, Owner, string, string) (*Task, error)
	Create(context.Context, *Task) (*Task, bool, error)
	Get(context.Context, Owner, string, string) (*Task, error)
	Start(context.Context, string) (bool, error)
	Update(context.Context, *Task, []byte) error
	Content(context.Context, Owner, string, string) ([]byte, string, error)
}
type Quota interface {
	Reserve(context.Context, Owner, string, string, string, string, int64) error
	Bind(context.Context, int64, string, string) error
	Validate(context.Context, int64, string) error
	Settle(context.Context, int64, string, string, int64, string) error
}
type Provider struct {
	AccountID int64
	Kind, Key string
}
type Routes interface {
	Resolve(context.Context, Owner, string, string) (Provider, error)
	Reopen(context.Context, Owner, *Task) (Provider, error)
}
type VoiceAssetResolver interface {
	ResolveVoiceAsset(context.Context, Owner, Request) (Request, Provider, error)
}
type Outcome struct {
	Status, ProviderID, ContentType, ErrorCode string
	Progress                                   int
	Content                                    []byte
	ProviderUsage                              json.RawMessage
	SettlementUnits                            *int64
}
type Supplier interface {
	Submit(context.Context, Provider, Owner, string, Request) Outcome
	Poll(context.Context, Provider, string) Outcome
}

func (t *Task) Public() *Task {
	if t == nil {
		return nil
	}
	v := *t
	if v.SettlementUnits != nil {
		v.Usage.Quantity = *v.SettlementUnits
	}
	v.Object = "media.task"
	if v.Kind == Audio {
		v.Usage.Unit = "characters"
	} else {
		v.Usage.Unit = "tasks"
	}
	return &v
}
