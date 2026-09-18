package main

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// goroutine 快照诊断参数：常态下 dumper 只做 NumGoroutine 原子读，
// 只有越过阈值才抓全量栈并过滤输出，避免周期性全量 dump 刷爆日志。
const (
	// goroutineDumpThreshold 触发全量抓取+过滤输出的 goroutine 数阈值（常态基线约 30 的 2 倍）。
	goroutineDumpThreshold = 60
	// goroutineCheckInterval 周期检查 NumGoroutine 的间隔。
	goroutineCheckInterval = 30 * time.Second
	// goroutineDumpCooldown 两次实际 dump 的最小间隔，防日志风暴。
	goroutineDumpCooldown = 5 * time.Minute
	// goroutineSuspiciousFor 判定"阻塞/空闲可疑"的时长门槛。
	goroutineSuspiciousFor = 5 * time.Minute
)

// goroutineWhitelist 是常驻待命型 goroutine 的栈帧特征子串。
// 它们必然长期阻塞（select/chan receive/IO wait），属于正常现象，直接跳过。
var goroutineWhitelist = []string{
	"lumberjack",                          // 日志轮转
	"os/signal",                           // 信号监听
	"fsnotify",                            // inotify
	"viper.(*Viper).WatchConfig",          // 配置热加载
	"database/sql.(*DB).connectionOpener", // 数据库连接池
	"net/http.(*Server).Serve",            // listener 待命
	"(*UnixListener).Accept",              // listener 待命
	"startGoroutineDumper",                // dumper 自己
	"reqlog.(*Logger).worker",             // 业务常驻 worker
	"proxy.(*CaptureLogger).worker",       // 抓包常驻 worker
	"playback.(*Detector).expireLoop",     // 播放探测过期循环
	"playlist.(*SyncService).run",         // 歌单同步主循环
	"proxy.(*Proxy).Start.func1",          // 代理关闭协程
}

// 出站长连接特征：http.Transport 的 persistConn readLoop/writeLoop、http2 连接协程。
var goroutineOutboundConnMarkers = []string{
	"persistConn",
	"http2.",
}

var (
	goroutineHeaderRe = regexp.MustCompile(`^goroutine (\d+) \[([^\]]*)\]:`)
	elapsedPairRe     = regexp.MustCompile(`(\d+) (nanoseconds?|microseconds?|milliseconds?|seconds?|minutes?|hours?|days?)`)
)

// 时长单位换算表（runtime 状态行输出的是复数全称）。
var elapsedUnitRe = map[string]time.Duration{
	"nanosecond":  time.Nanosecond,
	"microsecond": time.Microsecond,
	"millisecond": time.Millisecond,
	"second":      time.Second,
	"minute":      time.Minute,
	"hour":        time.Hour,
	"day":         24 * time.Hour,
}

// goroutineBlock 是解析出的单个 goroutine 栈块。
type goroutineBlock struct {
	id       string        // goroutine 编号
	state    string        // 状态行方括号内的原文，如 "select, 35 minutes"
	elapsed  time.Duration // 状态行中的阻塞/空闲时长，无则为 0
	text     string        // 完整栈块（含状态行）
	outbound bool          // 是否为出站长连接（persistConn / http2）
}

// parseGoroutineBlocks 以 "goroutine <id> [<state>]:" 为分隔把全量栈切成块，
// 快照头部等非栈行自动忽略；每块文本含状态行。
func parseGoroutineBlocks(raw string) []goroutineBlock {
	var (
		blocks []goroutineBlock
		cur    = -1
	)

	for line := range strings.SplitSeq(raw, "\n") {
		if m := goroutineHeaderRe.FindStringSubmatch(line); m != nil {
			blocks = append(blocks, goroutineBlock{
				id:      m[1],
				state:   m[2],
				elapsed: parseElapsed(m[2]),
				text:    line + "\n",
			})
			cur = len(blocks) - 1

			continue
		}

		if cur >= 0 {
			blocks[cur].text += line + "\n"
		}
	}

	return blocks
}

// parseElapsed 从状态行方括号内容解析时长，如 "select, 35 minutes" → 35m。
// runtime 输出可能为多段组合（"2 days 3 hours"）或 "about 3 hours"。
func parseElapsed(state string) time.Duration {
	_, dur, found := strings.Cut(state, ",")
	if !found {
		return 0
	}

	dur = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(dur), "about"))

	var total time.Duration
	for _, m := range elapsedPairRe.FindAllStringSubmatch(dur, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}

		unit := strings.TrimSuffix(m[2], "s")
		if u, ok := elapsedUnitRe[unit]; ok {
			total += time.Duration(n) * u
		}
	}

	return total
}

// isWhitelistedBlock 判断栈块是否命中白名单（对整块文本做子串匹配）。
func isWhitelistedBlock(text string) bool {
	for _, w := range goroutineWhitelist {
		if strings.Contains(text, w) {
			return true
		}
	}

	return false
}

// isOutboundConnBlock 判断栈块是否为出站长连接（persistConn / http2 帧）。
func isOutboundConnBlock(text string) bool {
	for _, m := range goroutineOutboundConnMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}

	return false
}

// filterSuspiciousGoroutines 对全量栈做白名单过滤与可疑判定：
//   - 命中白名单 → 跳过；
//   - 出站长连接（persistConn/http2）空闲 ≥ 5 分钟 → 可疑；
//   - 其余非白名单 goroutine 阻塞 ≥ 5 分钟 → 可疑。
func filterSuspiciousGoroutines(raw string) []goroutineBlock {
	var suspicious []goroutineBlock

	for _, b := range parseGoroutineBlocks(raw) {
		if isWhitelistedBlock(b.text) {
			continue
		}

		if b.elapsed >= goroutineSuspiciousFor {
			b.outbound = isOutboundConnBlock(b.text)
			suspicious = append(suspicious, b)
		}
	}

	return suspicious
}

// reportSuspiciousGoroutines 以 WARN 输出可疑清单：先一行汇总，再逐条状态行+完整堆栈。
func reportSuspiciousGoroutines(logger *slog.Logger, raw string, total int) {
	suspicious := filterSuspiciousGoroutines(raw)

	logger.Warn("goroutine 达到阈值，输出可疑清单",
		"goroutines", total,
		"threshold", goroutineDumpThreshold,
		"suspicious", len(suspicious),
	)

	for _, b := range suspicious {
		kind := "阻塞"
		if b.outbound {
			kind = "出站长连接空闲"
		}

		logger.Warn(fmt.Sprintf("可疑 goroutine %s [%s]（%s %v）", b.id, b.state, kind, b.elapsed),
			"stack", strings.TrimRight(b.text, "\n"),
		)
	}
}
