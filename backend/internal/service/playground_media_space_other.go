//go:build !unix

package service

// freeDiskBytes 非 unix 平台不做磁盘探测。
//
// 生产跑在 Linux 容器里，这个分支只为本机开发编译得过而存在；
// 返回 false 表示「探测不到」，调用方据此放行而不是拦截。
func freeDiskBytes(string) (int64, bool) { return 0, false }
