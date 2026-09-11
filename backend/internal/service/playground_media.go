package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PlaygroundMediaMaxBytes 单个转存文件的上限。
//
// 实测 1K 图 1.45MB、8 秒 720p 视频 4.3MB，64MB 留足余量；
// 真正的作用是挡住异常响应，避免一次请求把盘吃掉一大块。
const PlaygroundMediaMaxBytes int64 = 64 << 20

// PlaygroundMediaMinFreeBytes 低于这个可用空间就停止转存。
//
// 这台机器 2026-09-09 刚因为 Docker 悬空镜像撑到 98%，磁盘写满会让整个服务
// （含数据库）一起出问题。转存失败只是这一张图回退到上游地址、24 小时后失效，
// 比把盘写死轻得多，所以宁可不存。
const PlaygroundMediaMinFreeBytes int64 = 5 << 30

var (
	// ErrPlaygroundMediaDiskFull 可用空间不足，本次不转存。
	ErrPlaygroundMediaDiskFull = errors.New("playground media storage is low on free space")
	// ErrPlaygroundMediaHostNotAllowed 源地址不在白名单内。
	ErrPlaygroundMediaHostNotAllowed = errors.New("playground media source host is not allowed")
)

// playgroundMediaAllowedHosts 允许转存的上游域名。
//
// **必须是白名单**：这个能力会用服务器身份去下载任意 URL 并落盘，放开就是
// 一个 SSRF + 任意写入的组合。与媒体中转代理用同一份口径。
var playgroundMediaAllowedHosts = map[string]struct{}{
	"files.toapis.cn":  {},
	"files.toapis.com": {},
	"toapis.cn":        {},
}

// PlaygroundMedia 一条已转存的生成结果。
type PlaygroundMedia struct {
	ID         int64
	UserID     int64
	TaskID     string
	Kind       string
	SourceURL  string
	StoredPath string
	MimeType   string
	SizeBytes  int64
	CreatedAt  time.Time
}

// PlaygroundMediaRepository 转存记录的存取。
type PlaygroundMediaRepository interface {
	// FindBySource 按 (用户, 源地址) 查已转存记录；没有返回 nil。
	FindBySource(ctx context.Context, userID int64, sourceURL string) (*PlaygroundMedia, error)
	// Insert 落库；并发下若已存在则返回既有记录，不报错。
	Insert(ctx context.Context, media *PlaygroundMedia) (*PlaygroundMedia, error)
	// GetOwned 取该用户自己的一条记录；不属于他就返回 nil。
	GetOwned(ctx context.Context, userID, id int64) (*PlaygroundMedia, error)
}

// PlaygroundMediaService 把上游生成结果转存到本地盘。
//
// 存在的理由：上游结果 24 小时后过期，只把地址存进库解决不了问题——
// 必须把字节本身搬过来。
type PlaygroundMediaService struct {
	repo    PlaygroundMediaRepository
	dataDir string
	client  *http.Client
	// allowedHosts 默认取包级白名单；测试里替换成 httptest 的地址，
	// 否则没法在不放开生产白名单的前提下验证下载与落盘。
	allowedHosts map[string]struct{}
	// allowInsecure 只有测试会打开：httptest 起的是 http 服务。
	// 生产恒为 false——明文 http 取不到任何上游结果，也不该去取。
	allowInsecure bool
}

func NewPlaygroundMediaService(repo PlaygroundMediaRepository, dataDir string) *PlaygroundMediaService {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return &PlaygroundMediaService{
		repo:         repo,
		dataDir:      dataDir,
		client:       &http.Client{Timeout: 180 * time.Second},
		allowedHosts: playgroundMediaAllowedHosts,
	}
}

// mediaExtensions 只认这几种类型，与生成结果的实际产出一致。
// 不按 URL 后缀猜：上游地址可能不带扩展名，猜错会存成打不开的文件。
var mediaExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
	"video/mp4":  ".mp4",
	"video/webm": ".webm",
}

// Persist 把一个上游结果地址转存到本地，返回记录。
//
// 幂等：同一个 (用户, 源地址) 已存过就直接返回旧记录，不重复下载、不重复占盘。
// 前端会反复轮询同一个任务，也可能重进页面再取一次，这一层是必需的。
func (s *PlaygroundMediaService) Persist(ctx context.Context, userID int64, sourceURL, kind, taskID string) (*PlaygroundMedia, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("playground media service is unavailable")
	}
	sourceURL = strings.TrimSpace(sourceURL)
	if userID <= 0 || sourceURL == "" {
		return nil, errors.New("invalid persist request")
	}
	if kind != "image" && kind != "video" {
		return nil, errors.New("kind must be image or video")
	}

	if existing, err := s.repo.FindBySource(ctx, userID, sourceURL); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	target, err := url.Parse(sourceURL)
	if err != nil || (target.Scheme != "https" && !(s.allowInsecure && target.Scheme == "http")) {
		return nil, ErrPlaygroundMediaHostNotAllowed
	}
	if _, ok := s.allowedHosts[strings.ToLower(target.Hostname())]; !ok {
		return nil, ErrPlaygroundMediaHostNotAllowed
	}

	// 下载前先看盘：写到一半才发现没空间，留下的是半个文件和一条指向它的记录。
	if err := s.ensureFreeSpace(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %s", resp.Status)
	}

	mime := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	ext, ok := mediaExtensions[mime]
	if !ok {
		return nil, fmt.Errorf("unsupported media type %q", mime)
	}

	relDir := filepath.Join("media", fmt.Sprint(userID), time.Now().Format("200601"))
	absDir := filepath.Join(s.dataDir, relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, err
	}

	// 文件名取源地址的哈希：同一张图重复转存会落到同一个文件名，
	// 就算 DB 记录因故丢了也不会在盘上堆出两份。
	sum := sha256.Sum256([]byte(sourceURL))
	name := hex.EncodeToString(sum[:])[:32] + ext
	relPath := filepath.ToSlash(filepath.Join(relDir, name))
	absPath := filepath.Join(absDir, name)

	// 先写临时文件再改名：中途失败（断线、超限、进程被杀）留下的是 .tmp，
	// 不会有一个体积对不上的「正式」文件被后续请求当成完整结果读走。
	tmp, err := os.CreateTemp(absDir, name+".*.tmp")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, PlaygroundMediaMaxBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return nil, copyErr
		}
		return nil, closeErr
	}
	if written > PlaygroundMediaMaxBytes {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("media exceeds %d bytes", PlaygroundMediaMaxBytes)
	}
	if written <= 0 {
		_ = os.Remove(tmpPath)
		return nil, errors.New("upstream returned an empty body")
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}

	stored, err := s.repo.Insert(ctx, &PlaygroundMedia{
		UserID:     userID,
		TaskID:     strings.TrimSpace(taskID),
		Kind:       kind,
		SourceURL:  sourceURL,
		StoredPath: relPath,
		MimeType:   mime,
		SizeBytes:  written,
	})
	if err != nil {
		// 落库失败就把文件删掉，避免盘上堆出没人认领的孤儿文件。
		_ = os.Remove(absPath)
		return nil, err
	}
	return stored, nil
}

// Open 打开该用户自己的一条转存结果。不属于他就当不存在。
func (s *PlaygroundMediaService) Open(ctx context.Context, userID, id int64) (*PlaygroundMedia, *os.File, error) {
	if s == nil || s.repo == nil {
		return nil, nil, errors.New("playground media service is unavailable")
	}
	media, err := s.repo.GetOwned(ctx, userID, id)
	if err != nil || media == nil {
		return nil, nil, err
	}
	// 清一次 stored_path 再拼接：库里的值理论上都是我们自己写的，
	// 但拼路径这种地方不留「理论上」——带 ../ 的值能读到 data 目录之外。
	clean := filepath.Clean("/" + filepath.FromSlash(media.StoredPath))
	file, err := os.Open(filepath.Join(s.dataDir, clean))
	if err != nil {
		return nil, nil, err
	}
	return media, file, nil
}

// ensureFreeSpace 可用空间低于阈值时拒绝转存。
//
// 具体的取值按平台分开实现（见 playground_media_space_unix.go / _other.go）：
// Windows 上没有 syscall.Statfs，写在一起编译期就会失败。
func (s *PlaygroundMediaService) ensureFreeSpace() error {
	free, ok := freeDiskBytes(s.dataDir)
	if !ok {
		// 拿不到磁盘信息就放行：宁可存下去，也不能因为探测不到就让整个功能失效。
		return nil
	}
	if free < PlaygroundMediaMinFreeBytes {
		return ErrPlaygroundMediaDiskFull
	}
	return nil
}

// useTestTransport 让测试把白名单换成 httptest 的地址并放行 http。
// 放在生产代码里而不是 export：只有同包的测试用得到，外部改不了。
func (s *PlaygroundMediaService) useTestTransport(hosts map[string]struct{}) {
	s.allowedHosts = hosts
	s.allowInsecure = true
}
