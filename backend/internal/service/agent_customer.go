package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

// AgentCustomer 代理名下的一个客户。
//
// 邮箱脱敏后返回：代理确实需要认出是谁，但平台侧一旦把完整邮箱交出去就收不回来了。
// 先紧后松——真需要完整邮箱再单独开，反过来做不到。
type AgentCustomer struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Status string `json:"status"`
	// BoundAt 绑定到该代理名下的时间（= 客户注册时间，归属一次性锁定）。
	BoundAt time.Time `json:"bound_at"`

	// 消费汇总。代理判断一个客户值不值得跟进，看的就是这两个数。
	TotalCostCredits float64    `json:"total_cost_credits"`
	RequestCount     int64      `json:"request_count"`
	LastActiveAt     *time.Time `json:"last_active_at,omitempty"`
}

// AgentCustomerUsage 客户的一条用量记录（代理视角的精简版）。
//
// 刻意不含 prompt、token 明细、账号信息：代理需要知道客户花了多少钱在什么模型上，
// 不需要、也不该看到客户具体问了什么。这条边界一旦开口就难收。
type AgentCustomerUsage struct {
	ID            int64     `json:"id"`
	CustomerID    int64     `json:"customer_id"`
	CustomerEmail string    `json:"customer_email"`
	Model         string    `json:"model"`
	BillingMode   string    `json:"billing_mode"`
	ActualCost    float64   `json:"actual_cost"`
	ImageCount    int       `json:"image_count"`
	CreatedAt     time.Time `json:"created_at"`
}

// AgentCustomerUsageFilters 用量查询的筛选条件。
type AgentCustomerUsageFilters struct {
	// CustomerID 只看某个客户；0 表示全部。
	//
	// 注意：这个值来自请求参数，但作用域根始终是代理自己的 user_id——
	// 仓储层会同时要求 inviter_id 匹配，所以填别人的客户 id 查不到东西。
	CustomerID int64
	Model      string
	Limit      int
	Offset     int
}

// AgentCustomerRepository 代理名下客户与用量的读取。
//
// 所有方法都以 agentUserID 为作用域根，实现里必须 JOIN user_affiliates
// 并强制 inviter_id = agentUserID。漏一处，代理就能看到别人的客户。
type AgentCustomerRepository interface {
	ListCustomers(ctx context.Context, agentUserID int64, limit, offset int) ([]AgentCustomer, int, error)
	ListUsage(ctx context.Context, agentUserID int64, filters AgentCustomerUsageFilters) ([]AgentCustomerUsage, int, error)
}

// AgentCustomerService 代理的客户管理。
type AgentCustomerService struct {
	repo   AgentCustomerRepository
	agents *AgentService
}

func NewAgentCustomerService(repo AgentCustomerRepository, agents *AgentService) *AgentCustomerService {
	return &AgentCustomerService{repo: repo, agents: agents}
}

// ListCustomers 代理查自己的客户。
func (s *AgentCustomerService) ListCustomers(ctx context.Context, agentUserID int64, limit, offset int) ([]AgentCustomer, int, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("agent customer service is unavailable")
	}
	if agentUserID <= 0 {
		return nil, 0, ErrAgentProfileInvalid
	}
	limit, offset = normalizeAgentPaging(limit, offset)

	customers, total, err := s.repo.ListCustomers(ctx, agentUserID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	for i := range customers {
		customers[i].Email = maskEmail(customers[i].Email)
	}
	return customers, total, nil
}

// ListUsage 代理查自己客户的用量。
func (s *AgentCustomerService) ListUsage(ctx context.Context, agentUserID int64, filters AgentCustomerUsageFilters) ([]AgentCustomerUsage, int, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("agent customer service is unavailable")
	}
	if agentUserID <= 0 {
		return nil, 0, ErrAgentProfileInvalid
	}
	filters.Limit, filters.Offset = normalizeAgentPaging(filters.Limit, filters.Offset)
	filters.Model = strings.TrimSpace(filters.Model)
	if len([]rune(filters.Model)) > 100 {
		return nil, 0, ErrAgentProfileInvalid
	}

	usage, total, err := s.repo.ListUsage(ctx, agentUserID, filters)
	if err != nil {
		return nil, 0, err
	}
	for i := range usage {
		usage[i].CustomerEmail = maskEmail(usage[i].CustomerEmail)
	}
	return usage, total, nil
}

func normalizeAgentPaging(limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
