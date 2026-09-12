package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

// 上限与前端保持一致（playgroundStore.ts）。服务端也要卡一道：
// 前端的限制是为 localStorage 配额设的，拦不住直接打接口的请求。
const (
	PlaygroundMaxConversations       = 50
	PlaygroundMaxMessagesPerConv     = 200
	playgroundMaxConversationIDLen   = 64
	playgroundMaxTitleLen            = 200
	playgroundMaxMessageContentBytes = 64 << 10
)

// ErrPlaygroundConversationInvalid 会话数据不合法。
var ErrPlaygroundConversationInvalid = errors.New("invalid playground conversation")

// PlaygroundMessage 一条会话消息。
type PlaygroundMessage struct {
	ID         string   `json:"id"`
	Position   int      `json:"-"`
	Role       string   `json:"role"`
	Content    string   `json:"content"`
	MediaURLs  []string `json:"media_urls"`
	MediaKind  string   `json:"media_kind,omitempty"`
	TaskID     string   `json:"task_id,omitempty"`
	Error      string   `json:"error,omitempty"`
	NeedsTopUp bool     `json:"needs_top_up,omitempty"`
	CreatedAt  int64    `json:"created_at"`
}

// PlaygroundConversation 一条会话及其全部消息。
type PlaygroundConversation struct {
	ID        string              `json:"id"`
	Title     string              `json:"title"`
	Mode      string              `json:"mode"`
	Model     string              `json:"model"`
	UpdatedAt int64               `json:"updated_at"`
	Messages  []PlaygroundMessage `json:"messages"`
}

// PlaygroundConversationRepository 会话的存取。
type PlaygroundConversationRepository interface {
	// List 返回该用户的会话（含消息），按更新时间倒序。
	List(ctx context.Context, userID int64, limit int) ([]PlaygroundConversation, error)
	// Upsert 整条覆盖写入一个会话及其消息。
	Upsert(ctx context.Context, userID int64, conv *PlaygroundConversation) error
	// Delete 删除一个会话及其消息。
	Delete(ctx context.Context, userID int64, id string) error
	// TrimOldest 只保留最近 keep 条会话，其余删除，返回删掉的条数。
	//
	// 返回条数不是给日志看的：删掉会话意味着它引用的转存文件可能已经没人
	// 引用了，调用方靠这个数字决定要不要去回收，而不是每次保存都扫一遍。
	TrimOldest(ctx context.Context, userID int64, keep int) (int, error)
}

// PlaygroundMediaReclaimer 回收已无会话引用的转存结果。
//
// 定成接口而不是直接依赖 *PlaygroundMediaService：会话存储不该因为
// 媒体转存没配起来就不能用。为 nil 时所有回收调用直接跳过。
type PlaygroundMediaReclaimer interface {
	ReclaimUnreferenced(ctx context.Context, userID int64) (int, error)
}

// PlaygroundConversationService 会话的服务端存储。
//
// 存在的理由：会话只放浏览器 localStorage 时，换台电脑或重新登录后
// 连会话列表都是空的——生成结果已经落盘了也看不到。
type PlaygroundConversationService struct {
	repo PlaygroundConversationRepository
	// reclaimer 可以为 nil：媒体转存没配起来时，会话该照常能存能删。
	reclaimer PlaygroundMediaReclaimer
}

func NewPlaygroundConversationService(repo PlaygroundConversationRepository, reclaimer PlaygroundMediaReclaimer) *PlaygroundConversationService {
	return &PlaygroundConversationService{repo: repo, reclaimer: reclaimer}
}

// reclaimMedia 回收因会话消失而没人再引用的转存文件。
//
// 一律吞掉错误：回收是清理，不是用户这次操作的目的。删会话删成功了却因为
// 清不掉几个文件而报错，用户只会以为没删掉，然后再点一次。
func (s *PlaygroundConversationService) reclaimMedia(ctx context.Context, userID int64) {
	if s == nil || s.reclaimer == nil {
		return
	}
	_, _ = s.reclaimer.ReclaimUnreferenced(ctx, userID)
}

func (s *PlaygroundConversationService) List(ctx context.Context, userID int64) ([]PlaygroundConversation, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("playground conversation service is unavailable")
	}
	if userID <= 0 {
		return nil, ErrPlaygroundConversationInvalid
	}
	return s.repo.List(ctx, userID, PlaygroundMaxConversations)
}

// Save 整条覆盖写入并顺手裁掉超量的老会话。
//
// 覆盖而不是增量：流式对话每秒要改好几次内容，逐条 diff 的复杂度远超收益。
func (s *PlaygroundConversationService) Save(ctx context.Context, userID int64, conv *PlaygroundConversation) error {
	if s == nil || s.repo == nil {
		return errors.New("playground conversation service is unavailable")
	}
	if err := normalizeConversation(userID, conv); err != nil {
		return err
	}
	if err := s.repo.Upsert(ctx, userID, conv); err != nil {
		return err
	}
	// 裁剪失败不影响本次保存：用户要的是「这条存下了」，
	// 多留几条老会话只是占点空间，不该让保存报错。
	trimmed, err := s.repo.TrimOldest(ctx, userID, PlaygroundMaxConversations)
	if err == nil && trimmed > 0 {
		// 只有真裁掉了才回收。保存是高频动作（流式对话每秒好几次），
		// 每次都去扫一遍转存表纯属浪费。
		s.reclaimMedia(ctx, userID)
	}
	return nil
}

func (s *PlaygroundConversationService) Delete(ctx context.Context, userID int64, id string) error {
	if s == nil || s.repo == nil {
		return errors.New("playground conversation service is unavailable")
	}
	id = strings.TrimSpace(id)
	if userID <= 0 || id == "" {
		return ErrPlaygroundConversationInvalid
	}
	if err := s.repo.Delete(ctx, userID, id); err != nil {
		return err
	}
	// 会话没了，它引用的图片和视频再也没有入口能看到，继续占着盘就是永久泄漏。
	s.reclaimMedia(ctx, userID)
	return nil
}

// normalizeConversation 校验并裁剪到可入库的形状。
//
// 服务端必须自己卡一遍：前端那套上限是为 localStorage 配额设的，
// 拦不住直接打接口的请求——超长内容会把表撑爆。
func normalizeConversation(userID int64, conv *PlaygroundConversation) error {
	if userID <= 0 || conv == nil {
		return ErrPlaygroundConversationInvalid
	}
	conv.ID = strings.TrimSpace(conv.ID)
	if conv.ID == "" || len(conv.ID) > playgroundMaxConversationIDLen {
		return ErrPlaygroundConversationInvalid
	}
	switch conv.Mode {
	case "chat", "image", "video":
	default:
		conv.Mode = "chat"
	}
	conv.Title = truncateRunes(conv.Title, playgroundMaxTitleLen)
	conv.Model = truncateRunes(conv.Model, 200)
	if conv.UpdatedAt <= 0 {
		conv.UpdatedAt = time.Now().UnixMilli()
	}

	// 只保留最近的消息，与前端同一口径。
	if len(conv.Messages) > PlaygroundMaxMessagesPerConv {
		conv.Messages = conv.Messages[len(conv.Messages)-PlaygroundMaxMessagesPerConv:]
	}
	kept := conv.Messages[:0]
	for i := range conv.Messages {
		m := &conv.Messages[i]
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" || len(m.ID) > playgroundMaxConversationIDLen {
			// 没有 id 的消息存不了——主键是 (user, conversation, id)。丢掉而不是报错：
			// 一条坏消息不该让整条会话存不进去。
			continue
		}
		switch m.Role {
		case "user", "assistant", "system":
		default:
			m.Role = "assistant"
		}
		m.Content = truncateBytes(m.Content, playgroundMaxMessageContentBytes)
		if m.MediaURLs == nil {
			m.MediaURLs = []string{}
		}
		if len(m.MediaURLs) > 8 {
			m.MediaURLs = m.MediaURLs[:8]
		}
		if m.CreatedAt <= 0 {
			m.CreatedAt = conv.UpdatedAt
		}
		m.Position = len(kept)
		kept = append(kept, *m)
	}
	conv.Messages = kept
	return nil
}

// truncateRunes 按字符截断，不按字节——按字节切会把一个汉字劈成半个，
// 存进去就是乱码。
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// truncateBytes 按字节上限截断，但保证不切断多字节字符。
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	for len(string(r)) > max {
		r = r[:len(r)-1]
	}
	return string(r)
}
