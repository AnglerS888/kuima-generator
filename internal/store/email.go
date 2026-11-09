package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"wxcode/internal/util"
)

const EmailCodeTTL = 10 * time.Minute

const EmailCodeMaxAttempts = 5

const EmailPurposeBind = "bind"

var errEmailCodeBad = errors.New("验证码错误或已过期，请重新获取")

var EmailCodeGen = newEmailCode

// ---------- 验证码 ----------

// newEmailCode 生成 6 位数字验证码（拒绝采样，避免取模偏差）。
func newEmailCode() string {
	const digits = "0123456789"
	out := make([]byte, 6)
	for i := range out {
		var b [1]byte
		for {
			if _, err := rand.Read(b[:]); err != nil {
				panic("crypto/rand 不可用: " + err.Error())
			}
			// 250 = 25×10，丢掉尾部避免 %10 分布不均
			if int(b[0]) < 250 {
				out[i] = digits[int(b[0])%10]
				break
			}
		}
	}
	return string(out)
}

func hashEmailCode(purpose, subject, code string) string {
	sum := sha256.Sum256([]byte("wxcode-email-code|" + purpose + "|" + subject + "|" + code))
	return hex.EncodeToString(sum[:])
}

// issueEmailCode 生成并落库一条验证码，返回明文交给调用方发信。
// subject 是归属键：已登录场景用 emailSubjectKey(userID)，注册场景用 emailAddrKey(email)。
func IssueEmailCode(purpose, subject, email string) (string, error) {
	now := time.Now().UTC()

	var last string
	err := DB.QueryRow(`SELECT created_at FROM email_codes
		WHERE purpose = ? AND subject = ? ORDER BY id DESC LIMIT 1`, purpose, subject).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == nil {
		if t, e := time.Parse(time.RFC3339, last); e == nil {
			if wait := util.EmailCodeResend - now.Sub(t); wait > 0 {
				secs := int(wait.Seconds()) + 1
				if secs < 1 {
					secs = 1
				}
				return "", fmt.Errorf("验证码发送过于频繁，请 %d 秒后再试", secs)
			}
		}
	}

	var n int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM email_codes
		WHERE purpose = ? AND subject = ? AND created_at > ?`,
		purpose, subject, now.Add(-time.Hour).Format(time.RFC3339)).Scan(&n); err != nil {
		return "", err
	}
	if n >= util.EmailCodeMaxPerHour {
		return "", errors.New("该账号 1 小时内获取验证码次数过多，请稍后再试或联系管理员")
	}

	code := EmailCodeGen()
	// 保留旧记录：每小时次数上限要靠它们计数；校验时只取最新一条，
	// 过期记录由定时任务统一清理。
	_, err = DB.Exec(`INSERT INTO email_codes(purpose, subject, email, code_hash, attempts, expires_at, created_at)
		VALUES(?, ?, ?, ?, 0, ?, ?)`,
		purpose, subject, email, hashEmailCode(purpose, subject, code),
		now.Add(EmailCodeTTL).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return code, nil
}

// verifyEmailCode 校验验证码；成功后立即作废（一次性）。
// 连续错 5 次直接删除，必须重新获取。
func VerifyEmailCode(purpose, subject, code string) error {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return errEmailCodeBad
	}

	var (
		id       int64
		hash     string
		expires  string
		attempts int
	)
	err := DB.QueryRow(`SELECT id, code_hash, expires_at, attempts FROM email_codes
		WHERE purpose = ? AND subject = ? ORDER BY id DESC LIMIT 1`, purpose, subject).
		Scan(&id, &hash, &expires, &attempts)
	if err == sql.ErrNoRows {
		return errEmailCodeBad
	}
	if err != nil {
		return err
	}
	if attempts >= EmailCodeMaxAttempts {
		_, _ = DB.Exec(`DELETE FROM email_codes WHERE purpose = ? AND subject = ?`, purpose, subject)
		return errEmailCodeBad
	}
	if t, e := time.Parse(time.RFC3339, expires); e != nil || time.Now().UTC().After(t) {
		_, _ = DB.Exec(`DELETE FROM email_codes WHERE purpose = ? AND subject = ?`, purpose, subject)
		return errEmailCodeBad
	}
	want := hashEmailCode(purpose, subject, code)
	if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 {
		_, _ = DB.Exec(`UPDATE email_codes SET attempts = attempts + 1 WHERE id = ?`, id)
		return errEmailCodeBad
	}
	// 一次性：同用途的所有历史验证码一并作废
	_, _ = DB.Exec(`DELETE FROM email_codes WHERE purpose = ? AND subject = ?`, purpose, subject)
	return nil
}

// deleteEmailCodes 清掉某个用途下的验证码（发信失败 / 绑定成功后调用）。
func DeleteEmailCodes(purpose, subject string) {
	_, _ = DB.Exec(`DELETE FROM email_codes WHERE purpose = ? AND subject = ?`, purpose, subject)
}

func DeleteAllEmailCodes(subject string) {
	_, _ = DB.Exec(`DELETE FROM email_codes WHERE subject = ?`, subject)
}
