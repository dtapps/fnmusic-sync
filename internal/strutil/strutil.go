// Package strutil 提供字符串工具函数。
package strutil

import "unicode"

// FirstN 截取字符串前 n 位。
// 用于日志中展示 token 等长字符串的前缀（不泄露完整值）。
func FirstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// FirstN8 截取字符串前 8 位，等价于 FirstN(s, 8)。
func FirstN8(s string) string {
	return FirstN(s, 8)
}

// DisplayWidth 返回字符串在终端中的显示宽度（列数）。
// 东亚全角字符（中日韩）占 2 列，其余占 1 列。
// 适用于版本输出等需要列对齐的场景。
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += RuneWidth(r)
	}
	return w
}

// RuneWidth 返回单个 rune 的终端显示宽度：0（控制符）、1（半角）、2（全角）。
func RuneWidth(r rune) int {
	if r == 0 || r == '\n' || r == '\r' || r == '\t' {
		return 0
	}
	if unicode.IsControl(r) {
		return 0
	}
	// 东亚全角范围（精简版，覆盖 CJK 统一表意文字、全角标点等）
	if isWide(r) {
		return 2
	}
	return 1
}

// isWide 判断 rune 是否为宽字符（占 2 列）。
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return true
	case r >= 0x2E80 && r <= 0x303E: // CJK Radicals 等
		return true
	case r >= 0x3041 && r <= 0x33FF: // 平假名/片假名/谚文/CJK 标点
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK 扩展 A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一表意文字（常用汉字）
		return true
	case r >= 0xA000 && r <= 0xA4CF: // 彝文
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul Syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK 兼容表意文字
		return true
	case r >= 0xFE30 && r <= 0xFE4F: // CJK 兼容标点
		return true
	case r >= 0xFF00 && r <= 0xFF60: // 全角 ASCII
		return true
	case r >= 0xFFE0 && r <= 0xFFE6: // 全角符号
		return true
	case r >= 0x20000 && r <= 0x2FFFD: // CJK 扩展 B-F
		return true
	case r >= 0x30000 && r <= 0x3FFFD: // CJK 扩展 G+
		return true
	default:
		return false
	}
}

// PadRight 将 s 填充到指定显示宽度 width，右侧补空格。
// 考虑全角字符占 2 列的情况。
func PadRight(s string, width int) string {
	dw := DisplayWidth(s)
	if dw >= width {
		return s
	}
	return s + spaces(width-dw)
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
