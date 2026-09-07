package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"cnb.cool/dtapp/fnmusic-sync/internal/proxy"
)

// check 只做体检：socket 归属、权限、upstream 连通性，不接管、不启动代理。
//
// 输出分两路，缺一不可：
//   - 对齐的表格用 fmt.Printf 直出终端——这是给人看的一次性报告，不该混进日志；
//   - 结论用 logger 记录——随运行日志落盘，便于事后排查。
func check(
	ctx context.Context,
	listen, upstream string,
	p *proxy.Proxy,
	logger *slog.Logger,
) error {
	logger.Info("开始体检", "监听", listen, "上游", upstream)

	listenKind := proxy.ProbeSocket(listen)
	upstreamKind := proxy.ProbeSocket(upstream)

	fmt.Printf("监听   %-45s 归属=%s\n", listen, listenKind)
	fmt.Printf("上游   %-45s 归属=%s\n", upstream, upstreamKind)

	for _, path := range []string{listen, upstream} {
		mode, uid, gid, ok := proxy.SocketMode(path)
		if !ok {
			fmt.Printf("权限   %-45s (不存在)\n", path)

			continue
		}

		fmt.Printf("权限   %-45s %#o 属主=%s:%s\n",
			path, mode&os.ModePerm, num(uid), num(gid))

		if mode&0o007 == 0 {
			fmt.Printf("警告   %-45s 权限不允许 other 访问，"+
				"nginx(www-data) 将无法连接，会表现为 502\n", path)

			logger.Warn("socket 权限不允许 other 访问，nginx(www-data) 会 502", "路径", path)
		}
	}

	// 体检时官方后端可能在 listen 路径（未接管）也可能在 upstream（已接管），
	// 探测它实际所在的那个 socket。
	trimPath := upstream

	switch {
	case upstreamKind == proxy.SocketTrim:
		trimPath = upstream
	case listenKind == proxy.SocketTrim:
		trimPath = listen
		fmt.Println("提示：尚未接管，飞牛音乐当前为官方直连")
	}

	if err := p.CheckSocket(ctx, trimPath); err != nil {
		fmt.Printf("官方后端连通性(%s)：失败 (%v)\n", trimPath, err)

		logger.Error("官方后端连通性检查失败", "路径", trimPath, "错误", err)

		return fmt.Errorf("上游检查失败")
	}

	fmt.Printf("官方后端连通性(%s)：正常\n", trimPath)

	logger.Info("体检完成", "官方后端", trimPath, "结果", "正常")

	if upstreamKind != proxy.SocketTrim && listenKind != proxy.SocketTrim {
		fmt.Println("警告：两个 socket 上都没有探测到官方 trim-music")

		logger.Warn("两个 socket 上都没有探测到官方 trim-music")
	}

	return nil
}

func num(value int) string {
	if value < 0 {
		return "-"
	}

	return strconv.Itoa(value)
}
