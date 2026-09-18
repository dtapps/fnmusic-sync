package proxy

import (
	"fmt"
	"os"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

// SocketStatus 描述单个 unix socket 的体检结果。
type SocketStatus struct {
	Path        string `json:"path"`
	Exists      bool   `json:"exists"`
	IsSocket    bool   `json:"is_socket"`
	Perm        string `json:"perm"` // 权限八进制，如 "0666"
	UID         int    `json:"uid"`
	GID         int    `json:"gid"`
	Kind        string `json:"kind"` // none/trim/proxy/stale
	Connectable bool   `json:"connectable"`
	Healthy     bool   `json:"healthy"`
	PeerPID     int    `json:"peer_pid"` // 持有该 socket 的进程 PID（监听=本代理，上游=飞牛音乐）
	Detail      string `json:"detail"`
}

// SocketOverview 两个固定 socket 的体检汇总。
type SocketOverview struct {
	Listen     SocketStatus `json:"listen"`
	Upstream   SocketStatus `json:"upstream"`
	AllHealthy bool         `json:"all_healthy"`
	Message    string       `json:"message"`
}

// ProbeSockets 采集两个固定 socket（监听 / 上游）的状态，
// 并给出"是否正常"的简单判断，供命令行与 Web UI 展示。
//
// 简单判断规则：
//   - 监听 socket：文件存在、是 socket、可连接（nginx 能连上代理/官方）。
//   - 上游 socket：飞牛音乐在线（可连接且归属 trim），这是 scrobble 成功的前提。
func ProbeSockets() SocketOverview {
	listen := inspectSocket(buildinfo.DefaultListenSocket)
	upstream := inspectSocket(buildinfo.DefaultUpstreamSocket)

	listen.Healthy = listen.Exists && listen.IsSocket && listen.Connectable
	// 上游需为飞牛音乐在线（可连接且归属 trim），否则代理无法转发到音乐服务。
	upstream.Healthy = upstream.Exists &&
		upstream.IsSocket &&
		upstream.Connectable &&
		upstream.Kind == string(SocketTrim)

	allHealthy := listen.Healthy && upstream.Healthy
	message := "正常"

	switch {
	case !listen.Exists:
		message = "监听 socket 不存在"
	case !listen.IsSocket:
		message = "监听路径不是 socket 文件"
	case !listen.Connectable:
		message = "监听 socket 不可连接（nginx 将无法连接，表现为 502）"
	case !upstream.Exists:
		message = "上游 socket 不存在（飞牛音乐可能未启动）"
	case !upstream.IsSocket:
		message = "上游路径不是 socket 文件"
	case !upstream.Connectable:
		message = "上游 socket 不可连接（飞牛音乐未就绪）"
	case upstream.Kind != string(SocketTrim):
		message = "上游 socket 不是飞牛音乐（归属=" + upstream.Kind + "）"
	}

	return SocketOverview{
		Listen:     listen,
		Upstream:   upstream,
		AllHealthy: allHealthy,
		Message:    message,
	}
}

// inspectSocket 采集单个 socket 的详细信息。
func inspectSocket(path string) SocketStatus {
	st := SocketStatus{Path: path}

	info, err := os.Stat(path)
	if err != nil {
		st.Detail = "文件不存在"

		return st
	}

	st.Exists = true
	st.IsSocket = info.Mode()&os.ModeSocket != 0
	if !st.IsSocket {
		st.Detail = fmt.Sprintf("不是 socket 文件（类型=%s）", info.Mode().Type())

		return st
	}

	// ProbeSocket 已做传输层探测：文件存在但无人监听时返回 SocketNone。
	kind := ProbeSocket(path)
	st.Kind = string(kind)
	st.Connectable = kind != SocketNone

	if mode, uid, gid, ok := SocketMode(path); ok {
		st.Perm = fmt.Sprintf("%#o", mode&os.ModePerm)
		st.UID = uid
		st.GID = gid
	}

	if !st.Connectable {
		st.Detail = "socket 存在但无进程监听（死文件）"

		return st
	}

	// 记录持有该 socket 的进程 PID，方便在状态页一眼看出
	// 监听是否被本代理接管、上游到底是不是飞牛音乐。
	switch path {
	case buildinfo.DefaultListenSocket:
		// 监听 socket 由本代理自身持有，直接取自身 PID，
		// 避免再连一次自己造成无谓的连接。
		st.PeerPID = os.Getpid()
	default:
		if pid, ok := peerPID(path); ok {
			st.PeerPID = pid
		}
	}

	return st
}
