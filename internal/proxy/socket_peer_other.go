//go:build !linux

package proxy

// 非 Linux 平台无法获取 socket 对端 PID，返回 false。
func peerPID(path string) (int, bool) { return 0, false }
