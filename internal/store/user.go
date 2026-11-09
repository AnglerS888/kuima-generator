package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/logger"
	"wxcode/internal/model"
	"wxcode/internal/passwd"
	"wxcode/internal/util"
)

// SetPassword 写入密码哈希（明文列一律清空，仅保留历史兼容）。
func SetPassword(userID int64, passwordHash string) error {
	_, err := DB.Exec(`UPDATE users SET password = '', password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, NowStr(), userID)
	return err
}

// touchUserIP 记录用户最近一次使用的 IP。
func TouchUserIP(userID int64, ip string) {
	if userID <= 0 || ip == "" {
		return
	}
	_, _ = DB.Exec(`UPDATE users SET last_ip = ? WHERE id = ? AND last_ip <> ?`, ip, userID, ip)
}

// ---------- 查询 ----------
const userCols = `id, username, password_hash, role, status, quota, quota_used,
	api_key, last_ip, email, note, created_at, updated_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Status,
		&u.Quota, &u.QuotaUsed, &u.APIKey, &u.LastIP, &u.Email, &u.Note,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func GetUserByID(id int64) (*model.User, error) {
	return scanUser(DB.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func GetUserByUsername(name string) (*model.User, error) {
	return scanUser(DB.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, name))
}

// errEmailAmbiguous 表示同一个邮箱关联了多个账号（历史数据 / 管理员手工改过）。
// 这种情况不允许用邮箱登录，必须让用户改用用户名，避免登录到别人的账号。
var ErrEmailAmbiguous = errors.New("该邮箱关联了多个账号，请改用用户名登录或联系管理员")

// getUserByEmail 按绑定邮箱找账号：忽略大小写与首尾空格。
//
// 邮箱不是唯一索引（老库可能重复），这里最多取 2 条来判断冲突；
// 只匹配真正填了邮箱的账号 —— 空邮箱不能当账号用。
func GetUserByEmail(email string) (*model.User, error) {
	email = util.NormalizeEmail(email)
	if email == "" {
		return nil, ErrNotFound
	}
	rows, err := DB.Query(`SELECT `+userCols+` FROM users
		WHERE TRIM(email) <> '' AND TRIM(email) = ? COLLATE NOCASE
		ORDER BY id LIMIT 2`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var found []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return found[0], nil
	default:
		return nil, ErrEmailAmbiguous
	}
}

// emailTakenByOther 判断邮箱是否已被别的账号占用（忽略大小写 / 首尾空格）。
// excludeUserID 传 0 表示「注册新账号」，传当前用户 id 表示换绑场景。
func EmailTakenByOther(email string, excludeUserID int64) (bool, error) {
	email = util.NormalizeEmail(email)
	if email == "" {
		return false, nil
	}
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM users
		WHERE TRIM(email) <> '' AND TRIM(email) = ? COLLATE NOCASE AND id <> ?`,
		email, excludeUserID).Scan(&n)
	return n > 0, err
}

func GetUserByAPIKey(key string) (*model.User, error) {
	return scanUser(DB.QueryRow(`SELECT `+userCols+` FROM users WHERE api_key = ?`, key))
}

// CreateUser 建账号；passwordHash 必须是 passwd.Hash 的结果，额度为 nil 时用当前默认额度。
func CreateUser(username, passwordHash, role string, quota *int) (*model.User, error) {
	q := SettingInt(SetDefaultQuota, 100)
	if quota != nil {
		q = *quota
	}
	ts := NowStr()
	res, err := DB.Exec(`INSERT INTO users
		(username, password, password_hash, role, status, quota, quota_used, api_key, created_at, updated_at)
		VALUES(?, '', ?, ?, 'active', ?, 0, ?, ?, ?)`,
		username, passwordHash, role, q, util.NewAPIKey(), ts, ts)
	if err != nil {
		if isUniqueErr(err) {
			return nil, fmt.Errorf("用户名已存在")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return GetUserByID(id)
}

// ---------- 会话 ----------
const SessionCookie = "wx_sid"

const SessionTTL = 7 * 24 * time.Hour

const AdminTTL = 12 * time.Hour

func CreateSession(userID int64, isAdmin bool, ip, ua string) (string, error) {
	ttl := SessionTTL
	if isAdmin {
		ttl = AdminTTL
	}
	id := util.RandomHex(32)
	_, err := DB.Exec(`INSERT INTO sessions(id, user_id, expires_at, created_at, ip, ua)
		VALUES(?, ?, ?, ?, ?, ?)`,
		id, userID, time.Now().UTC().Add(ttl).Format(time.RFC3339), NowStr(), ip, util.Truncate(ua, 200))
	if err != nil {
		return "", err
	}
	return id, nil
}

// userBySession 按 cookie 取当前用户；过期 / 被封禁都返回 nil。
func UserBySession(sid string) *model.User {
	if sid == "" {
		return nil
	}
	var uid int64
	var exp string
	err := DB.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE id = ?`, sid).Scan(&uid, &exp)
	if err != nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || time.Now().UTC().After(t) {
		_, _ = DB.Exec(`DELETE FROM sessions WHERE id = ?`, sid)
		return nil
	}
	u, err := GetUserByID(uid)
	if err != nil || u.IsBanned() {
		return nil
	}
	return u
}

func DeleteSession(sid string) {
	if sid != "" {
		_, _ = DB.Exec(`DELETE FROM sessions WHERE id = ?`, sid)
	}
}

// 每次启动清一次过期会话，避免表无限增长
func CleanupSessions() {
	_, _ = DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, NowStr())
}

// ---------- 额度 ----------
// consumeQuota 原子扣减通用次数；转码和解码共用这一份额度。
func ConsumeQuota(userID int64) bool {
	res, err := DB.Exec(`UPDATE users
		SET quota = quota - 1, quota_used = quota_used + 1, updated_at = ?
		WHERE id = ? AND quota > 0`, NowStr(), userID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// refundQuota 生成失败时把扣掉的次数还回去。
func RefundQuota(userID int64) {
	_, _ = DB.Exec(`UPDATE users SET quota = quota + 1, quota_used = quota_used - 1, updated_at = ?
		WHERE id = ? AND quota_used > 0`, NowStr(), userID)
}

func RefreshAPIKey(userID int64) (string, error) {
	key := util.NewAPIKey()
	if _, err := DB.Exec(`UPDATE users SET api_key = ?, updated_at = ? WHERE id = ?`,
		key, NowStr(), userID); err != nil {
		return "", err
	}
	return key, nil
}

// ---------- 管理员初始化 ----------
// 首次启动时建管理员：优先用 ADMIN_USER / ADMIN_PASSWORD 环境变量，
// 否则 admin + 随机密码（只在日志里打印一次，登录后可改）。
//
// ADMIN_RESET_PASSWORD=1 时，即使管理员已存在，也把密码重置成 ADMIN_PASSWORD。
// 部署脚本用它保证「终端上显示的密码」和库里的密码一致。
func ensureAdmin() error {
	user := strings.TrimSpace(os.Getenv("ADMIN_USER"))
	if user == "" {
		user = "admin"
	}
	if !util.ValidUsername(user) {
		user = "admin"
	}
	pass := os.Getenv("ADMIN_PASSWORD")

	var n int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		// 已有管理员：默认绝不动密码（用户可能自己改过）。
		if pass == "" || !config.EnvTrue("ADMIN_RESET_PASSWORD") {
			return nil
		}
		var id int64
		var name string
		if err := DB.QueryRow(`SELECT id, username FROM users WHERE role = 'admin' ORDER BY id LIMIT 1`).
			Scan(&id, &name); err != nil {
			return err
		}
		hash, err := passwd.Hash(pass)
		if err != nil {
			return err
		}
		if err := SetPassword(id, hash); err != nil {
			return err
		}
		logger.Info("✓ 已重置管理员 " + name + " 的密码（ADMIN_RESET_PASSWORD=1）")
		return nil
	}

	generated := false
	if pass == "" {
		pass = util.RandomHex(6) // 12 位十六进制
		generated = true
	}
	hash, err := passwd.Hash(pass)
	if err != nil {
		return err
	}
	if _, err := CreateUser(user, hash, "admin", nil); err != nil {
		return err
	}
	if generated {
		// 随机密码只在日志里出现这一次，务必保留「管理员」二字：
		// 部署文档里就是靠 `docker compose logs wxcode | grep 管理员` 找它。
		logger.Info("⚠ 已创建管理员账号",
			"username", user,
			"password", pass,
			"note", "请登录后台后修改密码",
		)
	} else {
		logger.Info("✓ 已按 ADMIN_USER/ADMIN_PASSWORD 创建管理员", "username", user)
	}
	return nil
}

// ============================================================
// 管理端 / 账号维护相关的写操作
// ============================================================

// ListUsers 按关键词（用户名 / 邮箱 / 备注）与状态分页查询普通用户。
func ListUsers(keyword, status string, limit, offset int) ([]*model.User, int, error) {
	where := "WHERE role = 'user'"
	args := []any{}
	if keyword != "" {
		where += " AND (username LIKE ? OR email LIKE ? OR note LIKE ?)"
		like := "%" + keyword + "%"
		args = append(args, like, like, like)
	}
	if status == "active" || status == "banned" {
		where += " AND status = ?"
		args = append(args, status)
	}

	var total int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := DB.Query(`SELECT `+userCols+` FROM users `+where+
		` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// UpdateUserProfile 更新邮箱与备注。
func UpdateUserProfile(id int64, email, note string) error {
	_, err := DB.Exec(`UPDATE users SET email = ?, note = ?, updated_at = ? WHERE id = ?`,
		email, note, NowStr(), id)
	return err
}

// UpdateUserFields 保存管理端可编辑的字段（邮箱 / 备注 / 额度 / 状态）。
func UpdateUserFields(u *model.User) error {
	_, err := DB.Exec(`UPDATE users SET email = ?, note = ?, quota = ?, status = ?, updated_at = ?
		WHERE id = ?`, u.Email, u.Note, u.Quota, u.Status, NowStr(), u.ID)
	return err
}

// ResetPassword 管理员重置密码（写入哈希、清空明文列）；返回是否命中记录。
func ResetPassword(id int64, passwordHash string) (bool, error) {
	res, err := DB.Exec(`UPDATE users SET password = '', password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, NowStr(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteUser 删除账号。
func DeleteUser(id int64) error {
	_, err := DB.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// SetUserEmail 更新绑定邮箱；email 传空串表示解绑。
func SetUserEmail(id int64, email string) error {
	_, err := DB.Exec(`UPDATE users SET email = ?, updated_at = ? WHERE id = ?`, email, NowStr(), id)
	return err
}

// TouchLogin 记录一次成功登录的时间与来源 IP。
func TouchLogin(id int64, ip string) error {
	now := NowStr()
	_, err := DB.Exec(`UPDATE users SET last_login_at = ?, last_ip = ?, updated_at = ? WHERE id = ?`,
		now, ip, now, id)
	return err
}

// DeleteUserSessions 踢掉该用户的全部会话。
func DeleteUserSessions(userID int64) error {
	_, err := DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteOtherSessions 踢掉该用户除 keepSID 之外的会话。
func DeleteOtherSessions(userID int64, keepSID string) error {
	_, err := DB.Exec(`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepSID)
	return err
}

// Ping 检查数据库是否可用（健康检查用）。
func Ping(ctx context.Context) error {
	if DB == nil {
		return errors.New("数据库未初始化")
	}
	return DB.PingContext(ctx)
}
