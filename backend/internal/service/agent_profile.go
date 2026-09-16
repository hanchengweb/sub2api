package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

// 代理模式。两者互斥——同一个代理同时吃返利和差价，
// 平台会在同一笔钱上被削两次利润。
const (
	// AgentModeAffiliate 分佣：客户按零售价付钱给平台，代理拿返利。
	// 复用既有 user_affiliates 那套邀请码 + 返利账本。
	AgentModeAffiliate = "affiliate"
	// AgentModeReseller 转售：代理按批发价拿货，进专属分组，自己向客户收钱赚差价。
	AgentModeReseller = "reseller"
)

// 代理档案状态。
const (
	AgentStatusActive     = "active"
	AgentStatusSuspended  = "suspended"
	AgentStatusTerminated = "terminated"
)

var agentModes = map[string]struct{}{
	AgentModeAffiliate: {},
	AgentModeReseller:  {},
}

var agentStatuses = map[string]struct{}{
	AgentStatusActive:     {},
	AgentStatusSuspended:  {},
	AgentStatusTerminated: {},
}

var (
	// ErrAgentProfileInvalid 入参不合法。
	ErrAgentProfileInvalid = errors.New("invalid agent profile")
	// ErrAgentProfileNotFound 该用户不是代理。
	ErrAgentProfileNotFound = errors.New("agent profile not found")
)

// DefaultAgentModeForDirection 按合作方向决定默认代理模式。
//
// 这是 2026-09-16 定的口径：
//   - channel     渠道推广 → 分佣。代理带客户来，客户是平台的客户。
//   - integration 技术集成 → 转售。代理把模型能力接进自己的产品，客户关系归代理。
//   - delivery    客户交付 → 转售。同上。
//
// 管理员在审核时可以覆盖这个默认值，这里只是省掉常规情况下的一次选择。
func DefaultAgentModeForDirection(direction string) string {
	if strings.TrimSpace(direction) == "channel" {
		return AgentModeAffiliate
	}
	return AgentModeReseller
}

// AgentProfile 一份代理档案。
//
// 代理身份不占用 users.role：role 只有 admin/user 两个值，且被
// middleware/admin_only.go 当作管理员判定，往里加值等于在鉴权链路上开口子。
type AgentProfile struct {
	UserID        int64  `json:"user_id"`
	ApplicationID *int64 `json:"application_id,omitempty"`
	Mode          string `json:"mode"`
	Direction     string `json:"direction"`
	Status        string `json:"status"`

	// 转售模式专用。分佣模式下均为 nil。
	PricingPlanID   *int64 `json:"pricing_plan_id,omitempty"`
	ResellerGroupID *int64 `json:"reseller_group_id,omitempty"`

	// Note 平台内部备注，不返回给代理本人（见 handler 的代理侧接口）。
	Note        string    `json:"note,omitempty"`
	ActivatedAt time.Time `json:"activated_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Email 申请人邮箱，只有管理员列表会填。
	//
	// 不填的话后台是一列光秃秃的 user_id，运营认不出谁是谁——
	// 停用一个代理这种操作，靠数字 ID 对人是要出事的。
	Email string `json:"email,omitempty"`
}

// IsActive 只有 active 的代理才享受代理价和代理侧接口。
func (p *AgentProfile) IsActive() bool {
	return p != nil && p.Status == AgentStatusActive
}

// AgentProfileFilters 管理员列表的筛选条件。
type AgentProfileFilters struct {
	Status string
	Mode   string
	Limit  int
	Offset int
}

// AgentProfileRepository 代理档案的存取。
type AgentProfileRepository interface {
	// Upsert 按 user_id 落档。已存在时只更新可变字段，不覆盖 activated_at——
	// 重复审核同一个人不应该把首次生效时间抹掉。
	Upsert(ctx context.Context, p *AgentProfile) error
	GetByUserID(ctx context.Context, userID int64) (*AgentProfile, error)
	List(ctx context.Context, filters AgentProfileFilters) ([]AgentProfile, int, error)
	UpdateStatus(ctx context.Context, userID int64, status, note string) error
	UpdatePricing(ctx context.Context, userID int64, planID, groupID *int64) error
}

// AgentAffiliateBinder 建档时确保代理有邀请码。
//
// 只取 EnsureUserAffiliate 这一个方法而不是整个 *AffiliateService：
// 代理体系用得着的仅仅是「这个人得有一个码」，不该为此把返利服务整个拖进来。
// *AffiliateService 天然满足这个接口。
type AgentAffiliateBinder interface {
	EnsureUserAffiliate(ctx context.Context, userID int64) (*AffiliateSummary, error)
}

// AgentService 代理档案。
type AgentService struct {
	repo      AgentProfileRepository
	affiliate AgentAffiliateBinder
}

func NewAgentService(repo AgentProfileRepository, affiliate AgentAffiliateBinder) *AgentService {
	return &AgentService{repo: repo, affiliate: affiliate}
}

// ActivateAgentInput 审核通过时的建档入参。
type ActivateAgentInput struct {
	UserID        int64
	ApplicationID *int64
	Direction     string
	// Mode 留空时按 Direction 取默认值。
	Mode string
	// PricingPlanID / ResellerGroupID 只在转售模式下有意义。
	PricingPlanID   *int64
	ResellerGroupID *int64
	Note            string
}

// Activate 审核通过后开通代理身份。
//
// 这条路径是幂等的：管理员把同一条申请反复改成 accepted（或者同一个人的
// 第二条申请也通过了）不应该报错，也不应该重置已经生效的代理。
//
// 分佣模式会顺带确保邀请码存在。EnsureUserAffiliate 本身是 upsert，
// 已经有码的人不会被换码——代理的码可能已经印在物料上了，换掉就是事故。
func (s *AgentService) Activate(ctx context.Context, input ActivateAgentInput) (*AgentProfile, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("agent service is unavailable")
	}
	if input.UserID <= 0 {
		return nil, ErrAgentProfileInvalid
	}
	direction := strings.TrimSpace(input.Direction)
	if _, ok := agencyDirections[direction]; !ok {
		return nil, ErrAgentProfileInvalid
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = DefaultAgentModeForDirection(direction)
	}
	if _, ok := agentModes[mode]; !ok {
		return nil, ErrAgentProfileInvalid
	}

	profile := &AgentProfile{
		UserID:        input.UserID,
		ApplicationID: input.ApplicationID,
		Mode:          mode,
		Direction:     direction,
		Status:        AgentStatusActive,
		Note:          truncateRunes(input.Note, 2000),
	}
	// 转售专用字段只在转售模式下带上：分佣代理挂着一个分组 ID 会让
	// 后面读代码的人以为他也有批发价。
	if mode == AgentModeReseller {
		profile.PricingPlanID = input.PricingPlanID
		profile.ResellerGroupID = input.ResellerGroupID
	}

	if err := s.repo.Upsert(ctx, profile); err != nil {
		return nil, err
	}

	// 邀请码：分佣代理靠它绑定客户。建档失败要报错，发码失败不该拖垮建档——
	// 代理身份已经写进库了，码可以由管理员在后台补发。
	if mode == AgentModeAffiliate && s.affiliate != nil {
		if _, err := s.affiliate.EnsureUserAffiliate(ctx, input.UserID); err != nil {
			return profile, nil
		}
	}
	return profile, nil
}

// Get 读一份代理档案。不是代理时返回 ErrAgentProfileNotFound。
func (s *AgentService) Get(ctx context.Context, userID int64) (*AgentProfile, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("agent service is unavailable")
	}
	if userID <= 0 {
		return nil, ErrAgentProfileInvalid
	}
	return s.repo.GetByUserID(ctx, userID)
}

// IsActiveAgent 代理侧接口的准入判定。
//
// 查不到档案、或者档案被停用，都算不是代理——调用方据此回 403，
// 不要把「不是代理」和「服务异常」混成同一个响应。
func (s *AgentService) IsActiveAgent(ctx context.Context, userID int64) (bool, *AgentProfile, error) {
	profile, err := s.Get(ctx, userID)
	if errors.Is(err, ErrAgentProfileNotFound) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return profile.IsActive(), profile, nil
}

// List 管理员分页查看代理。
func (s *AgentService) List(ctx context.Context, filters AgentProfileFilters) ([]AgentProfile, int, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("agent service is unavailable")
	}
	if filters.Status != "" {
		if _, ok := agentStatuses[filters.Status]; !ok {
			return nil, 0, ErrAgentProfileInvalid
		}
	}
	if filters.Mode != "" {
		if _, ok := agentModes[filters.Mode]; !ok {
			return nil, 0, ErrAgentProfileInvalid
		}
	}
	if filters.Limit <= 0 || filters.Limit > 100 {
		filters.Limit = 20
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}
	return s.repo.List(ctx, filters)
}

// UpdateStatus 停用 / 恢复 / 终止一个代理。
//
// 停用只挡代理侧接口和后续结算，不动他已经拿到的分组和余额——
// 那些要单独处理，混在一个开关里会让「先停掉再慢慢清算」这个常规动作变得不可能。
func (s *AgentService) UpdateStatus(ctx context.Context, userID int64, status, note string) error {
	if s == nil || s.repo == nil {
		return errors.New("agent service is unavailable")
	}
	status = strings.TrimSpace(status)
	if userID <= 0 {
		return ErrAgentProfileInvalid
	}
	if _, ok := agentStatuses[status]; !ok {
		return ErrAgentProfileInvalid
	}
	return s.repo.UpdateStatus(ctx, userID, status, truncateRunes(note, 2000))
}

// UpdatePricing 改代理的定价方案与专属分组。
func (s *AgentService) UpdatePricing(ctx context.Context, userID int64, planID, groupID *int64) error {
	if s == nil || s.repo == nil {
		return errors.New("agent service is unavailable")
	}
	if userID <= 0 {
		return ErrAgentProfileInvalid
	}
	profile, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	// 分佣代理没有批发价，给他配定价方案是配置错误，不是无害的多余动作：
	// 他的客户按零售价付钱，这份方案永远不会生效，却会让后台显示他有批发价。
	if profile.Mode != AgentModeReseller {
		return ErrAgentProfileInvalid
	}
	return s.repo.UpdatePricing(ctx, userID, planID, groupID)
}
