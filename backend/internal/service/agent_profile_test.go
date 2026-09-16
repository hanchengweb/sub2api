package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeAgentRepo struct {
	stored     map[int64]*AgentProfile
	upsertErr  error
	upsertCall int
}

func newFakeAgentRepo() *fakeAgentRepo {
	return &fakeAgentRepo{stored: map[int64]*AgentProfile{}}
}

func (r *fakeAgentRepo) Upsert(_ context.Context, p *AgentProfile) error {
	r.upsertCall++
	if r.upsertErr != nil {
		return r.upsertErr
	}
	clone := *p
	r.stored[p.UserID] = &clone
	return nil
}

func (r *fakeAgentRepo) GetByUserID(_ context.Context, userID int64) (*AgentProfile, error) {
	p, ok := r.stored[userID]
	if !ok {
		return nil, ErrAgentProfileNotFound
	}
	clone := *p
	return &clone, nil
}

func (r *fakeAgentRepo) List(context.Context, AgentProfileFilters) ([]AgentProfile, int, error) {
	return nil, 0, nil
}

func (r *fakeAgentRepo) UpdateStatus(_ context.Context, userID int64, status, note string) error {
	p, ok := r.stored[userID]
	if !ok {
		return ErrAgentProfileNotFound
	}
	p.Status = status
	p.Note = note
	return nil
}

func (r *fakeAgentRepo) UpdatePricing(_ context.Context, userID int64, planID, groupID *int64) error {
	p, ok := r.stored[userID]
	if !ok {
		return ErrAgentProfileNotFound
	}
	p.PricingPlanID = planID
	p.ResellerGroupID = groupID
	return nil
}

// fakeBinder 记录发码调用。
type fakeBinder struct {
	calls int
	err   error
}

func (b *fakeBinder) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	b.calls++
	if b.err != nil {
		return nil, b.err
	}
	return &AffiliateSummary{UserID: userID, AffCode: "TESTCODE"}, nil
}

// 方向决定默认模式：渠道推广走分佣，技术集成/客户交付走转售。
// 这两种模式互斥——同一个代理既拿返利又赚差价，平台会被削两次利润。
func TestDefaultAgentModeForDirection(t *testing.T) {
	require.Equal(t, AgentModeAffiliate, DefaultAgentModeForDirection("channel"))
	require.Equal(t, AgentModeReseller, DefaultAgentModeForDirection("integration"))
	require.Equal(t, AgentModeReseller, DefaultAgentModeForDirection("delivery"))
}

// 渠道推广开通后要有邀请码，否则他没法把客户绑到自己名下。
func TestActivateChannelDirectionIssuesAffiliateCode(t *testing.T) {
	repo := newFakeAgentRepo()
	binder := &fakeBinder{}
	svc := NewAgentService(repo, binder)

	profile, err := svc.Activate(context.Background(), ActivateAgentInput{
		UserID:    7,
		Direction: "channel",
	})
	require.NoError(t, err)
	require.Equal(t, AgentModeAffiliate, profile.Mode)
	require.Equal(t, AgentStatusActive, profile.Status)
	require.Equal(t, 1, binder.calls, "分佣代理必须有邀请码")
}

// 转售代理不发码：他的客户不走平台账号，发了也没人用，
// 反而会让后台以为他在拿返利。
func TestActivateResellerDirectionSkipsAffiliateCode(t *testing.T) {
	repo := newFakeAgentRepo()
	binder := &fakeBinder{}
	svc := NewAgentService(repo, binder)

	profile, err := svc.Activate(context.Background(), ActivateAgentInput{
		UserID:    8,
		Direction: "integration",
	})
	require.NoError(t, err)
	require.Equal(t, AgentModeReseller, profile.Mode)
	require.Equal(t, 0, binder.calls)
}

// 重复审核同一个人不该报错：管理员点两次 accepted 是常事。
func TestActivateIsIdempotent(t *testing.T) {
	repo := newFakeAgentRepo()
	svc := NewAgentService(repo, &fakeBinder{})

	for i := 0; i < 3; i++ {
		_, err := svc.Activate(context.Background(), ActivateAgentInput{
			UserID:    9,
			Direction: "delivery",
		})
		require.NoError(t, err)
	}
	require.Len(t, repo.stored, 1)
}

// 发码失败不该拖垮建档：代理身份已经写进库了，
// 码可以由管理员在后台补发，反过来没法收拾。
func TestActivateSurvivesAffiliateFailure(t *testing.T) {
	repo := newFakeAgentRepo()
	binder := &fakeBinder{err: errors.New("affiliate down")}
	svc := NewAgentService(repo, binder)

	profile, err := svc.Activate(context.Background(), ActivateAgentInput{
		UserID:    10,
		Direction: "channel",
	})
	require.NoError(t, err)
	require.NotNil(t, profile)
	require.Len(t, repo.stored, 1)
}

func TestActivateRejectsUnknownDirection(t *testing.T) {
	svc := NewAgentService(newFakeAgentRepo(), &fakeBinder{})

	_, err := svc.Activate(context.Background(), ActivateAgentInput{UserID: 1, Direction: "partner"})
	require.ErrorIs(t, err, ErrAgentProfileInvalid)

	_, err = svc.Activate(context.Background(), ActivateAgentInput{UserID: 0, Direction: "channel"})
	require.ErrorIs(t, err, ErrAgentProfileInvalid)
}

// 分佣代理挂定价方案是配置错误：他的客户按零售价付钱，
// 那份方案永远不会生效，却会让后台显示他有批发价。
func TestUpdatePricingRejectsAffiliateMode(t *testing.T) {
	repo := newFakeAgentRepo()
	svc := NewAgentService(repo, &fakeBinder{})
	_, err := svc.Activate(context.Background(), ActivateAgentInput{UserID: 11, Direction: "channel"})
	require.NoError(t, err)

	planID := int64(1)
	require.ErrorIs(t,
		svc.UpdatePricing(context.Background(), 11, &planID, nil),
		ErrAgentProfileInvalid)
}

func TestUpdatePricingAcceptsResellerMode(t *testing.T) {
	repo := newFakeAgentRepo()
	svc := NewAgentService(repo, &fakeBinder{})
	_, err := svc.Activate(context.Background(), ActivateAgentInput{UserID: 12, Direction: "delivery"})
	require.NoError(t, err)

	planID, groupID := int64(1), int64(4)
	require.NoError(t, svc.UpdatePricing(context.Background(), 12, &planID, &groupID))
	require.Equal(t, groupID, *repo.stored[12].ResellerGroupID)
}

// 不是代理和服务异常是两回事：前者回 false 不带错，
// 后者要把错误抛上去，不能被当成「他不是代理」糊弄过去。
func TestIsActiveAgentDistinguishesMissingFromError(t *testing.T) {
	repo := newFakeAgentRepo()
	svc := NewAgentService(repo, &fakeBinder{})

	ok, profile, err := svc.IsActiveAgent(context.Background(), 404)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, profile)

	_, err = svc.Activate(context.Background(), ActivateAgentInput{UserID: 13, Direction: "channel"})
	require.NoError(t, err)
	require.NoError(t, svc.UpdateStatus(context.Background(), 13, AgentStatusSuspended, "欠款"))

	ok, profile, err = svc.IsActiveAgent(context.Background(), 13)
	require.NoError(t, err)
	require.False(t, ok, "停用的代理不该继续享受代理价")
	require.NotNil(t, profile)
}
