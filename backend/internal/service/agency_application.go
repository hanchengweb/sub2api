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
	CountPendingByUser(ctx context.Context, userID int64) (int, error)
	List(ctx context.Context, filters AgencyApplicationFilters) ([]AgencyApplication, int, error)
	UpdateStatus(ctx context.Context, id int64, status, adminNote string) error
}

// AgencyApplicationService 合作申请。
type AgencyApplicationService struct {
	repo AgencyApplicationRepository
}

func NewAgencyApplicationService(repo AgencyApplicationRepository) *AgencyApplicationService {
	return &AgencyApplicationService{repo: repo}
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
	pending, err := s.repo.CountPendingByUser(ctx, userID)
	if err != nil {
		return err
	}
	if pending >= AgencyMaxPendingPerUser {
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
	return s.repo.UpdateStatus(ctx, id, status, truncateRunes(note, 2000))
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
