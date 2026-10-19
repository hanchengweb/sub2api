package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"golang.org/x/net/html"
)

var ErrDesktopDriveNotFound = infraerrors.NotFound("DRIVE_NOT_FOUND", "云盘或文件不存在")

type DesktopDriveService struct {
	repo          DesktopDriveRepository
	dataDir       string
	freeSpace     func(string) (int64, bool)
	uploadMu      sync.Mutex
	uploadUsers   map[int64]bool
	reservedBytes int64
	scanAfter     map[int64]string
}

func NewDesktopDriveService(repo DesktopDriveRepository, dataDir string) *DesktopDriveService {
	return &DesktopDriveService{repo: repo, dataDir: dataDir, freeSpace: freeDiskBytes, uploadUsers: map[int64]bool{}, scanAfter: map[int64]string{}}
}

// The API key is a credential, never the data owner. New devices and rotated
// personal keys resolve to the same UserID. Organization service keys cannot.
func ValidateDesktopDriveKey(key *APIKey) error {
	if err := ValidateDesktopRechargeKey(key); err != nil {
		return err
	}
	if key.UserID <= 0 || key.User.ID != key.UserID || key.GroupID == nil || *key.GroupID != 4 || key.Group == nil || key.Group.ID != 4 {
		return infraerrors.Forbidden("DRIVE_PERSONAL_ONLY", "请使用个人账号连接访问个人云盘")
	}
	return nil
}

func desktopDriveUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func desktopDriveName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 160 || !utf8.ValidString(value) || value == "." || value == ".." {
		return "", infraerrors.BadRequest("DRIVE_NAME_INVALID", "名称应为1到160个字符")
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return "", infraerrors.BadRequest("DRIVE_NAME_INVALID", "名称不能包含路径或控制字符")
		}
	}
	return value, nil
}

func (s *DesktopDriveService) Catalog(ctx context.Context, key *APIKey) (map[string]any, error) {
	s.maintain(ctx, key.UserID, false)
	var drives []DesktopDrive
	var used int64
	err := s.repo.WithUser(ctx, key.UserID, func(tx DesktopDriveStore) error {
		var err error
		drives, err = tx.Drives(ctx)
		if err != nil {
			return err
		}
		used, _, err = tx.Usage(ctx)
		return err
	})
	if drives == nil {
		drives = []DesktopDrive{}
	}
	return map[string]any{"account": map[string]any{"reference": fmt.Sprintf("WH-%d", key.UserID), "nickname": key.User.Username}, "resourceFolders": drives,
		"quota": map[string]any{"usedBytes": used, "limitBytes": DesktopDriveLimitBytes, "maxFileBytes": DesktopDriveMaxFileBytes}}, err
}

func driveOwned(ctx context.Context, tx DesktopDriveStore, driveID, parentID string) (*DesktopDrive, error) {
	if !desktopDriveUUID(driveID) || parentID != "" && !desktopDriveUUID(parentID) {
		return nil, ErrDesktopDriveNotFound
	}
	drive, err := tx.Drive(ctx, driveID)
	if err != nil {
		return nil, err
	}
	if drive == nil {
		return nil, ErrDesktopDriveNotFound
	}
	if parentID != "" {
		parent, err := tx.Item(ctx, driveID, parentID)
		if err != nil {
			return nil, err
		}
		if parent == nil || parent.ItemType != "folder" {
			return nil, ErrDesktopDriveNotFound
		}
	}
	return drive, nil
}

func (s *DesktopDriveService) List(ctx context.Context, userID int64, driveID, parentID string, offset, limit int) (map[string]any, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return nil, infraerrors.BadRequest("DRIVE_PAGINATION", "分页参数无效")
	}
	var result map[string]any
	err := s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error {
		drive, err := driveOwned(ctx, tx, driveID, parentID)
		if err != nil {
			return err
		}
		items, total, err := tx.Items(ctx, driveID, parentID, offset, limit)
		if items == nil {
			items = []DesktopDriveItem{}
		}
		result = map[string]any{"id": drive.ID, "name": drive.Name, "items": items, "total": total}
		return err
	})
	return result, err
}

func (s *DesktopDriveService) item(ctx context.Context, userID int64, driveID, itemID string) (*DesktopDriveItem, error) {
	if !desktopDriveUUID(driveID) || !desktopDriveUUID(itemID) {
		return nil, ErrDesktopDriveNotFound
	}
	var item *DesktopDriveItem
	err := s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error { var err error; item, err = tx.Item(ctx, driveID, itemID); return err })
	if err == nil && item == nil {
		err = ErrDesktopDriveNotFound
	}
	return item, err
}

func (s *DesktopDriveService) root(userID int64) (*os.Root, error) {
	if userID <= 0 || strings.TrimSpace(s.dataDir) == "" {
		return nil, errors.New("drive data directory unavailable")
	}
	data, err := os.OpenRoot(s.dataDir)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	if err = data.Mkdir("desktop-drive", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if st, err := data.Lstat("desktop-drive"); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe drive storage directory")
	}
	if err = syncDesktopDriveDirectory(data); err != nil {
		return nil, err
	}
	base, err := data.OpenRoot("desktop-drive")
	if err != nil {
		return nil, err
	}
	defer base.Close()
	user := strconv.FormatInt(userID, 10)
	if err = base.Mkdir(user, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if st, err := base.Lstat(user); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe user storage directory")
	}
	if err = syncDesktopDriveDirectory(base); err != nil {
		return nil, err
	}
	return base.OpenRoot(user)
}

func (s *DesktopDriveService) writable(extra int64) error {
	s.uploadMu.Lock()
	reserved := s.reservedBytes
	s.uploadMu.Unlock()
	if available, ok := s.freeSpace(s.dataDir); ok && available-extra-reserved < 5<<30 {
		return infraerrors.ServiceUnavailable("DRIVE_STORAGE_FULL", "云盘存储暂时不足，请稍后重试")
	}
	return nil
}

// The current deployment has one application instance on the persistent mount.
// Bound scratch space before parsing multipart bodies: one request per user,
// four requests globally, with worst-case disk reservation for each active file.
func (s *DesktopDriveService) BeginUpload(ctx context.Context, userID int64) (func(), error) {
	s.uploadMu.Lock()
	if s.uploadUsers[userID] || len(s.uploadUsers) >= 4 {
		s.uploadMu.Unlock()
		return nil, infraerrors.New(429, "DRIVE_UPLOAD_BUSY", "上传正在进行，请稍后重试")
	}
	s.uploadUsers[userID] = true
	s.uploadMu.Unlock()
	s.maintain(ctx, userID, true)
	var once sync.Once
	return func() { once.Do(func() { s.uploadMu.Lock(); delete(s.uploadUsers, userID); s.uploadMu.Unlock() }) }, nil
}

type DesktopDriveUpload struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	temp      string
}

func (s *DesktopDriveService) Stage(ctx context.Context, userID int64, name string, reader io.Reader) (*DesktopDriveUpload, error) {
	return s.stage(ctx, userID, name, reader, false)
}

func (s *DesktopDriveService) UploadCommitted(ctx context.Context, userID int64, requestID string) (bool, error) {
	var committed bool
	err := s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error {
		prior, err := tx.Request(ctx, requestID)
		committed = prior != nil
		return err
	})
	return committed, err
}

// A confirmed receipt can be replayed at full capacity: hash the bounded input
// without writing another temporary copy, then compare its permanent fingerprint.
func (s *DesktopDriveService) StageReplay(ctx context.Context, userID int64, name string, reader io.Reader) (*DesktopDriveUpload, error) {
	return s.stage(ctx, userID, name, reader, true)
}

// A process can stop after atomic publication but before SQL commit. The file
// UUID is stable for the request, so retry hashes its body and reuses that
// object instead of requiring space for a duplicate temporary copy.
func (s *DesktopDriveService) StageRetry(ctx context.Context, userID int64, requestID, driveID string, index int, name string, reader io.Reader) (*DesktopDriveUpload, error) {
	root, err := s.root(userID)
	if err != nil {
		return nil, err
	}
	st, statErr := root.Lstat(desktopDriveFileID(userID, requestID, driveID, index))
	root.Close()
	if statErr == nil && st.Mode().IsRegular() {
		return s.StageReplay(ctx, userID, name, reader)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	return s.Stage(ctx, userID, name, reader)
}

func (s *DesktopDriveService) stage(ctx context.Context, userID int64, name string, reader io.Reader, replay bool) (*DesktopDriveUpload, error) {
	name, err := desktopDriveName(name)
	if err != nil {
		return nil, err
	}
	if replay {
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(reader, DesktopDriveMaxFileBytes+1))
		if err != nil {
			return nil, err
		}
		if n > DesktopDriveMaxFileBytes {
			return nil, infraerrors.BadRequest("DRIVE_FILE_TOO_LARGE", "单个文件不能超过50MiB")
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return &DesktopDriveUpload{Name: name, MediaType: mediaType, SizeBytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
	}
	s.uploadMu.Lock()
	if available, ok := s.freeSpace(s.dataDir); ok && available-s.reservedBytes-DesktopDriveMaxFileBytes < 5<<30 {
		s.uploadMu.Unlock()
		return nil, infraerrors.ServiceUnavailable("DRIVE_STORAGE_FULL", "云盘存储暂时不足，请稍后重试")
	}
	s.reservedBytes += DesktopDriveMaxFileBytes
	s.uploadMu.Unlock()
	defer func() { s.uploadMu.Lock(); s.reservedBytes -= DesktopDriveMaxFileBytes; s.uploadMu.Unlock() }()
	root, err := s.root(userID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	maxBytes := DesktopDriveMaxFileBytes
	err = s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error {
		used, count, err := tx.Usage(ctx)
		if err != nil {
			return err
		}
		dir, err := root.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(10001)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if len(entries) >= 10000 || count >= DesktopDriveMaxItems {
			return driveQuotaError()
		}
		var physical int64
		for _, entry := range entries {
			st, err := root.Lstat(entry.Name())
			if err != nil {
				return err
			}
			if st.Mode().IsRegular() {
				physical += st.Size()
			}
		}
		if physical > used {
			used = physical
		}
		if remaining := DesktopDriveLimitBytes - used; remaining < maxBytes {
			maxBytes = remaining
		}
		if maxBytes < 0 {
			return driveQuotaError()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	temp := ".upload-" + uuid.NewString()
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			_ = root.Remove(temp)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if n > DesktopDriveMaxFileBytes {
		return nil, infraerrors.BadRequest("DRIVE_FILE_TOO_LARGE", "单个文件不能超过50MiB")
	}
	if n > maxBytes {
		return nil, driveQuotaError()
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	keep = true
	return &DesktopDriveUpload{Name: name, MediaType: mediaType, SizeBytes: n, SHA256: hex.EncodeToString(hash.Sum(nil)), temp: temp}, nil
}

func (s *DesktopDriveService) DropUploads(userID int64, uploads []DesktopDriveUpload) {
	root, err := s.root(userID)
	if err != nil {
		return
	}
	defer root.Close()
	for _, upload := range uploads {
		if strings.HasPrefix(upload.temp, ".upload-") && !strings.ContainsAny(upload.temp, `/\\`) {
			_ = root.Remove(upload.temp)
		}
	}
}

func (s *DesktopDriveService) publish(root *os.Root, id string, upload DesktopDriveUpload) error {
	if !desktopDriveUUID(id) {
		return errors.New("invalid staged file")
	}
	if st, err := root.Lstat(id); err == nil {
		if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Size() != upload.SizeBytes {
			return ErrIdempotencyKeyConflict
		}
		file, err := root.Open(id)
		if err != nil {
			return err
		}
		defer file.Close()
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != upload.SHA256 {
			return ErrIdempotencyKeyConflict
		}
		return syncDesktopDriveDirectory(root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if upload.temp == "" {
		return errors.New("staged file is unavailable; retry the same request")
	}
	if err := root.Rename(upload.temp, id); err != nil {
		return err
	}
	return syncDesktopDriveDirectory(root)
}

type DesktopDriveInput struct {
	Name            string `json:"name"`
	ParentID        string `json:"parentId,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
}

func desktopDriveMutationFingerprint(userID int64, operation, driveID, itemID string, input DesktopDriveInput, uploads []DesktopDriveUpload) (string, error) {
	payload := struct {
		Input DesktopDriveInput
		Files []DesktopDriveUpload
	}{input, uploads}
	return BuildIdempotencyFingerprint(operation, driveID+"/"+itemID, fmt.Sprintf("user:%d", userID), payload)
}

// The generic coordinator stores only a tiny acknowledgement. File names and
// large batches must replay losslessly from this permanent owner-bound receipt,
// without the generic log-redaction or response-size truncation policy.
func (s *DesktopDriveService) MutationResult(ctx context.Context, userID int64, requestID, operation, driveID, itemID string, input DesktopDriveInput, uploads []DesktopDriveUpload) (json.RawMessage, error) {
	fingerprint, err := desktopDriveMutationFingerprint(userID, operation, driveID, itemID, input, uploads)
	if err != nil {
		return nil, err
	}
	var result json.RawMessage
	err = s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error {
		request, err := tx.Request(ctx, requestID)
		if err != nil {
			return err
		}
		if request == nil {
			return infraerrors.ServiceUnavailable("DRIVE_REQUEST_UNKNOWN", "云盘操作结果尚未确认，请使用原请求标识重试")
		}
		if request.Fingerprint != fingerprint {
			return ErrIdempotencyKeyConflict
		}
		result = request.Result
		return nil
	})
	return result, err
}

func (s *DesktopDriveService) Mutate(ctx context.Context, userID int64, requestID, operation, driveID, itemID string, input DesktopDriveInput, uploads []DesktopDriveUpload) (json.RawMessage, error) {
	if !desktopDriveUUID(requestID) {
		return nil, ErrIdempotencyKeyInvalid
	}
	if input.ClientRequestID != "" && input.ClientRequestID != requestID {
		return nil, ErrIdempotencyKeyConflict
	}
	if operation != "create" && (!desktopDriveUUID(driveID) || itemID != "" && !desktopDriveUUID(itemID)) {
		return nil, ErrDesktopDriveNotFound
	}
	s.maintain(ctx, userID, false)
	fingerprint, err := desktopDriveMutationFingerprint(userID, operation, driveID, itemID, input, uploads)
	if err != nil {
		return nil, err
	}
	var result json.RawMessage
	err = s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error {
		prior, err := tx.Request(ctx, requestID)
		if err != nil {
			return err
		}
		if prior != nil {
			if prior.Fingerprint != fingerprint {
				return ErrIdempotencyKeyConflict
			}
			result = prior.Result
			return nil
		}
		if operation != "delete" {
			if err = s.writable(0); err != nil {
				return err
			}
		}
		var data any
		if operation == "create" {
			name, err := desktopDriveName(input.Name)
			if err != nil {
				return err
			}
			_, count, err := tx.Usage(ctx)
			if err != nil {
				return err
			}
			if count >= DesktopDriveMaxItems {
				return driveQuotaError()
			}
			drive := DesktopDrive{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("drive:%d:%s", userID, requestID))).String(), Name: name, Status: "ready", Scope: "private"}
			if err = tx.CreateDrive(ctx, drive); err != nil {
				return err
			}
			data = drive
		} else {
			if _, err = driveOwned(ctx, tx, driveID, input.ParentID); err != nil {
				return err
			}
			if itemID != "" {
				item, err := tx.Item(ctx, driveID, itemID)
				if err != nil {
					return err
				}
				if item == nil {
					return ErrDesktopDriveNotFound
				}
			}
			switch operation {
			case "folder", "upload":
				used, count, err := tx.Usage(ctx)
				if err != nil {
					return err
				}
				needed := int64(1)
				if operation == "upload" {
					needed = int64(len(uploads))
				}
				if needed == 0 || needed > 100 || count+needed > DesktopDriveMaxItems {
					return driveQuotaError()
				}
				var bytes int64
				for _, file := range uploads {
					if file.SizeBytes < 0 || file.SizeBytes > DesktopDriveMaxFileBytes {
						return driveQuotaError()
					}
					bytes += file.SizeBytes
				}
				if bytes > DesktopDriveMaxBatchBytes || used+bytes > DesktopDriveLimitBytes {
					return driveQuotaError()
				}
				if err = s.writable(bytes); err != nil {
					return err
				}
				items := []DesktopDriveItem{}
				if operation == "folder" {
					name, err := desktopDriveName(input.Name)
					if err != nil {
						return err
					}
					item := DesktopDriveItem{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("folder:%d:%s", userID, requestID))).String(), Name: name, ItemType: "folder", ParentID: input.ParentID}
					if err = tx.CreateItem(ctx, driveID, item); err != nil {
						return err
					}
					data = item
				} else {
					root, err := s.root(userID)
					if err != nil {
						return err
					}
					defer root.Close()
					for index, file := range uploads {
						id := desktopDriveFileID(userID, requestID, driveID, index)
						if err = s.publish(root, id, file); err != nil {
							return err
						}
						item := DesktopDriveItem{ID: id, Name: file.Name, ItemType: "file", FileName: file.Name, MediaType: file.MediaType, SizeBytes: file.SizeBytes, ParentID: input.ParentID, SHA256: file.SHA256}
						if err = tx.CreateItem(ctx, driveID, item); err != nil {
							return err
						}
						items = append(items, item)
					}
					data = map[string]any{"items": items, "total": len(items)}
				}
			case "rename":
				name, err := desktopDriveName(input.Name)
				if err != nil {
					return err
				}
				if err = tx.Rename(ctx, driveID, itemID, name); err != nil {
					return err
				}
				data = map[string]any{"id": driveID, "name": name}
				if itemID != "" {
					data = map[string]any{"id": itemID, "name": name}
				}
			case "delete":
				if err = tx.Delete(ctx, driveID, itemID); err != nil {
					return err
				}
				data = map[string]any{"deleted": true}
			default:
				return infraerrors.BadRequest("DRIVE_OPERATION_INVALID", "云盘操作无效")
			}
		}
		result, err = json.Marshal(data)
		if err != nil {
			return err
		}
		return tx.SaveRequest(ctx, requestID, DesktopDriveRequest{Fingerprint: fingerprint, Result: result})
	})
	if err == nil {
		s.maintain(ctx, userID, false)
	} else if operation == "upload" {
		// A commit acknowledgement can be lost. Reconcile under the same user
		// lock before removing published files, using a fresh context if the
		// uploading client disconnected. A DB outage preserves files for retry.
		reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.repo.WithUser(reconcileCtx, userID, func(tx DesktopDriveStore) error {
			root, rootErr := s.root(userID)
			if rootErr != nil {
				return rootErr
			}
			defer root.Close()
			for index := range uploads {
				id := desktopDriveFileID(userID, requestID, driveID, index)
				item, checkErr := tx.Item(reconcileCtx, driveID, id)
				if checkErr != nil {
					return checkErr
				}
				if item == nil {
					if st, statErr := root.Lstat(id); statErr == nil && st.Mode().IsRegular() {
						_ = root.Remove(id)
					}
				}
			}
			return nil
		})
	}
	return result, err
}

func desktopDriveFileID(userID int64, requestID, driveID string, index int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("file:%d:%s:%s:%d", userID, requestID, driveID, index))).String()
}

func driveQuotaError() error {
	return infraerrors.Conflict("DRIVE_QUOTA_EXCEEDED", "个人云盘容量或文件数量已达上限")
}

func (s *DesktopDriveService) maintain(ctx context.Context, userID int64, admittedUpload bool) {
	ctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	// This outbox is committed with deletion. Failed filesystem cleanup is
	// retried by subsequent mutations; no unconfirmed object is ever removed.
	_ = s.repo.WithUser(ctx, userID, func(tx DesktopDriveStore) error {
		deadline := time.Now().Add(time.Second)
		root, err := s.root(userID)
		if err != nil {
			return err
		}
		defer root.Close()
		for processed := 0; processed < 10000 && time.Now().Before(deadline); {
			ids, err := tx.Garbage(ctx)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			removed := 0
			for _, id := range ids {
				if !time.Now().Before(deadline) {
					break
				}
				processed++
				if !desktopDriveUUID(id) {
					continue
				}
				if st, err := root.Lstat(id); err == nil && !st.Mode().IsRegular() {
					continue
				}
				if err = root.Remove(id); err != nil && !errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err = tx.ForgetGarbage(ctx, id); err != nil {
					return err
				}
				removed++
			}
			if removed == 0 {
				break
			}
		}
		s.uploadMu.Lock()
		active := s.uploadUsers[userID]
		s.uploadMu.Unlock()
		if active && !admittedUpload {
			return nil
		}
		dir, err := root.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(10000)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		s.uploadMu.Lock()
		after := s.scanAfter[userID]
		s.uploadMu.Unlock()
		start := sort.Search(len(entries), func(i int) bool { return entries[i].Name() > after })
		if start == len(entries) {
			start = 0
		}
		next := after
		defer func() { s.uploadMu.Lock(); s.scanAfter[userID] = next; s.uploadMu.Unlock() }()
		for index := range entries {
			if !time.Now().Before(deadline) {
				break
			}
			entry := entries[(start+index)%len(entries)]
			next = entry.Name()
			name := entry.Name()
			temporary := strings.HasPrefix(name, ".upload-") && desktopDriveUUID(strings.TrimPrefix(name, ".upload-"))
			if !temporary && !desktopDriveUUID(name) {
				continue
			}
			st, err := root.Lstat(name)
			if err != nil || !st.Mode().IsRegular() || time.Since(st.ModTime()) < time.Hour {
				continue
			}
			if !temporary {
				exists, err := tx.FileExists(ctx, name)
				if err != nil {
					return err
				}
				if exists {
					continue
				}
			}
			_ = root.Remove(name)
		}
		return nil
	})
}

func (s *DesktopDriveService) Original(ctx context.Context, userID int64, driveID, itemID string) (*DesktopDriveItem, *os.File, error) {
	item, err := s.item(ctx, userID, driveID, itemID)
	if err != nil {
		return nil, nil, err
	}
	if item.ItemType != "file" {
		return nil, nil, ErrDesktopDriveNotFound
	}
	root, err := s.root(userID)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	st, err := root.Lstat(item.ID)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrDesktopDriveNotFound
	}
	f, err := root.Open(item.ID)
	if err != nil {
		return nil, nil, err
	}
	return item, f, nil
}

func (s *DesktopDriveService) Content(ctx context.Context, userID int64, driveID, itemID string, offset, limit int) (map[string]any, error) {
	if offset < 0 || limit < 1 || limit > 200000 {
		return nil, infraerrors.BadRequest("DRIVE_PAGINATION", "分页参数无效")
	}
	item, err := s.item(ctx, userID, driveID, itemID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"id": item.ID, "name": item.Name, "itemType": item.ItemType, "fileName": item.FileName, "mediaType": item.MediaType, "sizeBytes": item.SizeBytes, "content": "", "total": 0, "offset": offset, "truncated": false}
	if item.ItemType != "file" {
		result["contentUnavailable"] = true
		return result, nil
	}
	ext := strings.ToLower(filepath.Ext(item.FileName))
	textMedia := strings.HasPrefix(item.MediaType, "text/") || item.MediaType == "application/json"
	if !textMedia || !strings.Contains("|.txt|.md|.csv|.json|.html|.htm|", "|"+ext+"|") || ext == "" || item.SizeBytes > 2<<20 {
		result["contentUnavailable"] = true
		return result, nil
	}
	_, f, err := s.Original(ctx, userID, driveID, itemID)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	bytes, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(bytes) > 2<<20 || !utf8.Valid(bytes) || strings.IndexByte(string(bytes), 0) >= 0 {
		result["contentUnavailable"] = true
		return result, nil
	}
	text := string(bytes)
	if ext == ".html" || ext == ".htm" {
		text = desktopDriveHTMLText(text)
	}
	runes := []rune(text)
	total := len(runes)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	result["content"] = string(runes[offset:end])
	result["total"] = total
	result["offset"] = offset
	result["truncated"] = end < total
	return result, nil
}

func desktopDriveHTMLText(input string) string {
	tokens := html.NewTokenizer(strings.NewReader(input))
	var out strings.Builder
	hidden := 0
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			break
		}
		token := tokens.Token()
		if kind == html.StartTagToken && (token.Data == "script" || token.Data == "style" || token.Data == "noscript") {
			hidden++
		}
		if kind == html.TextToken && hidden == 0 {
			out.WriteString(token.Data)
			out.WriteByte('\n')
		}
		if kind == html.EndTagToken && (token.Data == "script" || token.Data == "style" || token.Data == "noscript") && hidden > 0 {
			hidden--
		}
	}
	return strings.TrimSpace(out.String())
}
