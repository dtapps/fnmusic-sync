package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// loadSnapshots 读取真实 goroutine-dump.txt 并按快照头切块；文件不存在（未入库）时跳过。
func loadSnapshots(t *testing.T) []string {
	t.Helper()

	const path = "../../logs/3/goroutine-dump.txt"

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("快照文件不存在，跳过验收: %v", err)
	}

	parts := regexp.MustCompile(`(?m)^===== goroutine 快照`).Split(string(raw), -1)

	var snaps []string
	for _, p := range parts {
		if strings.Contains(p, "\ngoroutine ") || strings.HasPrefix(p, "goroutine ") {
			snaps = append(snaps, p)
		}
	}

	if len(snaps) == 0 {
		t.Fatal("未从文件解析出任何快照")
	}

	return snaps
}

func TestParseElapsed(t *testing.T) {
	cases := map[string]time.Duration{
		"select":                         0,
		"IO wait":                        0,
		"running":                        0,
		"select, 35 minutes":             35 * time.Minute,
		"IO wait, 1 minutes":             time.Minute,
		"sync.WaitGroup.Wait, 5 minutes": 5 * time.Minute,
		"chan receive, 2 days":           48 * time.Hour,
		"select, 2 days 3 hours":         51 * time.Hour,
		"chan receive, about 3 hours":    3 * time.Hour,
		"select, 45 seconds":             45 * time.Second,
		"syscall":                        0,
	}

	for state, want := range cases {
		if got := parseElapsed(state); got != want {
			t.Errorf("parseElapsed(%q) = %v, want %v", state, got, want)
		}
	}
}

// TestFilterRules 验证过滤规则本身的不变量：白名单必被跳过、入选者时长必 ≥5 分钟。
func TestFilterRules(t *testing.T) {
	raw := strings.Join([]string{
		"goroutine 1 [IO wait, 35 minutes]:\nnet/http.(*Server).Serve(...)\n\n",
		"goroutine 2 [select, 35 minutes]:\ncnb.cool/x/internal/reqlog.(*Logger).worker(...)\n\n",
		"goroutine 3 [select, 35 minutes]:\ngopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun(...)\n\n",
		"goroutine 4 [IO wait, 35 minutes]:\nnet/http.(*persistConn).readLoop(...)\n\n",
		"goroutine 5 [select, 4 minutes]:\nsome.(*Idle).wait(...)\n\n",
		"goroutine 6 [sync.WaitGroup.Wait, 5 minutes]:\nsomeother.(*Thing).loop(...)\n\n",
	}, "")

	got := filterSuspiciousGoroutines(raw)
	if len(got) != 2 || got[0].id != "4" || got[1].id != "6" {
		ids := make([]string, len(got))
		for i, b := range got {
			ids[i] = b.id
		}
		t.Fatalf("期望可疑 [4 6]，实际 %v", ids)
	}

	if !got[0].outbound || got[1].outbound {
		t.Errorf("outbound 标记错误: 4=%v 6=%v", got[0].outbound, got[1].outbound)
	}
}

// TestAcceptanceOnRealSnapshots 验收：
//  1. 对每个真实快照跑过滤，输出不得含任何白名单/短时长 goroutine；
//  2. 1588 达到 35 分钟的那次快照，四条出站长连接（1588/1589/1740/1741）必须全部命中。
func TestAcceptanceOnRealSnapshots(t *testing.T) {
	snaps := loadSnapshots(t)

	var hit35 bool

	for _, snap := range snaps {
		suspicious := filterSuspiciousGoroutines(snap)

		for _, b := range suspicious {
			if isWhitelistedBlock(b.text) {
				t.Errorf("白名单 goroutine %s 被误报", b.id)
			}

			if b.elapsed < goroutineSuspiciousFor {
				t.Errorf("goroutine %s 时长 %v < 5 分钟被误报", b.id, b.elapsed)
			}
		}

		// 找到 1588 恰好空闲 35 分钟的快照，校验四条目标连接全部命中。
		if !hit35 && strings.Contains(snap, "goroutine 1588 [IO wait, 35 minutes]") {
			hit35 = true

			got := map[string]bool{}
			for _, b := range suspicious {
				got[b.id] = true
			}

			for _, id := range []string{"1588", "1589", "1740", "1741"} {
				if !got[id] {
					t.Errorf("35 分钟快照中目标空闲连接 %s 未被报出", id)
				}
			}

			t.Logf("35 分钟快照共报出 %d 条: %v", len(got), keysOf(got))
		}
	}

	if !hit35 {
		t.Log("快照中不存在 1588=35 分钟的样本，跳过定点校验")
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}

	return out
}
