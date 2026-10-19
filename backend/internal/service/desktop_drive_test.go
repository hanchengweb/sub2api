package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type memoryDriveRepo struct {
	mu    sync.Mutex
	users map[int64]*memoryDriveStore
}
type memoryDriveStore struct {
	DesktopDriveStore
	drives                 map[string]DesktopDrive
	items                  map[string]DesktopDriveItem
	owners                 map[string]string
	requests               map[string]DesktopDriveRequest
	garbage                map[string]bool
	extraBytes, extraCount int64
}

func (r *memoryDriveRepo) WithUser(_ context.Context, userID int64, fn func(DesktopDriveStore) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users == nil {
		r.users = map[int64]*memoryDriveStore{}
	}
	if r.users[userID] == nil {
		r.users[userID] = &memoryDriveStore{drives: map[string]DesktopDrive{}, items: map[string]DesktopDriveItem{}, owners: map[string]string{}, requests: map[string]DesktopDriveRequest{}, garbage: map[string]bool{}}
	}
	return fn(r.users[userID])
}
func (s *memoryDriveStore) Drives(context.Context) ([]DesktopDrive, error) {
	rows := []DesktopDrive{}
	for _, d := range s.drives {
		for id := range s.items {
			if s.owners[id] == d.ID {
				d.ItemCount++
			}
		}
		rows = append(rows, d)
	}
	return rows, nil
}
func (s *memoryDriveStore) Drive(_ context.Context, id string) (*DesktopDrive, error) {
	d, ok := s.drives[id]
	if !ok {
		return nil, nil
	}
	return &d, nil
}
func (s *memoryDriveStore) Item(_ context.Context, drive, id string) (*DesktopDriveItem, error) {
	d, ok := s.items[id]
	if !ok || s.owners[id] != drive {
		return nil, nil
	}
	return &d, nil
}

func (s *memoryDriveStore) FileExists(_ context.Context, id string) (bool, error) {
	item, ok := s.items[id]
	return ok && item.ItemType == "file", nil
}
func (s *memoryDriveStore) Items(_ context.Context, drive, parent string, offset, limit int) ([]DesktopDriveItem, int64, error) {
	rows := []DesktopDriveItem{}
	for id, d := range s.items {
		if s.owners[id] == drive && d.ParentID == parent {
			rows = append(rows, d)
		}
	}
	total := len(rows)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return rows[offset:end], int64(total), nil
}
func (s *memoryDriveStore) Usage(context.Context) (int64, int64, error) {
	used := s.extraBytes
	for _, d := range s.items {
		used += d.SizeBytes
	}
	return used, s.extraCount + int64(len(s.drives)+len(s.items)), nil
}
func (s *memoryDriveStore) CreateDrive(_ context.Context, d DesktopDrive) error {
	s.drives[d.ID] = d
	return nil
}
func (s *memoryDriveStore) CreateItem(_ context.Context, drive string, d DesktopDriveItem) error {
	s.items[d.ID] = d
	s.owners[d.ID] = drive
	return nil
}
func (s *memoryDriveStore) Rename(_ context.Context, drive, id, name string) error {
	if id == "" {
		d := s.drives[drive]
		d.Name = name
		s.drives[drive] = d
	} else {
		d := s.items[id]
		d.Name = name
		s.items[id] = d
	}
	return nil
}
func (s *memoryDriveStore) Delete(ctx context.Context, drive, id string) error {
	for child, d := range s.items {
		if s.owners[child] == drive && (id == "" || child == id || d.ParentID == id) {
			if child != id {
				_ = s.Delete(ctx, drive, child)
			}
			if d.ItemType == "file" {
				s.garbage[child] = true
			}
			delete(s.items, child)
			delete(s.owners, child)
		}
	}
	if id == "" {
		delete(s.drives, drive)
	}
	return nil
}
func (s *memoryDriveStore) Request(_ context.Context, id string) (*DesktopDriveRequest, error) {
	d, ok := s.requests[id]
	if !ok {
		return nil, nil
	}
	return &d, nil
}
func (s *memoryDriveStore) SaveRequest(_ context.Context, id string, d DesktopDriveRequest) error {
	s.requests[id] = d
	return nil
}
func (s *memoryDriveStore) Garbage(context.Context) ([]string, error) {
	ids := []string{}
	for id := range s.garbage {
		ids = append(ids, id)
	}
	return ids, nil
}
func (s *memoryDriveStore) ForgetGarbage(_ context.Context, id string) error {
	delete(s.garbage, id)
	return nil
}

func newDriveTest(t *testing.T) (*DesktopDriveService, *memoryDriveRepo) {
	t.Helper()
	r := &memoryDriveRepo{}
	s := NewDesktopDriveService(r, t.TempDir())
	s.freeSpace = func(string) (int64, bool) { return 20 << 30, true }
	return s, r
}
func createTestDrive(t *testing.T, s *DesktopDriveService, user int64) string {
	t.Helper()
	data, err := s.Mutate(context.Background(), user, uuid.NewString(), "create", "", "", DesktopDriveInput{Name: "我的资料"}, nil)
	require.NoError(t, err)
	var d DesktopDrive
	require.NoError(t, json.Unmarshal(data, &d))
	return d.ID
}
func uploadTestFile(t *testing.T, s *DesktopDriveService, user int64, drive, parent, name, content string) (string, string) {
	t.Helper()
	f, err := s.Stage(context.Background(), user, name, strings.NewReader(content))
	require.NoError(t, err)
	files := []DesktopDriveUpload{*f}
	defer s.DropUploads(user, files)
	key := uuid.NewString()
	data, err := s.Mutate(context.Background(), user, key, "upload", drive, "", DesktopDriveInput{ParentID: parent}, files)
	require.NoError(t, err)
	var result struct{ Items []DesktopDriveItem }
	require.NoError(t, json.Unmarshal(data, &result))
	return result.Items[0].ID, key
}

func TestDesktopDrivePersonalKeyBoundary(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*APIKey)
		allowed bool
	}{
		{"zero balance", func(k *APIKey) { k.User.Balance = 0 }, true},
		{"organization service", func(k *APIKey) { k.User.AccountType = AccountTypeOrganizationService }, false},
		{"wrong group", func(k *APIKey) { id := int64(2); k.GroupID = &id; k.Group.ID = 2 }, false},
		{"absent group", func(k *APIKey) { k.Group = nil }, false},
		{"limited key", func(k *APIKey) { k.Quota = 1 }, false},
		{"disabled", func(k *APIKey) { k.Status = StatusAPIKeyDisabled }, false},
		{"expired", func(k *APIKey) { past := time.Now().Add(-time.Hour); k.ExpiresAt = &past }, false},
		{"wrong user", func(k *APIKey) { k.User.ID = 2 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := int64(4)
			k := &APIKey{UserID: 1, Status: StatusActive, GroupID: &group, Group: &Group{ID: 4}, User: &User{ID: 1, Status: StatusActive}}
			test.change(k)
			require.Equal(t, test.allowed, ValidateDesktopDriveKey(k) == nil)
		})
	}
}

func TestDesktopDriveAccountIsolationAndFileLifecycle(t *testing.T) {
	ctx := context.Background()
	s, r := newDriveTest(t)
	drive := createTestDrive(t, s, 7)
	id, key := uploadTestFile(t, s, 7, drive, "", "说明.txt", "你好\n云盘")
	_, err := s.List(ctx, 8, drive, "", 0, 100)
	require.ErrorIs(t, err, ErrDesktopDriveNotFound)
	_, _, err = s.Original(ctx, 8, drive, id)
	require.ErrorIs(t, err, ErrDesktopDriveNotFound)
	other := createTestDrive(t, s, 7)
	_, _, err = s.Original(ctx, 7, other, id)
	require.ErrorIs(t, err, ErrDesktopDriveNotFound)
	item, f, err := s.Original(ctx, 7, drive, id)
	require.NoError(t, err)
	require.Equal(t, "说明.txt", item.FileName)
	bytes, err := io.ReadAll(f)
	f.Close()
	require.NoError(t, err)
	require.Equal(t, "你好\n云盘", string(bytes))
	content, err := s.Content(ctx, 7, drive, id, 0, 2)
	require.NoError(t, err)
	require.Equal(t, "你好", content["content"])
	require.Equal(t, true, content["truncated"])
	// Rotating credentials keeps ownership at user 7, independent of key ID.
	cat, err := s.Catalog(ctx, &APIKey{ID: 999, UserID: 7, User: &User{Username: "新设备"}})
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("WH-%d", 7), cat["account"].(map[string]any)["reference"])
	deleteKey := uuid.NewString()
	_, err = s.Mutate(ctx, 7, deleteKey, "delete", drive, "", DesktopDriveInput{}, nil)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(s.dataDir, "desktop-drive", "7", id))
	require.True(t, os.IsNotExist(err))
	require.Empty(t, r.users[7].garbage)
	// A completed request stays remembered even after its drive was deleted.
	file, err := s.Stage(context.Background(), 7, "说明.txt", strings.NewReader("你好\n云盘"))
	require.NoError(t, err)
	defer s.DropUploads(7, []DesktopDriveUpload{*file})
	_, err = s.Mutate(ctx, 7, key, "upload", drive, "", DesktopDriveInput{}, []DesktopDriveUpload{*file})
	require.NoError(t, err)
	require.Empty(t, r.users[7].items)
}

func TestDesktopDrivePayloadBindingAndConcurrentQuota(t *testing.T) {
	ctx := context.Background()
	s, r := newDriveTest(t)
	key := uuid.NewString()
	input := DesktopDriveInput{Name: "第一份", ClientRequestID: key}
	first, err := s.Mutate(ctx, 9, key, "create", "", "", input, nil)
	require.NoError(t, err)
	second, err := s.Mutate(ctx, 9, key, "create", "", "", input, nil)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
	require.Len(t, r.users[9].drives, 1)
	input.Name = "不同内容"
	_, err = s.Mutate(ctx, 9, key, "create", "", "", input, nil)
	require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
	var d DesktopDrive
	require.NoError(t, json.Unmarshal(first, &d))
	r.users[9].extraCount = DesktopDriveMaxItems - 2
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Mutate(ctx, 9, uuid.NewString(), "folder", d.ID, "", DesktopDriveInput{Name: "并发文件夹"}, nil)
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, success)
}

func TestDesktopDriveStorageAndUnavailableContent(t *testing.T) {
	ctx := context.Background()
	s, r := newDriveTest(t)
	drive := createTestDrive(t, s, 1)
	for _, name := range []string{"../escape", "a/b", "a\\b", "..", "\x00x"} {
		_, err := s.Stage(context.Background(), 1, name, strings.NewReader("x"))
		require.Error(t, err)
	}
	s.freeSpace = func(string) (int64, bool) { return (5 << 30) - 1, true }
	_, err := s.Stage(context.Background(), 1, "x.txt", strings.NewReader("x"))
	require.Error(t, err)
	s.freeSpace = func(string) (int64, bool) { return 20 << 30, true }
	id, _ := uploadTestFile(t, s, 1, drive, "", "原件.pdf", "real PDF bytes, not extracted text")
	result, err := s.Content(ctx, 1, drive, id, 0, 100)
	require.NoError(t, err)
	require.Equal(t, "", result["content"])
	require.Equal(t, true, result["contentUnavailable"])
	htmlID, _ := uploadTestFile(t, s, 1, drive, "", "页面.html", "<h1>正文</h1><script>private script</script><style>secret css</style><p>末尾</p>")
	result, err = s.Content(ctx, 1, drive, htmlID, 0, 100)
	require.NoError(t, err)
	require.Contains(t, result["content"], "正文")
	require.NotContains(t, result["content"], "private script")
	require.NotContains(t, result["content"], "secret css")
	r.users[1].extraBytes = DesktopDriveLimitBytes
	staged, err := s.Stage(context.Background(), 1, "quota.txt", strings.NewReader("x"))
	require.Error(t, err)
	require.Nil(t, staged)
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "desktop-drive", "1"))
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".upload-"))
	}
}

func TestDesktopDriveUploadAdmissionIsBoundedAndReleased(t *testing.T) {
	s, _ := newDriveTest(t)
	ctx := context.Background()
	release, err := s.BeginUpload(ctx, 1)
	require.NoError(t, err)
	_, err = s.BeginUpload(ctx, 1)
	require.Error(t, err)
	releases := []func(){release}
	for user := int64(2); user <= 4; user++ {
		done, err := s.BeginUpload(ctx, user)
		require.NoError(t, err)
		releases = append(releases, done)
	}
	_, err = s.BeginUpload(ctx, 5)
	require.Error(t, err)
	for _, done := range releases {
		done()
		done()
	}
	done, err := s.BeginUpload(ctx, 1)
	require.NoError(t, err)
	done()
	require.Empty(t, s.uploadUsers)
}

func TestDesktopDriveConcurrentScratchReservesDiskAndCleansFailure(t *testing.T) {
	s, _ := newDriveTest(t)
	s.freeSpace = func(string) (int64, bool) { return (5 << 30) + (75 << 20), true }
	reader, writer := io.Pipe()
	result := make(chan *DesktopDriveUpload, 1)
	fail := make(chan error, 1)
	go func() {
		file, err := s.Stage(context.Background(), 1, "inflight.txt", reader)
		result <- file
		fail <- err
	}()
	require.Eventually(t, func() bool {
		s.uploadMu.Lock()
		defer s.uploadMu.Unlock()
		return s.reservedBytes == DesktopDriveMaxFileBytes
	}, time.Second, 10*time.Millisecond)
	_, err := s.Stage(context.Background(), 2, "second.txt", strings.NewReader("x"))
	require.Error(t, err, "concurrent writes must reserve the 5GiB disk floor")
	require.NoError(t, writer.CloseWithError(io.ErrUnexpectedEOF))
	require.Nil(t, <-result)
	require.ErrorIs(t, <-fail, io.ErrUnexpectedEOF)
	reader.Close()
	require.Zero(t, s.reservedBytes)
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "desktop-drive", "1"))
	require.NoError(t, err)
	require.Empty(t, entries, "failed stream cannot leave a temporary file")
}

func TestDesktopDriveCrashMaintenancePreservesLiveAndActiveObjects(t *testing.T) {
	ctx := context.Background()
	s, _ := newDriveTest(t)
	drive := createTestDrive(t, s, 1)
	live, _ := uploadTestFile(t, s, 1, drive, "", "live.txt", "keep")
	dir := filepath.Join(s.dataDir, "desktop-drive", "1")
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, live), old, old))
	orphan := uuid.NewString()
	staleTemp := ".upload-" + uuid.NewString()
	fresh := uuid.NewString()
	for _, name := range []string{orphan, staleTemp, fresh} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600))
		if name != fresh {
			require.NoError(t, os.Chtimes(filepath.Join(dir, name), old, old))
		}
	}
	_, err := s.Catalog(ctx, &APIKey{UserID: 1, User: &User{}})
	require.NoError(t, err)
	for _, name := range []string{orphan, staleTemp} {
		_, err := os.Stat(filepath.Join(dir, name))
		require.True(t, os.IsNotExist(err))
	}
	for _, name := range []string{live, fresh} {
		_, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
	}
	release, err := s.BeginUpload(ctx, 1)
	require.NoError(t, err)
	active := ".upload-" + uuid.NewString()
	require.NoError(t, os.WriteFile(filepath.Join(dir, active), []byte("still uploading"), 0600))
	require.NoError(t, os.Chtimes(filepath.Join(dir, active), old, old))
	_, err = s.Catalog(ctx, &APIKey{UserID: 1, User: &User{}})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, active))
	require.NoError(t, err)
	release()
	s.freeSpace = func(string) (int64, bool) { return 0, true }
	_, err = s.Catalog(ctx, &APIKey{UserID: 1, User: &User{}})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, active))
	require.True(t, os.IsNotExist(err), "maintenance remains available with a full disk")
}

func TestDesktopDriveRetryReusesPublicationBeforeCommit(t *testing.T) {
	ctx := context.Background()
	s, r := newDriveTest(t)
	drive := createTestDrive(t, s, 1)
	requestID := uuid.NewString()
	original, err := s.Stage(ctx, 1, "retry.txt", strings.NewReader("published"))
	require.NoError(t, err)
	root, err := s.root(1)
	require.NoError(t, err)
	id := desktopDriveFileID(1, requestID, drive, 0)
	require.NoError(t, s.publish(root, id, *original))
	root.Close()
	// No metadata receipt survived the simulated process stop. Reserving a
	// second temporary file is impossible, while reusing the raw object is safe.
	r.users[1].extraBytes = DesktopDriveLimitBytes - int64(len("published"))
	s.freeSpace = func(string) (int64, bool) { return (5 << 30) + 1024, true }
	retry, err := s.StageRetry(ctx, 1, requestID, drive, 0, "retry.txt", strings.NewReader("published"))
	require.NoError(t, err)
	require.Empty(t, retry.temp)
	_, err = s.Mutate(ctx, 1, requestID, "upload", drive, "", DesktopDriveInput{}, []DesktopDriveUpload{*retry})
	require.NoError(t, err, "retry reuses publication with no space for another temporary copy")
	_, err = os.Stat(filepath.Join(s.dataDir, "desktop-drive", "1", id))
	require.NoError(t, err)
}

func TestDesktopDriveRejectsSymlinkStorage(t *testing.T) {
	s, _ := newDriveTest(t)
	outside := t.TempDir()
	err := os.Symlink(outside, filepath.Join(s.dataDir, "desktop-drive"))
	if err != nil {
		t.Skip("symlink creation unavailable on this host")
	}
	_, err = s.Stage(context.Background(), 1, "escape.txt", strings.NewReader("x"))
	require.Error(t, err)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
}
