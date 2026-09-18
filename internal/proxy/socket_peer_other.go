//go:build !linux

package proxy

import "net"

// 非 Linux 平台（本地开发编译）不依赖 SO_PEERCRED，留空实现，
// 保证跨平台编译通过；实际运行目标 fnOS 为 Linux，走带实现的版本。
func peerPIDOf(conn *net.UnixConn) (int64, bool) { return 0, false }

// 非 Linux 平台无法获取 socket 对端 PID，返回 false。
func peerPID(path string) (int, bool) { return 0, false }
