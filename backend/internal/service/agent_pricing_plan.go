package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 定价方案状态。
const (
	AgentPricingPlanActive   = "active"
	AgentPricingPlanArchived = "archived"
)

// 模型类别。按 channel_model_pricing.billing_mode 判定，不额外维护一张模型分类表：
// 那张表一定会和实际接入的模型脱节，而 billing_mode 是计费时真正生效的字段。
const (
	// AgentCategoryText 文本类：按 token 计费。代理价 = 零售价 × 折扣。
	AgentCategoryText = "text"
	// AgentCategoryMultimodal 多模态：按次 / 图片 / 视频计费。代理价 = 零售价 - 固定让利。
	AgentCategoryMultimodal = "multimodal"
)

var (
	// ErrAgentPricingPlanInvalid 方案参数不合法。
	ErrAgentPricingPlanInvalid = errors.New("invalid agent pricing plan")
	// ErrAgentPricingPlanNotFound 方案不存在。
	ErrAgentPricingPlanNotFound = errors.New("agent pricing plan not found")
)

// AgentPricingPlan 代理定价方案：生成批发价时套用的规则。
type AgentPricingPlan struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// TextDiscount 文本类折扣，代理价 = 零售价 × 本值。0.8 = 八折。
	TextDiscount float64 `json:"text_discount"`
	// MultimodalDeductionCNY 多模态每次让利，单位人民币元。0.1 = 每次让一毛。
	//
	// 存人民币而不是积分：运营填的是「一毛」这个业务口径，
	// ￥1 ↔ 积分的换算率是可配置的（BALANCE_RECHARGE_MULTIPLIER），
	// 换算率日后调整时方案不用跟着改，重新生成一次即可。
	MultimodalDeductionCNY float64 `json:"multimodal_deduction_cny"`
	// EnforceCostFloor 开启后，算出来低于成本价的条目不生成。
	EnforceCostFloor bool      `json:"enforce_cost_floor"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Validate 校验方案参数。
//
// 折扣必须落在 (0,1]：大于 1 是给代理加价，几乎一定是把「加价 20%」
// 和「打八折」搞混了；等于 0 是白送。这两种都不该静默存进库。
func (p *AgentPricingPlan) Validate() error {
	if p == nil {
		return ErrAgentPricingPlanInvalid
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len([]rune(p.Name)) > 100 {
		return ErrAgentPricingPlanInvalid
	}
	if p.TextDiscount <= 0 || p.TextDiscount > 1 {
		return ErrAgentPricingPlanInvalid
	}
	if p.MultimodalDeductionCNY < 0 {
		return ErrAgentPricingPlanInvalid
	}
	if p.Status == "" {
		p.Status = AgentPricingPlanActive
	}
	if p.Status != AgentPricingPlanActive && p.Status != AgentPricingPlanArchived {
		return ErrAgentPricingPlanInvalid
	}
	return nil
}

// AgentPricingPlanRepository 定价方案的存取。
type AgentPricingPlanRepository interface {
	Create(ctx context.Context, p *AgentPricingPlan) error
	Update(ctx context.Context, p *AgentPricingPlan) error
	GetByID(ctx context.Context, id int64) (*AgentPricingPlan, error)
	List(ctx context.Context) ([]AgentPricingPlan, error)
}

// CategoryForBillingMode 按计费模式判定模型类别。
//
// token 之外的三种（per_request / image / video）都按多模态处理：
// 它们的共同点是「一次调用一个价」，固定让利一毛才有意义。
// token 模式是「每 token 一个价」，减一毛会把单价打成负数，只能按比例折。
func CategoryForBillingMode(mode BillingMode) string {
	if mode == "" || mode == BillingModeToken {
		return AgentCategoryText
	}
	return AgentCategoryMultimodal
}

// AgentCostLookup 查一个模型某个价格字段的成本（积分）。查不到返回 false。
//
// 带 field 维度而不是只给一个「成本单价」：输入价和输出价的成本差一个数量级，
// 拿输出成本去卡输入价的下限，会把本来健康的定价全判成亏本。
//
// 成本与售价同为积分量纲，可以直接比大小（见 frontend/src/utils/format.ts
// 的 grossMarginPercent：「收入与成本同为积分时直接相除，不涉及汇率」）。
type AgentCostLookup func(platform, model, field string) (float64, bool)

// BuildAgentCostLookup 把渠道的账号成本定价规则编成一个查询函数。
//
// 规则按 SortOrder 先命中为准，与 account_stats_pricing.go 的语义一致；
// 这里不重新实现匹配逻辑的分组/账号维度——生成代理价时还不知道将来会走哪个
// 账号，只能按模型取第一条配了价的规则。取到的值仅用于下限告警，不参与计费。
func BuildAgentCostLookup(rules []AccountStatsPricingRule) AgentCostLookup {
	if len(rules) == 0 {
		return nil
	}
	return func(platform, model, field string) (float64, bool) {
		for i := range rules {
			for j := range rules[i].Pricing {
				row := &rules[i].Pricing[j]
				if row.Platform != "" && platform != "" && row.Platform != platform {
					continue
				}
				if !containsModel(row.Models, model) {
					continue
				}
				if v := agentCostFieldValue(row, field); v != nil {
					return *v, true
				}
			}
		}
		return 0, false
	}
}

func containsModel(models []string, model string) bool {
	for _, m := range models {
		if m == model {
			return true
		}
	}
	return false
}

// agentCostFieldValue 取成本行上与代理价同名的那个字段。
func agentCostFieldValue(row *ChannelModelPricing, field string) *float64 {
	switch field {
	case "input":
		return row.InputPrice
	case "output":
		return row.OutputPrice
	case "cache_write":
		return row.CacheWritePrice
	case "cache_read":
		return row.CacheReadPrice
	case "image_input":
		return row.ImageInputPrice
	case "image_output":
		return row.ImageOutputPrice
	case "per_request":
		return row.PerRequestPrice
	default:
		return nil
	}
}

// AgentPriceChange 生成器算出的一行价格变更，用于预览。
//
// 预览是这个模块的核心：直接把算出来的价格写进库，配错了要等到用户
// 调用并被扣了错的钱才会发现。先看 diff 再落库。
type AgentPriceChange struct {
	Platform    string   `json:"platform"`
	Models      []string `json:"models"`
	BillingMode string   `json:"billing_mode"`
	Category    string   `json:"category"`
	// Field 价格字段名（input / output / per_request / ...），
	// 一行定价有多个价格字段时会展开成多条。
	Field string `json:"field"`
	// TierLabel 非空表示这条来自分层定价（如 1K·高、4K·中），空表示行级默认价。
	//
	// 必须展开到分层：生产上 10 条定价配了 54 个层级，实际计费走的是层级价。
	// 只折行级默认价的话，代理照零售层级价被扣费，一分钱折扣都拿不到；
	// nano_banana_2 更极端——它行级价是 NULL，只有层级价。
	TierLabel   string  `json:"tier_label,omitempty"`
	RetailPrice float64 `json:"retail_price"`
	AgentPrice  float64 `json:"agent_price"`
	// CostPrice 成本单价；查不到时为 nil，此时下限保护没有依据。
	CostPrice *float64 `json:"cost_price,omitempty"`
	// Warning 非空表示这条需要人看一眼。空字符串表示算得干净。
	Warning string `json:"warning,omitempty"`
	// Skipped 为真时这条保留零售价写入目标渠道，不套用方案。
	//
	// 保留零售价而不是不写：不写的话该模型在代理渠道里没有定价，
	// 会回退到 LiteLLM 官方价——那个价和我们的零售价无关，可能更低，
	// 等于跳过保护反而捅出一个更大的窟窿。
	Skipped bool `json:"skipped"`
}

// AgentPricingPreview 一次生成的完整结果。
type AgentPricingPreview struct {
	PlanID   int64  `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// CreditsPerCNY 本次换算用的汇率（每元多少积分）。
	//
	// 必须回报给调用方：多模态让利是按人民币配的，换算率取错的话
	// 「让一毛」会变成「让一分」或者「让一块」，而算出来的数字本身看不出问题。
	CreditsPerCNY float64 `json:"credits_per_cny"`
	// DeductionCredits 换算后的每次让利积分数 = MultimodalDeductionCNY × CreditsPerCNY。
	DeductionCredits float64            `json:"deduction_credits"`
	Changes          []AgentPriceChange `json:"changes"`
	// 汇总，让运营不用自己数。
	TotalRows   int `json:"total_rows"`
	SkippedRows int `json:"skipped_rows"`
	WarningRows int `json:"warning_rows"`

	// Rows 算好的代理定价，可直接交给 ChannelService.Update 落库。
	//
	// 不序列化：它和 Changes 是同一份结果的两种形态，返回给前端只会让
	// 响应体翻倍。留在结构体里是为了让「预览」和「应用」共用同一次计算——
	// 两边各算一次的话，中间零售价一改，运营看到的就不是实际写进去的。
	Rows []ChannelModelPricing `json:"-"`
}

// BuildAgentPricingPreview 按方案算出面向代理的批发价。
//
// creditsPerCNY 由调用方从 BALANCE_RECHARGE_MULTIPLIER 取并显式传入，
// 不在这里读配置：这个值直接决定「一毛」折算成多少积分，
// 让它作为一个看得见的入参，比藏在服务内部安全。
//
// costLookup 可以为 nil（成本表没配全时）。此时下限保护无从执行，
// 受影响的条目会带上告警而不是被静默放行。
func BuildAgentPricingPreview(
	plan *AgentPricingPlan,
	retail []ChannelModelPricing,
	creditsPerCNY float64,
	costLookup AgentCostLookup,
) (*AgentPricingPreview, error) {
	if plan == nil {
		return nil, ErrAgentPricingPlanInvalid
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if creditsPerCNY <= 0 {
		return nil, fmt.Errorf("invalid credits per CNY rate: %v", creditsPerCNY)
	}

	deduction := plan.MultimodalDeductionCNY * creditsPerCNY
	preview := &AgentPricingPreview{
		PlanID:           plan.ID,
		PlanName:         plan.Name,
		CreditsPerCNY:    creditsPerCNY,
		DeductionCredits: deduction,
		Changes:          make([]AgentPriceChange, 0, len(retail)*4),
	}

	preview.Rows = make([]ChannelModelPricing, 0, len(retail))
	for i := range retail {
		// 深拷贝一份作为落库用的代理定价：直接改 retail 会污染调用方
		// 手里的渠道对象，那是从缓存里拿出来的零售价。
		out := cloneChannelModelPricing(&retail[i])
		category := CategoryForBillingMode(out.BillingMode)

		for _, field := range agentPricedFields(&out, category) {
			if field.value == nil {
				continue
			}
			change := buildAgentPriceChange(&out, category, field, plan, deduction, costLookup)
			preview.Changes = append(preview.Changes, change)
			// 跳过的条目保留零售价（change.AgentPrice 已被置为零售价），
			// 照写不误——留空会让该模型回退到 LiteLLM 官方价。
			*field.value = change.AgentPrice
		}
		preview.Rows = append(preview.Rows, out)
	}

	preview.TotalRows = len(preview.Changes)
	for _, c := range preview.Changes {
		if c.Skipped {
			preview.SkippedRows++
		}
		if c.Warning != "" {
			preview.WarningRows++
		}
	}
	return preview, nil
}

// agentPricedField 一个待折算的价格字段。
//
// value 指向副本里的那个 float64，算完直接写回去——落库用的行和预览看的 diff
// 就是同一次计算的产物，不会各算一遍再对不上。
type agentPricedField struct {
	name  string
	value *float64
	// model 该字段对应的代表模型，用于查成本。
	model string
	// tier 非空表示来自分层定价；空表示行级默认价。
	tier string
}

// cloneChannelModelPricing 深拷贝一行定价，价格指针全部换成新分配的。
//
// 浅拷贝不行：ChannelModelPricing 的价格字段都是 *float64，直接复制结构体
// 会让副本和原件指向同一个 float64，往副本写代理价等于改掉了缓存里的零售价。
func cloneChannelModelPricing(src *ChannelModelPricing) ChannelModelPricing {
	out := *src
	out.InputPrice = clonePriceValue(src.InputPrice)
	out.OutputPrice = clonePriceValue(src.OutputPrice)
	out.CacheWritePrice = clonePriceValue(src.CacheWritePrice)
	out.CacheReadPrice = clonePriceValue(src.CacheReadPrice)
	out.ImageInputPrice = clonePriceValue(src.ImageInputPrice)
	out.ImageOutputPrice = clonePriceValue(src.ImageOutputPrice)
	out.PerRequestPrice = clonePriceValue(src.PerRequestPrice)

	if src.Models != nil {
		out.Models = append([]string(nil), src.Models...)
	}
	if src.Intervals != nil {
		out.Intervals = make([]PricingInterval, len(src.Intervals))
		for i := range src.Intervals {
			iv := src.Intervals[i]
			iv.InputPrice = clonePriceValue(src.Intervals[i].InputPrice)
			iv.OutputPrice = clonePriceValue(src.Intervals[i].OutputPrice)
			iv.CacheWritePrice = clonePriceValue(src.Intervals[i].CacheWritePrice)
			iv.CacheReadPrice = clonePriceValue(src.Intervals[i].CacheReadPrice)
			iv.PerRequestPrice = clonePriceValue(src.Intervals[i].PerRequestPrice)
			out.Intervals[i] = iv
		}
	}
	return out
}

func clonePriceValue(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// agentPricedFields 列出一行定价里需要折算的价格字段，含分层定价。
//
// 文本类按比例折，所有 token 相关的价格字段都要跟着动——只折输入价不折输出价，
// 代理在输出密集的场景下拿不到说好的八折。
// 多模态只动「每次」的价格：那是固定让利唯一说得通的地方。
//
// 分层必须一起折：生产上 10 条定价配了 54 个层级（1K/2K/4K × 低/中/高），
// 实际计费命中的是层级价，只折行级默认价的话代理照零售价被扣。
func agentPricedFields(row *ChannelModelPricing, category string) []agentPricedField {
	model := ""
	if len(row.Models) > 0 {
		model = row.Models[0]
	}

	fields := make([]agentPricedField, 0, 6+len(row.Intervals)*2)
	if category == AgentCategoryText {
		fields = append(fields,
			agentPricedField{name: "input", value: row.InputPrice, model: model},
			agentPricedField{name: "output", value: row.OutputPrice, model: model},
			agentPricedField{name: "cache_write", value: row.CacheWritePrice, model: model},
			agentPricedField{name: "cache_read", value: row.CacheReadPrice, model: model},
			agentPricedField{name: "image_input", value: row.ImageInputPrice, model: model},
			agentPricedField{name: "image_output", value: row.ImageOutputPrice, model: model},
		)
	} else {
		fields = append(fields,
			agentPricedField{name: "per_request", value: row.PerRequestPrice, model: model},
			agentPricedField{name: "image_output", value: row.ImageOutputPrice, model: model},
		)
	}

	for i := range row.Intervals {
		iv := &row.Intervals[i]
		tier := iv.TierLabel
		if tier == "" {
			// token 区间没有标签，用区间边界当标识，否则预览里几条会长得一模一样。
			tier = formatTokenRange(iv.MinTokens, iv.MaxTokens)
		}
		if category == AgentCategoryText {
			fields = append(fields,
				agentPricedField{name: "input", value: iv.InputPrice, model: model, tier: tier},
				agentPricedField{name: "output", value: iv.OutputPrice, model: model, tier: tier},
				agentPricedField{name: "cache_write", value: iv.CacheWritePrice, model: model, tier: tier},
				agentPricedField{name: "cache_read", value: iv.CacheReadPrice, model: model, tier: tier},
			)
			continue
		}
		fields = append(fields,
			agentPricedField{name: "per_request", value: iv.PerRequestPrice, model: model, tier: tier},
		)
	}
	return fields
}

func formatTokenRange(minTokens int, maxTokens *int) string {
	if maxTokens == nil {
		return fmt.Sprintf("%d+", minTokens)
	}
	return fmt.Sprintf("%d-%d", minTokens, *maxTokens)
}

// buildAgentPriceChange 算一个字段的代理价并判定告警。
func buildAgentPriceChange(
	row *ChannelModelPricing,
	category string,
	field agentPricedField,
	plan *AgentPricingPlan,
	deduction float64,
	costLookup AgentCostLookup,
) AgentPriceChange {
	retailPrice := *field.value
	change := AgentPriceChange{
		Platform:    row.Platform,
		Models:      row.Models,
		BillingMode: string(row.BillingMode),
		Category:    category,
		Field:       field.name,
		TierLabel:   field.tier,
		RetailPrice: retailPrice,
	}

	if category == AgentCategoryText {
		change.AgentPrice = retailPrice * plan.TextDiscount
	} else {
		change.AgentPrice = retailPrice - deduction
		// 让利大于零售价本身：按方案算出来是负价，等于平台倒贴还要送钱。
		// 退回零售价并标记跳过，由运营决定这个模型是单独定价还是不给代理。
		if change.AgentPrice <= 0 {
			change.AgentPrice = retailPrice
			change.Skipped = true
			change.Warning = fmt.Sprintf(
				"零售价 %.6f 积分不高于每次让利 %.6f 积分，按方案会算出非正价，已退回零售价",
				retailPrice, deduction)
			return change
		}
	}

	if costLookup == nil {
		if plan.EnforceCostFloor {
			change.Warning = "成本未知，无法执行成本下限保护"
		}
		return change
	}
	cost, ok := costLookup(row.Platform, field.model, field.name)
	if !ok {
		if plan.EnforceCostFloor {
			change.Warning = "成本表缺该模型，无法执行成本下限保护"
		}
		return change
	}
	change.CostPrice = &cost
	if change.AgentPrice < cost {
		change.Warning = fmt.Sprintf(
			"代理价 %.6f 低于成本 %.6f 积分，已退回零售价", change.AgentPrice, cost)
		// 只有开了保护才真的拦下来。关掉保护时仍然告警：
		// 明知故犯和不知情是两回事，但都得让人看见。
		if plan.EnforceCostFloor {
			change.Skipped = true
			change.AgentPrice = retailPrice
		}
	}
	return change
}

// AgentPricingPlanService 定价方案的增删改查。
type AgentPricingPlanService struct {
	repo AgentPricingPlanRepository
}

func NewAgentPricingPlanService(repo AgentPricingPlanRepository) *AgentPricingPlanService {
	return &AgentPricingPlanService{repo: repo}
}

func (s *AgentPricingPlanService) Create(ctx context.Context, p *AgentPricingPlan) error {
	if s == nil || s.repo == nil {
		return errors.New("agent pricing plan service is unavailable")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	return s.repo.Create(ctx, p)
}

func (s *AgentPricingPlanService) Update(ctx context.Context, p *AgentPricingPlan) error {
	if s == nil || s.repo == nil {
		return errors.New("agent pricing plan service is unavailable")
	}
	if p == nil || p.ID <= 0 {
		return ErrAgentPricingPlanInvalid
	}
	if err := p.Validate(); err != nil {
		return err
	}
	return s.repo.Update(ctx, p)
}

func (s *AgentPricingPlanService) Get(ctx context.Context, id int64) (*AgentPricingPlan, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("agent pricing plan service is unavailable")
	}
	if id <= 0 {
		return nil, ErrAgentPricingPlanInvalid
	}
	return s.repo.GetByID(ctx, id)
}

func (s *AgentPricingPlanService) List(ctx context.Context) ([]AgentPricingPlan, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("agent pricing plan service is unavailable")
	}
	return s.repo.List(ctx)
}
