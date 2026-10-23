package strutil

import (
	"strings"
	"testing"
)

func TestFirstN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{name: "短于 n 原样返回", s: "abc", n: 8, want: "abc"},
		{name: "等于 n 原样返回", s: "12345678", n: 8, want: "12345678"},
		{name: "长于 n 截断", s: "123456789", n: 8, want: "12345678"},
		{name: "空串", s: "", n: 8, want: ""},
		{name: "n 为 0 返回空", s: "abc", n: 0, want: ""},
		// 按字节截断：多字节字符可能被切开，这正是当前实现的既定行为
		// （token 等 ASCII 场景使用，不需要按 rune 对齐）。
		{name: "多字节按字节截断", s: "中文", n: 1, want: "\xe4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FirstN(tt.s, tt.n); got != tt.want {
				t.Errorf("FirstN(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func TestFirstN8(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 32)
	if got := FirstN8(long); got != long[:8] {
		t.Errorf("FirstN8(32×a) = %q, want 8 个 a", got)
	}

	if got := FirstN8("short"); got != "short" {
		t.Errorf("FirstN8(short) = %q, want short", got)
	}
}

func TestRuneWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    rune
		want int
	}{
		{name: "ASCII 半角", r: 'a', want: 1},
		{name: "数字", r: '7', want: 1},
		{name: "空格", r: ' ', want: 1},
		{name: "NUL", r: 0, want: 0},
		{name: "换行", r: '\n', want: 0},
		{name: "回车", r: '\r', want: 0},
		{name: "制表符", r: '\t', want: 0},
		{name: "ESC 控制符", r: 0x1b, want: 0},
		{name: "汉字", r: '中', want: 2},
		{name: "平假名", r: 'あ', want: 2},
		{name: "片假名", r: 'ア', want: 2},
		{name: "韩文音节", r: '한', want: 2},
		{name: "全角字母", r: 'Ａ', want: 2},
		{name: "全角括号", r: '（', want: 2},
		{name: "CJK 扩展 A", r: 0x3400, want: 2},
		{name: "CJK 扩展 B", r: 0x20000, want: 2},
		{name: "emoji 按半角计", r: '😀', want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := RuneWidth(tt.r); got != tt.want {
				t.Errorf("RuneWidth(%q) = %d, want %d", tt.r, got, tt.want)
			}
		})
	}
}

func TestDisplayWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		want int
	}{
		{name: "空串", s: "", want: 0},
		{name: "纯 ASCII", s: "abc", want: 3},
		{name: "纯中文", s: "中文", want: 4},
		{name: "中英混排", s: "a中", want: 3},
		{name: "带控制符", s: "a\tb", want: 2},
		{name: "版本号样式", s: "版本 v1.0.0", want: 4 + 1 + 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := DisplayWidth(tt.s); got != tt.want {
				t.Errorf("DisplayWidth(%q) = %d, want %d", tt.s, got, tt.want)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{name: "需补齐", s: "abc", width: 5, want: "abc  "},
		{name: "恰好等宽", s: "abcde", width: 5, want: "abcde"},
		{name: "超宽不截断", s: "abcdef", width: 5, want: "abcdef"},
		{name: "全角按两列算", s: "中文", width: 6, want: "中文  "},
		{name: "全角恰好等宽", s: "中文", width: 4, want: "中文"},
		{name: "空串补满", s: "", width: 3, want: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := PadRight(tt.s, tt.width)
			if got != tt.want {
				t.Errorf("PadRight(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
			}

			// 补齐后显示宽度必须达到 width（不超宽时）
			if w := DisplayWidth(got); w < tt.width {
				t.Errorf("PadRight(%q, %d) 显示宽度 = %d, 应 >= %d", tt.s, tt.width, w, tt.width)
			}
		})
	}
}
