package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// 与前端 AgencyView 的 directions 一一对应。
var agencyDirections = map[string]struct{}{
	"channel":     {},
	"integration": {},
	"delivery":    {},
}

var agencyStatuses = map[string]struct{}{
	"pending":   {},
	"contacted": {},
	"accepted":  {},
	"rejected":  {},
}

// AgencyMaxPendingPerUser 单个用户最多能有几条待处理申请。
//
// 表单挂在登录态后面，所以刷不出量；这个上限挡的是「同一个人反复点提交」——
// 没有它，运营那边会看到十几条一模一样的申请，真实申请反而被埋掉。
//
// 真正挡重复提交的是「同方向不得有第二条待处理」（见 Submit）；
// 这个总数上限是兜底：三个方向各提一条之后就不能再提了。
const AgencyMaxPendingPerUser = 3

const (
	agencyMaxNameLen     = 80
	agencyMaxEmailLen    = 254
	agencyMaxCompanyLen  = 120
	agencyMaxScenarioLen = 1000
)

var (
	// ErrAgencyApplicationInvalid 提交内容不合法。
	ErrAgencyApplicationInvalid = errors.New("invalid agency application")
	// ErrAgencyTooManyPending 待处理申请已达上限。
	ErrAgencyTooManyPending = errors.New("too many pending agency applications")
)

// agencyEmailPattern 只做形状校验，不追求 RFC 完备。
// 目的是挡住明显填错的（漏了 @、没有域名），真正的可达性由人工联系时验证。
var agencyEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// AgencyApplication 一条合作申请。
type AgencyApplication struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Direction   string    `json:"direction"`
	ContactName string    `json:"contact_name"`
	Email       string    `json:"email"`
	Company     string    `json:"company"`
	Scenario    string    `json:"scenario"`
	Status      string    `json:"status"`
	AdminNote   string    `json:"admin_note,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AgencyApplicationFilters 管理员列表的筛选条件。
type AgencyApplicationFilters struct {
	Status string
	Limit  int
	Offset int
}

// AgencyApplicationRepository 合作申请的存取。
type AgencyApplicationRepository interface {
	Create(ctx context.Context, app *AgencyApplication) error
	ListByUser(ctx context.Context, userID int64, limit int) ([]AgencyApplication, error)
	// CountPending 同时返回该用户待处理总数、以及其中落在 direction 上的条数。
	// 一次查询拿两个数：提交路径上不值得为此多跑一趟库。
	CountPending(ctx context.Context, userID int64, direction string) (total int, sameDirection int, err error)
	// GetByID 单条读取。审核通过要据此拿到申请人和合作方向去建代理档案。
	GetByID(ctx context.Context, id int64) (*AgencyApplication, error)
	List(ctx context.Context, filters AgencyApplicationFilters) ([]AgencyApplication, int, error)
	UpdateStatus(ctx context.Context, id int64, status, adminNote string) error
}

// AgencyApplicationService 合作申请。
type AgencyApplicationService struct {
	repo AgencyApplicationRepository
	// agents 非空时，审核通过会顺带开通代理身份。
	// 留空则退化成纯状态机（老行为），方便测试和渐进接线。
	agents *AgentService
}

func NewAgencyApplicationService(repo AgencyApplicationRepository) *AgencyApplicationService {
	return &AgencyApplicationService{repo: repo}
}

// NewAgencyApplicationServiceWithAgents 带代理开通能力的构造。
//
// 分成两个构造函数而不是给老的加参数：现有调用点和测试不用跟着改，
// 接线错了也只是退回「改状态但不开通」，不会在审核路径上 panic。
func NewAgencyApplicationServiceWithAgents(repo AgencyApplicationRepository, agents *AgentService) *AgencyApplicationService {
	return &AgencyApplicationService{repo: repo, agents: agents}
}

// Submit 提交一条合作申请。
//
// 提交前先数待处理条数：同一个人反复点提交会把运营那边的列表刷满，
// 真实申请反而被埋掉。达上限就明确报错，由前端提示「已有申请在处理中」。
func (s *AgencyApplicationService) Submit(ctx context.Context, userID int64, app *AgencyApplication) error {
	if s == nil || s.repo == nil {
		return errors.New("agency application service is unavailable")
	}
	if err := normalizeAgencyApplication(userID, app); err != nil {
		return err
	}
	pending, sameDirection, err := s.repo.CountPending(ctx, userID, app.Direction)
	if err != nil {
		return err
	}
	// 同一个方向已经有在处理的申请 = 重复提交，直接挡掉。
	// 不同方向另算：一个人既想做渠道又想做交付是正常诉求，不该被算成刷单。
	if sameDirection > 0 || pending >= AgencyMaxPendingPerUser {
		return ErrAgencyTooManyPending
	}
	app.UserID = userID
	app.Status = "pending"
	return s.repo.Create(ctx, app)
}

// ListMine 用户查看自己提交过的申请。
//
// 提交完什么反馈都没有的话，用户只会反复再提交一遍——所以这条接口是
// 提交功能的一部分，不是可选的附加项。
func (s *AgencyApplicationService) ListMine(ctx context.Context, userID int64) ([]AgencyApplication, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("agency application service is unavailable")
	}
	if userID <= 0 {
		return nil, ErrAgencyApplicationInvalid
	}
	return s.repo.ListByUser(ctx, userID, 20)
}

// List 管理员分页查看全部申请。
func (s *AgencyApplicationService) List(ctx context.Context, filters AgencyApplicationFilters) ([]AgencyApplication, int, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("agency application service is unavailable")
	}
	if filters.Status != "" {
		if _, ok := agencyStatuses[filters.Status]; !ok {
			return nil, 0, ErrAgencyApplicationInvalid
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

// UpdateStatus 管理员改处理状态与备注。
//
// 改成 accepted 会顺带开通代理身份——在此之前 accepted 只是个状态字，
// 审核通过之后平台侧什么都不会发生，运营得手工去别处再配一遍。
//
// 开通失败不回滚状态：状态已经写进库了，代理档案可以由管理员在代理管理页补建；
// 反过来（状态回滚、档案留着）才是更难收拾的不一致。失败会向上抛，
// 由 handler 决定怎么提示。
func (s *AgencyApplicationService) UpdateStatus(ctx context.Context, id int64, status, note string) error {
	if s == nil || s.repo == nil {
		return errors.New("agency application service is unavailable")
	}
	status = strings.TrimSpace(status)
	if id <= 0 {
		return ErrAgencyApplicationInvalid
	}
	if _, ok := agencyStatuses[status]; !ok {
		return ErrAgencyApplicationInvalid
	}

	// 先把申请读出来：状态改完再读的话，拿到的 direction 仍然是对的，
	// 但要是那一瞬间申请被删了就没法建档，还不如提前失败。
	var app *AgencyApplication
	if status == "accepted" && s.agents != nil {
		loaded, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		app = loaded
	}

	if err := s.repo.UpdateStatus(ctx, id, status, truncateRunes(note, 2000)); err != nil {
		return err
	}
	if app == nil {
		return nil
	}

	_, err := s.agents.Activate(ctx, ActivateAgentInput{
		UserID:        app.UserID,
		ApplicationID: &app.ID,
		Direction:     app.Direction,
		// Mode 留空：由 DefaultAgentModeForDirection 按方向决定
		// （渠道推广→分佣，技术集成/客户交付→转售）。
		// 要改模式走代理管理页，不在审核这一步塞选项。
	})
	return err
}

// normalizeAgencyApplication 校验并裁剪到可入库的形状。
//
// 前端那套 maxlength 只是体验，拦不住直接打接口的请求——超长内容会把表撑爆，
// 所以服务端必须自己再卡一遍。截断按字符不按字节，免得把汉字劈成半个。
func normalizeAgencyApplication(userID int64, app *AgencyApplication) error {
	if userID <= 0 || app == nil {
		return ErrAgencyApplicationInvalid
	}
	app.Direction = strings.TrimSpace(app.Direction)
	if _, ok := agencyDirections[app.Direction]; !ok {
		return ErrAgencyApplicationInvalid
	}
	app.ContactName = truncateRunes(app.ContactName, agencyMaxNameLen)
	app.Email = strings.TrimSpace(app.Email)
	app.Company = truncateRunes(app.Company, agencyMaxCompanyLen)
	app.Scenario = truncateRunes(app.Scenario, agencyMaxScenarioLen)

	if app.ContactName == "" || app.Scenario == "" {
		return ErrAgencyApplicationInvalid
	}
	if len(app.Email) > agencyMaxEmailLen || !agencyEmailPattern.MatchString(app.Email) {
		return ErrAgencyApplicationInvalid
	}
	return nil
}
