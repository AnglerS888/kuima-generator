package util

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// truncate 截断过长的 UA / 备注，避免单条记录过大。
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// 按 rune 截，避免把多字节字符切成半个
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// htmlEscape 只用于少量文本（站名），避免为了一个字段引入模板。
func HTMLEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// ---------- 邮箱格式 ----------

func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// validEmail 是「够用的严格」：一个 @、无空格/引号/尖括号、域名带点。
// 不做 RFC 5322 全量校验——真正的有效性由验证码邮件能否送达来证明。
func ValidEmail(s string) bool {
	if len(s) < 5 || len(s) > EmailMaxLen {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n<>()[]\\,;:\"'") {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') {
		return false
	}
	local, domain := s[:at], s[at+1:]
	if local == "" || domain == "" {
		return false
	}
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	if strings.Contains(domain, "..") {
		return false
	}
	return true
}

// maskEmail 把 user@example.com 显示成 us***@example.com，用于「验证码已发送到…」。
func MaskEmail(e string) string {
	at := strings.LastIndexByte(e, '@')
	if at <= 0 {
		return e
	}
	name, domain := e[:at], e[at:]
	r := []rune(name)
	n := 2
	if len(r) <= 2 {
		n = 1
	}
	return string(r[:n]) + "***" + domain
}

// utf8Len 按字符数（不是字节数）计长度。
func UTF8Len(s string) int { return len([]rune(s)) }

// firstNonEmpty 返回第一个非空字符串。
func FirstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
