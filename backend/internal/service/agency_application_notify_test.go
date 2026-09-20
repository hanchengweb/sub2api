package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type capturedMail struct {
	to, subject, body string
}

type fakeAgencyMailer struct {
	sent []capturedMail
	err  error
}

func (m *fakeAgencyMailer) SendEmail(_ context.Context, to, subject, body string) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, capturedMail{to, subject, body})
	return nil
}

// withSiteName 造一个配好站点名的通知服务；settings 传 nil 走兜底站点名。
func newNotifyService(mailer agencyNotifier, settings SettingRepository) *AgencyApplicationService {
	svc := NewAgencyApplicationService(&fakeAgencyRepo{})
	svc.notifier = mailer
	svc.settings = settings
	return svc
}

func settingsWithSiteName(t *testing.T, name string) SettingRepository {
	t.Helper()
	repo := newStubSettingRepo()
	require.NoError(t, repo.Set(context.Background(), SettingKeySiteName, name))
	return repo
}

func acceptedApp() *AgencyApplication {
	return &AgencyApplication{
		ID: 7, UserID: 9, Direction: "channel",
		ContactName: "韩诚", Email: "a@b.com", Scenario: "渠道",
	}
}

// 通过和拒绝都要通知：审核结果躺在库里没人认领，申请人只能自己反复来看。
func TestAgencyNotifyOnTerminalStates(t *testing.T) {
	for _, tc := range []struct{ status, wantInSubject string }{
		{"accepted", "已通过"},
		{"rejected", "结果"},
	} {
		mailer := &fakeAgencyMailer{}
		svc := newNotifyService(mailer, settingsWithSiteName(t, "风合智联"))
		svc.notifyApplicant(acceptedApp(), tc.status, "已电话联系")

		require.Len(t, mailer.sent, 1, tc.status)
		require.Equal(t, "a@b.com", mailer.sent[0].to)
		require.Contains(t, mailer.sent[0].subject, "风合智联")
		require.Contains(t, mailer.sent[0].subject, tc.wantInSubject)
		// 管理员写的备注申请人本来就看得到，邮件里一并带上
		require.Contains(t, mailer.sent[0].body, "已电话联系")
	}
}

// contacted 是运营内部流转，每改一次就发一封会变成骚扰。
func TestAgencyNoNotifyOnIntermediateState(t *testing.T) {
	mailer := &fakeAgencyMailer{}
	svc := newNotifyService(mailer, settingsWithSiteName(t, "风合智联"))
	svc.notifyApplicant(acceptedApp(), "contacted", "")
	require.Empty(t, mailer.sent)
}

// 通过信必须指向真实存在的页面：邀请链接在「成为代理 - 代理面板」里，
// 不在「个人资料」下面。
func TestAgencyAcceptedMailPointsAtAgentPanel(t *testing.T) {
	mailer := &fakeAgencyMailer{}
	svc := newNotifyService(mailer, settingsWithSiteName(t, "风合智联"))
	svc.notifyApplicant(acceptedApp(), "accepted", "")
	require.Len(t, mailer.sent, 1)
	require.Contains(t, mailer.sent[0].body, "代理面板")
	require.Contains(t, mailer.sent[0].body, "邀请链接")
	// 别再写成「个人资料 - 邀请」：那个页面不存在，邀请码在代理面板里
	require.NotContains(t, mailer.sent[0].body, "个人资料")
}

// 发信失败不能让审核报错：状态已落库、代理身份已开通，这时候报错会让
// 管理员以为没审核成功而重复操作。
func TestAgencyNotifyFailureIsSwallowed(t *testing.T) {
	svc := newNotifyService(&fakeAgencyMailer{err: errors.New("smtp down")}, settingsWithSiteName(t, "风合智联"))
	require.NotPanics(t, func() { svc.notifyApplicant(acceptedApp(), "accepted", "") })
}

// 没配通知也要能正常审核（接线前的老行为）。
func TestAgencyNotifyIsOptional(t *testing.T) {
	svc := NewAgencyApplicationService(&fakeAgencyRepo{})
	require.NotPanics(t, func() { svc.notifyApplicant(acceptedApp(), "accepted", "") })
}

// 申请没留邮箱就跳过，不能拿空收件人去发。
func TestAgencyNotifySkipsEmptyEmail(t *testing.T) {
	mailer := &fakeAgencyMailer{}
	svc := newNotifyService(mailer, settingsWithSiteName(t, "风合智联"))
	app := acceptedApp()
	app.Email = "   "
	svc.notifyApplicant(app, "accepted", "")
	require.Empty(t, mailer.sent)
}

// 取不到站点名时用兜底名，不能发出「[] 合作申请已通过」这种标题。
func TestAgencyNotifyFallsBackToDefaultSiteName(t *testing.T) {
	mailer := &fakeAgencyMailer{}
	svc := newNotifyService(mailer, nil)
	svc.notifyApplicant(acceptedApp(), "accepted", "")
	require.Len(t, mailer.sent, 1)
	require.Contains(t, mailer.sent[0].subject, defaultSiteName)
	require.False(t, strings.HasPrefix(mailer.sent[0].subject, "[]"))
}

// --- UpdateStatus 整条路径 ---

func newAuditService(t *testing.T, agentRepo *fakeAgentRepo, mailer agencyNotifier) (*AgencyApplicationService, *fakeAgencyRepo) {
	t.Helper()
	repo := &fakeAgencyRepo{stored: acceptedApp()}
	svc := NewAgencyApplicationServiceWithAgents(repo, NewAgentService(agentRepo, &fakeBinder{}))
	return svc.WithNotifier(mailer, settingsWithSiteName(t, "风合智联")), repo
}

// 开通失败 = 一封都不能发。
//
// 这条钉的是先前用 defer 发通知的写法：那样开通失败照样发出「已通过」，
// 申请人拿着不存在的代理身份去带客户，邀请码根本不生效。
func TestAgencyUpdateStatusSendsNoMailWhenActivationFails(t *testing.T) {
	agentRepo := newFakeAgentRepo()
	agentRepo.upsertErr = errors.New("db down")
	mailer := &fakeAgencyMailer{}
	svc, _ := newAuditService(t, agentRepo, mailer)

	err := svc.UpdateStatus(context.Background(), 7, "accepted", "")

	require.Error(t, err)
	require.Empty(t, mailer.sent, "开通失败却发出了通过通知")
}

// 开通成功才发信，且信是在开通之后发的。
func TestAgencyUpdateStatusMailsAfterSuccessfulActivation(t *testing.T) {
	agentRepo := newFakeAgentRepo()
	mailer := &fakeAgencyMailer{}
	svc, repo := newAuditService(t, agentRepo, mailer)

	require.NoError(t, svc.UpdateStatus(context.Background(), 7, "accepted", "欢迎"))

	require.Len(t, repo.statusUpdates, 1)
	require.Equal(t, "accepted", repo.statusUpdates[0].Status)
	require.Contains(t, agentRepo.stored, int64(9), "代理档案没建起来")
	require.Len(t, mailer.sent, 1)
	require.Contains(t, mailer.sent[0].body, "欢迎")
}

// 拒绝也要通知，但不能顺手给人开通代理身份。
func TestAgencyUpdateStatusRejectedMailsWithoutActivation(t *testing.T) {
	agentRepo := newFakeAgentRepo()
	mailer := &fakeAgencyMailer{}
	svc, _ := newAuditService(t, agentRepo, mailer)

	require.NoError(t, svc.UpdateStatus(context.Background(), 7, "rejected", "场景不匹配"))

	require.Zero(t, agentRepo.upsertCall, "拒绝却开通了代理")
	require.Len(t, mailer.sent, 1)
	require.Contains(t, mailer.sent[0].body, "场景不匹配")
}

// 通知发不出去不影响审核结果：状态和代理身份都已经落库了。
func TestAgencyUpdateStatusSucceedsWhenMailFails(t *testing.T) {
	agentRepo := newFakeAgentRepo()
	svc, repo := newAuditService(t, agentRepo, &fakeAgencyMailer{err: errors.New("smtp down")})

	require.NoError(t, svc.UpdateStatus(context.Background(), 7, "accepted", ""))
	require.Len(t, repo.statusUpdates, 1)
	require.Contains(t, agentRepo.stored, int64(9))
}

// 正文必须是 HTML：EmailService 把 Content-Type 写死成 text/html，
// 纯文本换行到客户端会糊成一坨。
func TestAgencyMailBodyIsHTML(t *testing.T) {
	mailer := &fakeAgencyMailer{}
	svc := newNotifyService(mailer, settingsWithSiteName(t, "风合智联"))
	svc.notifyApplicant(acceptedApp(), "accepted", "")
	require.Len(t, mailer.sent, 1)
	require.Contains(t, mailer.sent[0].body, "!DOCTYPE html")
}

// 联系人姓名是申请人自己填进表单的，直接拼进 HTML 就是个注入口子。
func TestAgencyMailEscapesApplicantInput(t *testing.T) {
	mailer := &fakeAgencyMailer{}
	svc := newNotifyService(mailer, settingsWithSiteName(t, "风合智联"))
	app := acceptedApp()
	app.ContactName = "<script>alert(1)</script>"
	svc.notifyApplicant(app, "accepted", "<img src=x onerror=alert(2)>")

	body := mailer.sent[0].body
	require.NotContains(t, body, "<script>")
	require.NotContains(t, body, "<img src=x")
	require.Contains(t, body, "&lt;script&gt;", "应该以转义形式原样展示")
}
