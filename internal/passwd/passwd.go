// Package passwd 提供密码哈希与校验（PBKDF2-HMAC-SHA256）。
//
// 存储格式：pbkdf2-sha256$<迭代次数>$<salt hex>$<hash hex>
// 只保存哈希、不保存明文；迭代次数写进字符串里，以后调高参数也能平滑升级
// （登录成功时用 NeedsRehash 判断并静默重算）。
package passwd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	algo       = "pbkdf2-sha256"
	iterations = 100_000 // PBKDF2 迭代次数
	keyLen     = 32      // 派生密钥长度（字节）
	saltLen    = 16      // 盐长度（字节）
)

// Hash 把明文密码转成可直接落库的哈希串。
func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码盐失败: %w", err)
	}
	sum := pbkdf2SHA256([]byte(password), salt, iterations, keyLen)
	return fmt.Sprintf("%s$%d$%s$%s", algo, iterations,
		hex.EncodeToString(salt), hex.EncodeToString(sum)), nil
}

// Verify 校验明文密码是否与哈希串匹配（常数时间比较）。
func Verify(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != algo {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NeedsRehash 报告哈希串的参数是否弱于当前配置。
// 调用方应在 Verify 成功后检查，并在需要时用 Hash 重新写入。
func NeedsRehash(encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != algo {
		return true
	}
	iter, err := strconv.Atoi(parts[1])
	return err != nil || iter < iterations
}

// pbkdf2SHA256 按 RFC 2898 实现 PBKDF2-HMAC-SHA256（标准库没有 PBKDF2，避免多引一个依赖）。
func pbkdf2SHA256(password, salt []byte, iter, n int) []byte {
	hLen := sha256.Size
	blocks := (n + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	var idx [4]byte
	for block := 1; block <= blocks; block++ {
		idx[0] = byte(block >> 24)
		idx[1] = byte(block >> 16)
		idx[2] = byte(block >> 8)
		idx[3] = byte(block)
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(idx[:])
		u := mac.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:n]
}
