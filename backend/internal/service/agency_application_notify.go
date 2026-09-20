package service

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"
)

// 审核结果通知。
//
// 为什么必须发：审核通过之后系统会默默给申请人开通代理身份（建 agent_profiles、
// 邀请码从此可以锁定客户归属），但申请人**完全不知道**——他只能自己再打开
// 「成为代理」页去看那一行状态。不通知等于让审核结果躺在那儿没人认领。
//
// 为什么走邮件而不是站内公告：公告的定向只支持「订阅套餐」和「余额」两种条件
// （见 domain.AnnouncementCondition），没有按用户定向，发一条全站都看得到。
// 而申请表单本来就收了联系邮箱，收它就是为了这一刻。

const agencyNotifyEmailTimeout = 20 * time.Second

// agencyNotifier 只取发信这一个能力。
//
// 不直接依赖 *EmailService：审核路径不该因为邮件模块换实现而改签名，
// 测试里也好塞一个假的看到底发了什么。
type agencyNotifier interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

// notifyApplicant 把审核结果发给申请人。
//
// **一切错误只记日志不返回**：审核是主操作，状态已经落库、代理身份可能也已开通，
// 这时候因为发不出邮件而让整个请求报错，管理员会以为没审核成功而重复操作。
func (s *AgencyApplicationService) notifyApplicant(app *AgencyApplication, status, note string) {
	if s == nil || s.notifier == nil || app == nil {
		return
	}
	to := strings.TrimSpace(app.Email)
	if to == "" {
		slog.Warn("agency: 申请没有联系邮箱，跳过通知", "application_id", app.ID)
		return
	}
	subject, body := agencyNotifyContent(s.siteName(), app, status, note)
	if subject == "" {
		return // contacted 之类的中间态不打扰申请人
	}

	ctx, cancel := context.WithTimeout(context.Background(), agencyNotifyEmailTimeout)
	defer cancel()
	if err := s.notifier.SendEmail(ctx, to, subject, body); err != nil {
		slog.Error("agency: 审核结果通知发送失败",
			"application_id", app.ID, "status", status, "error", err)
		return
	}
	slog.Info("agency: 审核结果已通知申请人", "application_id", app.ID, "status", status)
}

// agencyNotifyContent 生成通知内容；返回空主题表示这个状态不发通知。
//
// 只在 accepted / rejected 两个**终态**发信。contacted 是运营内部流转，
// 每次改都发一封会变成骚扰。
//
// 正文必须是 HTML：EmailService.SendEmailWithConfig 把 Content-Type 写死成
// text/html，纯文本里的换行到了客户端会糊成一整坨。
//
// 所有外部输入都过 html.EscapeString——ContactName 是申请人自己填进表单的，
// 直接拼进 HTML 等于给他一个注入口子；备注是管理员填的，一并转义不吃亏。
func agencyNotifyContent(siteName string, app *AgencyApplication, status, note string) (string, string) {
	name := strings.TrimSpace(app.ContactName)
	if name == "" {
		name = "您好"
	}
	note = strings.TrimSpace(note)

	// 为什么不把邀请码本身写进邮件：码是懒发的——渠道方向在 Activate 时就发，
	// 技术集成/客户交付走转售模式，Activate 不发，要等代理第一次打开面板
	//（GET /user/aff）才建行。在这里现查会把一个可选的通知路径变成必须能
	// 读到 affiliate 服务，发不出信还不如让人自己去面板拿。
	var subject, lead string
	var paragraphs []string
	switch status {
	case "accepted":
		subject = fmt.Sprintf("[%s] 合作申请已通过", siteName)
		lead = fmt.Sprintf("%s，您在%s提交的合作申请已通过审核。",
			html.EscapeString(name), html.EscapeString(siteName))
		paragraphs = []string{
			"代理身份已为您开通。在侧边栏「成为代理 - 代理面板」里可以拿到您的专属邀请链接。",
			"通过该链接注册的客户会自动归属到您名下；<strong>归属在客户注册那一刻锁定，事后无法更改</strong>。",
			"代理面板同时会展示您的客户数、客户消耗与结算记录。",
		}
	case "rejected":
		subject = fmt.Sprintf("[%s] 合作申请结果", siteName)
		lead = fmt.Sprintf("%s，感谢您在%s提交合作申请。",
			html.EscapeString(name), html.EscapeString(siteName))
		paragraphs = []string{
			"很遗憾，本次申请暂未通过。",
			"如情况有变化，欢迎再次提交申请。",
		}
	default:
		return "", ""
	}

	var content strings.Builder
	for _, p := range paragraphs {
		fmt.Fprintf(&content, "        <p>%s</p>\n", p)
	}
	if note != "" {
		fmt.Fprintf(&content,
			"        <div class=\"note\"><span>平台备注</span><p>%s</p></div>\n",
			html.EscapeString(note))
	}

	return subject, fmt.Sprintf(agencyNotifyEmailTemplate,
		html.EscapeString(siteName), lead, content.String(), html.EscapeString(siteName))
}

// agencyNotifyEmailTemplate 审核结果邮件外壳。
// 格式参数依次是：siteName、开头一句、正文段落块、页脚 siteName。
//
// 样式全部内联在 style 标签里、不引外部资源：邮件客户端大多不加载远程 CSS，
// 图片还常被默认拦掉。这里也刻意不放按钮链接——站点地址是配置项，
// 拼错的外链比没有链接更糟。
const agencyNotifyEmailTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background-color: #2563eb; color: #fff; padding: 28px 30px; }
        .header h1 { margin: 0; font-size: 20px; font-weight: 600; }
        .content { padding: 32px 30px; color: #333; font-size: 15px; line-height: 1.7; }
        .content .lead { font-size: 16px; margin: 0 0 18px; }
        .content p { margin: 0 0 12px; }
        .note { margin-top: 20px; padding: 14px 16px; background-color: #f8f9fa; border-left: 3px solid #2563eb; border-radius: 4px; }
        .note span { display: block; font-size: 12px; color: #999; margin-bottom: 6px; }
        .note p { margin: 0; color: #555; }
        .footer { background-color: #f8f9fa; padding: 18px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h1>%s</h1></div>
        <div class="content">
            <p class="lead">%s</p>
%s        </div>
        <div class="footer">%s</div>
    </div>
</body>
</html>`

func (s *AgencyApplicationService) siteName() string {
	if s == nil || s.settings == nil {
		return defaultSiteName
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	name, err := s.settings.GetValue(ctx, SettingKeySiteName)
	if err != nil || strings.TrimSpace(name) == "" {
		return defaultSiteName
	}
	return name
}
