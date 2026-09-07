// Package strutil 提供字符串工具函数。
package strutil

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
