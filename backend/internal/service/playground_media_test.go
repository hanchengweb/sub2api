package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakePlaygroundMediaRepo 内存版仓储，行为与真实实现对齐：
// 同一个 (用户, 源地址) 只存一条，重复插入返回既有记录。
type fakePlaygroundMediaRepo struct {
	rows    []*PlaygroundMedia
	nextID  int64
	inserts int
}

func (r *fakePlaygroundMediaRepo) FindBySource(_ context.Context, userID int64, src string) (*PlaygroundMedia, error) {
	for _, m := range r.rows {
		if m.UserID == userID && m.SourceURL == src {
			return m, nil
		}
	}
	return nil, nil
}

func (r *fakePlaygroundMediaRepo) Insert(_ context.Context, m *PlaygroundMedia) (*PlaygroundMedia, error) {
	r.inserts++
	r.nextID++
	m.ID = r.nextID
	r.rows = append(r.rows, m)
	return m, nil
}

func (r *fakePlaygroundMediaRepo) GetOwned(_ context.Context, userID, id int64) (*PlaygroundMedia, error) {
	for _, m := range r.rows {
		if m.ID == id && m.UserID == userID {
			return m, nil
		}
	}
	return nil, nil
}

func newTestMediaService(t *testing.T, handler http.HandlerFunc) (*PlaygroundMediaService, *fakePlaygroundMediaRepo, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	host, err := url.Parse(srv.URL)
	require.NoError(t, err)

	repo := &fakePlaygroundMediaRepo{}
	svc := NewPlaygroundMediaService(repo, t.TempDir())
	svc.useTestTransport(map[string]struct{}{host.Hostname(): {}})
	return svc, repo, srv
}

func pngHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}
}

// 转存的核心：字节真的落到盘上，而不是只记了个地址。
// 上游结果 24 小时后过期，只存地址等于没解决问题。
func TestPersistWritesBytesToDisk(t *testing.T) {
	payload := []byte("fake-png-bytes")
	svc, _, srv := newTestMediaService(t, pngHandler(payload))

	media, err := svc.Persist(context.Background(), 7, srv.URL+"/a.png", "image", "tsk_1")
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), media.SizeBytes)
	require.Equal(t, "image/png", media.MimeType)

	onDisk, err := os.ReadFile(filepath.Join(svc.dataDir, filepath.FromSlash(media.StoredPath)))
	require.NoError(t, err)
	require.Equal(t, payload, onDisk, "盘上的内容必须与上游一致")

	// 路径要带用户隔离，否则不同用户的文件会混在一个目录里
	require.Contains(t, media.StoredPath, "media/7/")
	// 存相对路径：换部署目录或挂载点后整表仍然有效
	require.False(t, filepath.IsAbs(media.StoredPath))
}

// 前端会反复轮询同一个任务，也可能重进页面再取一次。
// 不幂等的话，同一张图会被下载 N 次、占 N 份盘。
func TestPersistIsIdempotent(t *testing.T) {
	hits := 0
	svc, repo, srv := newTestMediaService(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		pngHandler([]byte("bytes"))(w, r)
	})

	first, err := svc.Persist(context.Background(), 7, srv.URL+"/a.png", "image", "tsk_1")
	require.NoError(t, err)
	second, err := svc.Persist(context.Background(), 7, srv.URL+"/a.png", "image", "tsk_1")
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, hits, "第二次不该再下载一遍")
	require.Equal(t, 1, repo.inserts)
}

// 白名单是防线：这个能力用服务器身份下载任意 URL 并落盘，
// 放开就是 SSRF 加任意写入的组合。
func TestPersistRejectsForeignHosts(t *testing.T) {
	svc, _, _ := newTestMediaService(t, pngHandler([]byte("x")))

	for _, bad := range []string{
		"https://evil.example.com/a.png",
		"https://169.254.169.254/latest/meta-data",
		"file:///etc/passwd",
	} {
		_, err := svc.Persist(context.Background(), 7, bad, "image", "t")
		require.ErrorIs(t, err, ErrPlaygroundMediaHostNotAllowed, bad)
	}
}

// 认不出的类型不落盘：按 URL 后缀猜会存成打不开的文件，
// 而且等于让上游决定我们往盘上写什么。
func TestPersistRejectsUnknownContentType(t *testing.T) {
	svc, _, srv := newTestMediaService(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	})

	_, err := svc.Persist(context.Background(), 7, srv.URL+"/a.png", "image", "t")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported media type")

	// 失败不能留下任何残留文件，包括中途的 .tmp
	require.Empty(t, listFiles(t, svc.dataDir))
}

// 空响应体也要拒：落一个 0 字节的文件下去，界面上就是一张永远加载不出的图。
func TestPersistRejectsEmptyBody(t *testing.T) {
	svc, _, srv := newTestMediaService(t, pngHandler(nil))
	_, err := svc.Persist(context.Background(), 7, srv.URL+"/a.png", "image", "t")
	require.Error(t, err)
	require.Empty(t, listFiles(t, svc.dataDir))
}

// 归属校验：别人的 id 一律当不存在，不能靠调用方记得比对 user_id。
func TestOpenRejectsOtherUsersMedia(t *testing.T) {
	svc, _, srv := newTestMediaService(t, pngHandler([]byte("bytes")))
	media, err := svc.Persist(context.Background(), 7, srv.URL+"/a.png", "image", "t")
	require.NoError(t, err)

	_, file, err := svc.Open(context.Background(), 8, media.ID)
	require.NoError(t, err)
	require.Nil(t, file, "不是自己的就该当不存在")

	_, own, err := svc.Open(context.Background(), 7, media.ID)
	require.NoError(t, err)
	require.NotNil(t, own)
	_ = own.Close()
}

// stored_path 理论上都是我们自己写的，但拼路径这种地方不留「理论上」：
// 带 ../ 的值能读到 data 目录之外。
func TestOpenIsNotFooledByTraversalPath(t *testing.T) {
	repo := &fakePlaygroundMediaRepo{}
	svc := NewPlaygroundMediaService(repo, t.TempDir())
	_, err := repo.Insert(context.Background(), &PlaygroundMedia{
		UserID: 7, Kind: "image", SourceURL: "x",
		StoredPath: "../../../../etc/passwd", MimeType: "image/png", SizeBytes: 1,
	})
	require.NoError(t, err)

	_, file, err := svc.Open(context.Background(), 7, 1)
	if file != nil {
		_ = file.Close()
		t.Fatal("不该打开 data 目录之外的文件")
	}
	require.Error(t, err, "越界路径应打开失败")
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && !strings.HasSuffix(path, ".keep") {
			out = append(out, path)
		}
		return nil
	}))
	return out
}
