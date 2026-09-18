//go:build linux

package proxy

import (
	"net"
	"syscall"
	"time"
)

// peerPIDOf 通过 SO_PEERCRED 读取已连接 unix socket 对端（上游进程）的 PID。
func peerPIDOf(conn *net.UnixConn) (int64, bool) {
	// File() 会 dup 底层 fd，关闭返回的 *os.File 不会影响原连接。
	f, err := conn.File()
	if err != nil {
		return 0, false
	}
	defer f.Close()

	cred, err := syscall.GetsockoptUcred(
		int(f.Fd()),
		syscall.SOL_SOCKET,
		syscall.SO_PEERCRED,
	)
	if err != nil {
		return 0, false
	}

	return int64(cred.Pid), true
}

// peerPID 返回监听在 path 上的进程 PID（用于状态展示）：
// listen socket 一般由本代理持有，upstream socket 由飞牛音乐持有。
func peerPID(path string) (int, bool) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return 0, false
	}
	defer conn.Close()

	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, false
	}

	f, err := uc.File()
	if err != nil {
		return 0, false
	}
	defer f.Close()

	cred, err := syscall.GetsockoptUcred(
		int(f.Fd()),
		syscall.SOL_SOCKET,
		syscall.SO_PEERCRED,
	)
	if err != nil {
		return 0, false
	}

	return int(cred.Pid), true
}
