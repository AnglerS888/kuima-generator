package util

import (
	"crypto/rand"
	"encoding/hex"
)

// ---------- 随机串与校验 ----------
func RandomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败属于系统级异常，直接 panic 比返回弱随机更安全
		panic("crypto/rand 不可用: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func NewAPIKey() string { return "wx_" + RandomHex(16) }

func ValidUsername(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
