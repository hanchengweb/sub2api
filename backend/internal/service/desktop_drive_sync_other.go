//go:build !unix

package service

import "os"

// Windows development checks atomic rename and file Sync. Production storage
// durability additionally uses the Unix directory fsync implementation.
func syncDesktopDriveDirectory(*os.Root) error { return nil }
