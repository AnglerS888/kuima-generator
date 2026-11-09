package model

import (
	"strings"
)

// ============================================================
// 账号模型：与 users 表一一对应，附带少量纯展示方法
// ============================================================

// User 是账号的完整视图（含密码哈希，只在服务端内部流转，序列化用 PublicView）。
//
// PasswordHash 是 passwd 包的哈希串（pbkdf2-sha256$iter$salt$hash），
// 库里不保存明文；老库的明文列在启动迁移时统一转成哈希并清空。
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string // user | admin
	Status       string // active | banned
	Quota        int    // 通用次数（转码 / 解码共用，卡密兑换也加到这里）
	QuotaUsed    int
	APIKey       string
	LastIP       string
	Email        string
	Note         string
	CreatedAt    string
	UpdatedAt    string
	LastLoginAt  string
}

func (u *User) IsAdmin() bool { return u.Role == "admin" }

func (u *User) IsBanned() bool { return u.Status == "banned" }

// publicView 是返回给前端的字段，绝不包含 password_hash。
func (u *User) PublicView() map[string]any {
	remaining := max(0, u.Quota)
	return map[string]any{
		"id":          u.ID,
		"username":    u.Username,
		"role":        u.Role,
		"status":      u.Status,
		"quota":       remaining,
		"quotaUsed":   u.QuotaUsed,
		"apiKey":      u.APIKey,
		"lastIp":      u.LastIP,
		"email":       strings.TrimSpace(u.Email),
		"note":        u.Note,
		"createdAt":   u.CreatedAt,
		"updatedAt":   u.UpdatedAt,
		"lastLoginAt": u.LastLoginAt,
	}
}
