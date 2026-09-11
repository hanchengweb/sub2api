//go:build unix

package service

import "syscall"

// freeDiskBytes 返回该路径所在文件系统的可用字节数。
//
// 用 Bavail 而不是 Bfree：Bfree 含 root 预留块，普通进程写不进去，
// 按它判断会在「还剩 3%」时以为还有空间。
func freeDiskBytes(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}
