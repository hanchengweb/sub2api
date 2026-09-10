package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrUsageBillingRequestIDRequired = errors.New("usage billing request_id is required")
var ErrUsageBillingRequestConflict = errors.New("usage billing request fingerprint conflict")

// UsageBillingCommand describes one billable request that must be applied at most once.
type UsageBillingCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string

	UserID              int64
	AccountID           int64
	SubscriptionID      *int64
	AccountType         string
	Model               string
	ServiceTier         string
	ReasoningEffort     string
	BillingType         int8
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	ImageCount          int
	MediaType           string

	BalanceCost         float64
	SubscriptionCost    float64
	APIKeyQuotaCost     float64
	APIKeyRateLimitCost float64
	AccountQuotaCost    float64
}

func (c *UsageBillingCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		c.RequestFingerprint = buildUsageBillingFingerprint(c)
	}
}

func buildUsageBillingFingerprint(c *UsageBillingCommand) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf(
		"%d|%d|%d|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%s|%d|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f",
		c.UserID,
		c.AccountID,
		c.APIKeyID,
		strings.TrimSpace(c.AccountType),
		strings.TrimSpace(c.Model),
		strings.TrimSpace(c.ServiceTier),
		strings.TrimSpace(c.ReasoningEffort),
		c.BillingType,
		c.InputTokens,
		c.OutputTokens,
		c.CacheCreationTokens,
		c.CacheReadTokens,
		c.ImageCount,
		strings.TrimSpace(c.MediaType),
		valueOrZero(c.SubscriptionID),
		c.BalanceCost,
		c.SubscriptionCost,
		c.APIKeyQuotaCost,
		c.APIKeyRateLimitCost,
		c.AccountQuotaCost,
	)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func HashUsageRequestPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func valueOrZero(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// AccountQuotaState holds the post-increment quota state returned by the DB transaction.
// All values are post-update (i.e., already include the increment).
type AccountQuotaState struct {
	TotalUsed   float64
	TotalLimit  float64
	DailyUsed   float64
	DailyLimit  float64
	WeeklyUsed  float64
	WeeklyLimit float64
}

type UsageBillingApplyResult struct {
	Applied              bool
	APIKeyQuotaExhausted bool
	NewBalance           *float64           // post-deduction balance (nil = no balance deduction)
	BalanceOverdrafted   bool               // true when the sufficient-balance guard missed and debt was still recorded
	QuotaState           *AccountQuotaState // post-increment quota state (nil = no quota increment)
}

// BatchImageBalanceHoldCommand describes an idempotent balance hold operation.
type BatchImageBalanceHoldCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string
	UserID             int64
	BatchID            string
	HoldAmount         float64
	ActualAmount       float64
}

func (c *BatchImageBalanceHoldCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	c.BatchID = strings.TrimSpace(c.BatchID)
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		c.RequestFingerprint = buildBatchImageBalanceHoldFingerprint(c)
	}
}

func buildBatchImageBalanceHoldFingerprint(c *BatchImageBalanceHoldCommand) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf(
		"%d|%d|%s|%0.10f|%0.10f",
		c.UserID,
		c.APIKeyID,
		strings.TrimSpace(c.BatchID),
		c.HoldAmount,
		c.ActualAmount,
	)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type BatchImageBalanceHoldResult struct {
	Applied       bool
	NewBalance    *float64
	FrozenBalance *float64
}

type UsageBillingRepository interface {
	Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error)
	ReserveBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	CaptureBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	ReleaseBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	BindMediaTaskCharge(ctx context.Context, cmd *MediaTaskChargeCommand) error
	TakeMediaTaskCharge(ctx context.Context, taskKey string) (*MediaTaskChargeTaken, error)
	// MarkUsageLogRefunded 把退款回写到对应的 usage_logs 行：冲平收入列，
	// 并把退款额记进 refunded_credits（原值 = total_cost + refunded_credits）。
	// 按 (request_id, api_key_id) 定位——那是 usage_logs 上的唯一索引。
	MarkUsageLogRefunded(ctx context.Context, requestID string, apiKeyID int64, credits float64) error
	// TakeMediaTaskChargeByTaskID 按上游 task_id 取出可退金额。
	//
	// Webhook 只带 task_id，而 task_key 是 hash(user_id, api_key_id, task_id)，
	// 从 task_id 无法反推，所以要另外一条按 task_id 的取款路径。
	TakeMediaTaskChargeByTaskID(ctx context.Context, taskID string) (*MediaTaskChargeTaken, error)
	// RecordWebhookEventOnce 写入事件幂等记录；返回 false 表示该事件已处理过。
	RecordWebhookEventOnce(ctx context.Context, provider, eventID, eventType, taskID string) (bool, error)
}

// MediaTaskChargeTaken 一笔被取出的扣费记录。需要 UserID 才知道退给谁，
// 需要 RequestID 才找得到要回写的 usage_logs 行（那张表不存上游 task_id）。
type MediaTaskChargeTaken struct {
	UserID    int64
	APIKeyID  int64
	Credits   float64
	RequestID string
}

// MediaTaskChargeCommand 记录异步媒体任务（图片 / 视频）已扣掉的积分，
// 供任务失败时原额退回。
//
// 网关在上游返回 200（任务 pending）时就已全额扣费，任务可能之后才失败，
// 而上游对失败任务不计费。没有这条记录就无从知道该退多少。
type MediaTaskChargeCommand struct {
	TaskKey  string // hash(userID, apiKeyID, taskID)
	TaskID   string // 上游原始 task_id；Webhook 只带它，不存就反查不到
	UserID   int64
	APIKeyID int64
	GroupID  *int64
	Credits  float64
	// RequestID 是回写 usage_logs 的关联键。退款发生在任务查询接口，那时只有
	// 上游 task_id，而 usage_logs 不存 task_id——绑定时不记下来就再也对不上。
	RequestID string
}

func (c *MediaTaskChargeCommand) Normalize() {
	if c == nil {
		return
	}
	c.TaskKey = strings.TrimSpace(c.TaskKey)
	c.TaskID = strings.TrimSpace(c.TaskID)
	c.RequestID = strings.TrimSpace(c.RequestID)
}
