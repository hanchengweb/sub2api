//go:build unix

package service

import "os"

// File fsync precedes atomic rename; directory fsync precedes the SQL commit.
// This also persists newly created storage directories on the Linux deployment.
func syncDesktopDriveDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
