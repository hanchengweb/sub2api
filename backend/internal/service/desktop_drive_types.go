package service

import (
	"context"
	"encoding/json"
)

const (
	DesktopDriveLimitBytes    int64 = 1 << 30
	DesktopDriveMaxFileBytes  int64 = 50 << 20
	DesktopDriveMaxBatchBytes int64 = 80 << 20
	DesktopDriveMaxItems      int64 = 10000
)

type DesktopDrive struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ItemCount int64  `json:"itemCount"`
	Status    string `json:"status"`
	Scope     string `json:"scope"`
}

type DesktopDriveItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ItemType  string `json:"itemType"`
	FileName  string `json:"fileName"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	ParentID  string `json:"-"`
	SHA256    string `json:"-"`
}

type DesktopDriveRequest struct {
	Fingerprint string
	Result      json.RawMessage
}

// Every store is permanently bound to one user, including all SQL reads.
// WithUser serializes mutations with a transaction-scoped cross-process lock.
type DesktopDriveRepository interface {
	WithUser(ctx context.Context, userID int64, fn func(DesktopDriveStore) error) error
}

type DesktopDriveStore interface {
	Drives(context.Context) ([]DesktopDrive, error)
	Drive(context.Context, string) (*DesktopDrive, error)
	Item(context.Context, string, string) (*DesktopDriveItem, error)
	FileExists(context.Context, string) (bool, error)
	Items(context.Context, string, string, int, int) ([]DesktopDriveItem, int64, error)
	Usage(context.Context) (int64, int64, error)
	CreateDrive(context.Context, DesktopDrive) error
	CreateItem(context.Context, string, DesktopDriveItem) error
	Rename(context.Context, string, string, string) error
	// Delete recursively removes metadata and durably queues owned file UUIDs
	// for cleanup. An empty itemID deletes the complete drive.
	Delete(context.Context, string, string) error
	Request(context.Context, string) (*DesktopDriveRequest, error)
	SaveRequest(context.Context, string, DesktopDriveRequest) error
	Garbage(context.Context) ([]string, error)
	ForgetGarbage(context.Context, string) error
}
