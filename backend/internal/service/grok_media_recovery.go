package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrGrokVideoBindingNotFound = errors.New("video request not found")

// A provider's explicit model outage must not quarantine its other models.
// Generic transport/credential errors retain the existing account policy.
func (s *OpenAIGatewayService) cooldownGrokMediaModel(ctx context.Context, account *Account, requestedModel string, status int, body []byte) bool {
	if s == nil || account == nil || account.Type != AccountTypeAPIKey || status != http.StatusServiceUnavailable {
		return false
	}
	model := canonicalOpenAIAccountSchedulingModel(account, requestedModel)
	message := strings.ToLower(extractUpstreamErrorMessage(body))
	if model == "" || !regexp.MustCompile(`(^|[^a-z0-9._:/-])`+regexp.QuoteMeta(strings.ToLower(model))+`($|[^a-z0-9._:/-])`).MatchString(message) {
		return false
	}
	modelOutage := strings.Contains(message, "渠道") && (strings.Contains(message, "熔断") || strings.Contains(message, "不可用")) ||
		strings.Contains(message, "model") && (strings.Contains(message, "circuit breaker") || strings.Contains(message, "all channels unavailable"))
	if !modelOutage {
		return false
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if s.accountRepo != nil {
		if err := s.accountRepo.SetModelRateLimit(stateCtx, account.ID, model, time.Now().Add(2*time.Minute), "media model temporarily unavailable"); err != nil {
			slog.Warn("grok_media.model_cooldown_failed", "account_id", account.ID, "model", model, "error", err)
		}
	}
	return true
}

// Existing jobs stay on their owner-bound account. Only the generic upstream
// 5xx cooldown is bypassed; revoked credentials, manual suspension, rate limits,
// expiry, group membership and concurrency controls still apply.
func (s *OpenAIGatewayService) SelectGrokMediaVideoLookupAccount(ctx context.Context, groupID *int64, requestID string, userID, apiKeyID int64) (*AccountSelectionResult, error) {
	accountID, err := s.ResolveGrokMediaVideoRequestAccount(ctx, groupID, requestID, userID, apiKeyID)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if accountID <= 0 {
		return nil, ErrGrokVideoBindingNotFound
	}
	if s.accountRepo == nil {
		return nil, ErrNoAvailableAccounts
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil || account.Platform != PlatformGrok || !s.openAIAccountMatchesSchedulingGroup(account, groupID) {
		return nil, ErrGrokVideoBindingNotFound
	}
	lookup := *account
	transient := account.Type == AccountTypeAPIKey && account.TempUnschedulableReason == "grok upstream temporary error" && account.TempUnschedulableUntil != nil
	if transient {
		lookup.TempUnschedulableUntil = nil
	}
	if !lookup.IsSchedulable() || !parentHealthyForShadow(account, s.parentAccountLookup(ctx)) {
		return nil, ErrNoAvailableAccounts
	}
	mu := s.openAIAccountRuntimeBlockLock(account.ID)
	mu.Lock()
	block, _ := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	blockedUntil, _ := block.(time.Time)
	_, readsAllowed := s.openaiAccountRuntimeVideoReadAllowed.Load(account.ID)
	blocked := time.Now().Before(blockedUntil) && !(transient && readsAllowed)
	mu.Unlock()
	if blocked {
		return nil, ErrNoAvailableAccounts
	}
	slot, err := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	if slot != nil && slot.Acquired {
		return s.newAcquiredSelectionResult(ctx, account, slot.ReleaseFunc)
	}
	cfg := s.schedulingConfig()
	return s.newSelectionResult(ctx, account, false, nil, &AccountWaitPlan{
		AccountID: account.ID, MaxConcurrency: account.Concurrency,
		Timeout: cfg.FallbackWaitTimeout, MaxWaiting: cfg.FallbackMaxWaiting,
	})
}
