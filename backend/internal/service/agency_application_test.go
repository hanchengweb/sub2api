package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeAgencyRepo struct {
	total     int
	sameDir   int
	created   []AgencyApplication
	lastDir   string
	countErr  error
	createErr error
}

func (r *fakeAgencyRepo) Create(_ context.Context, app *AgencyApplication) error {
	if r.createErr != nil {
		return r.createErr
	}
	app.ID = int64(len(r.created) + 1)
	r.created = append(r.created, *app)
	return nil
}

func (r *fakeAgencyRepo) ListByUser(context.Context, int64, int) ([]AgencyApplication, error) {
	return nil, nil
}

func (r *fakeAgencyRepo) CountPending(_ context.Context, _ int64, direction string) (int, int, error) {
	r.lastDir = direction
	return r.total, r.sameDir, r.countErr
}

func (r *fakeAgencyRepo) List(context.Context, AgencyApplicationFilters) ([]AgencyApplication, int, error) {
	return nil, 0, nil
}

func (r *fakeAgencyRepo) UpdateStatus(context.Context, int64, string, string) error { return nil }

func validApp(direction string) *AgencyApplication {
	return &AgencyApplication{
		Direction:   direction,
		ContactName: "韩诚",
		Email:       "a@b.com",
		Scenario:    "想做渠道",
	}
}

func TestSubmitAgencyApplication(t *testing.T) {
	repo := &fakeAgencyRepo{}
	svc := NewAgencyApplicationService(repo)

	require.NoError(t, svc.Submit(context.Background(), 9, validApp("channel")))
	require.Len(t, repo.created, 1)
	require.Equal(t, "pending", repo.created[0].Status)
	require.Equal(t, int64(9), repo.created[0].UserID)
	require.Equal(t, "channel", repo.lastDir, "重复判定要按方向查，不能只数总量")
}

// 同一个方向连提第二条就是重复提交——这是「请勿重复提交」真正落地的地方。
func TestSubmitRejectsSecondPendingInSameDirection(t *testing.T) {
	repo := &fakeAgencyRepo{total: 1, sameDir: 1}
	svc := NewAgencyApplicationService(repo)

	err := svc.Submit(context.Background(), 9, validApp("channel"))
	require.ErrorIs(t, err, ErrAgencyTooManyPending)
	require.Empty(t, repo.created)
}

// 但换个方向是正常诉求：一个人既想做渠道又想做交付，不该被算成刷单。
func TestSubmitAllowsAnotherDirectionWhileOneIsPending(t *testing.T) {
	repo := &fakeAgencyRepo{total: 1, sameDir: 0}
	svc := NewAgencyApplicationService(repo)

	require.NoError(t, svc.Submit(context.Background(), 9, validApp("delivery")))
	require.Len(t, repo.created, 1)
}

// 三个方向都提过之后就到顶了，兜底上限仍然生效。
func TestSubmitStillHonoursTheOverallPendingCap(t *testing.T) {
	repo := &fakeAgencyRepo{total: AgencyMaxPendingPerUser, sameDir: 0}
	svc := NewAgencyApplicationService(repo)

	require.ErrorIs(t, svc.Submit(context.Background(), 9, validApp("delivery")), ErrAgencyTooManyPending)
}

// 校验不过就不该去数待处理，更不该落库。
func TestSubmitRejectsInvalidPayloadBeforeTouchingTheRepo(t *testing.T) {
	repo := &fakeAgencyRepo{}
	svc := NewAgencyApplicationService(repo)

	bad := validApp("channel")
	bad.Email = "not-an-email"
	require.ErrorIs(t, svc.Submit(context.Background(), 9, bad), ErrAgencyApplicationInvalid)
	require.Empty(t, repo.lastDir)
	require.Empty(t, repo.created)

	require.ErrorIs(t, svc.Submit(context.Background(), 9, validApp("nope")), ErrAgencyApplicationInvalid)
}
