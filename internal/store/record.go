package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"wxcode/internal/model"
	"wxcode/internal/util"
)

// hasDone 判断该用户是否已经做过同样的事（同类型 + 同内容）。
// 重复的内容不再扣额度、也不再写记录；匿名（user_id<=0）不参与去重。
func HasDone(userID int64, kind, payload string) bool {
	stored, enabled := RecordStorePayload(payload)
	if userID <= 0 || !enabled || stored == "" {
		return false
	}
	var one int
	err := DB.QueryRow(`SELECT 1 FROM records
		WHERE user_id = ? AND kind = ? AND payload = ? LIMIT 1`,
		userID, kind, stored).Scan(&one)
	// 查到就是做过；查不到（ErrNoRows）或查询出错都当作"没做过"。
	// 出错时按没做过处理是安全的一侧：最多是多扣一次额度，不会白送额度。
	return err == nil && one == 1
}

// newRecord 组装一条待写入的记录（detail 传 map，自动转 JSON）。
func NewRecord(u *model.User, kind, source, payload, format string, detail map[string]any, size, ms int, ip, ua string) *model.Record {
	r := &model.Record{
		Kind: kind, Source: source, Payload: payload, Format: format,
		SizeBytes: size, MS: ms, IP: ip, UA: util.Truncate(ua, 200), CreatedAt: NowStr(),
	}
	if u != nil {
		r.UserID = u.ID
		r.Username = u.Username
	}
	if len(detail) > 0 {
		if b, err := json.Marshal(detail); err == nil {
			r.Detail = string(b)
		}
	}
	return r
}

func InsertRecord(r *model.Record) error {
	res, err := DB.Exec(`INSERT INTO records
		(user_id, username, kind, source, payload, detail, format, size_bytes, ms, ip, ua, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.UserID, r.Username, r.Kind, r.Source, r.Payload, r.Detail,
		r.Format, r.SizeBytes, r.MS, r.IP, r.UA, r.CreatedAt)
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil {
		r.ID = id
	}
	return nil
}

// recordFilter 是记录查询条件，管理端和用户端共用。
type RecordFilter struct {
	UserID int64  // 0 = 不限
	Kind   string // "" = 不限
	Limit  int
	Offset int
}

func (f *RecordFilter) normalize() {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
}

func ListRecords(f RecordFilter) ([]model.Record, int, error) {
	f.normalize()
	where := "WHERE 1=1"
	args := []any{}
	if f.UserID > 0 {
		where += " AND user_id = ?"
		args = append(args, f.UserID)
	}
	if f.Kind != "" {
		where += " AND kind = ?"
		args = append(args, f.Kind)
	}

	var total int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM records `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := DB.Query(`SELECT id, user_id, username, kind, source, payload, detail,
		format, size_bytes, ms, ip, ua, created_at FROM records `+where+
		` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]model.Record, 0, f.Limit)
	for rows.Next() {
		var r model.Record
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &r.Kind, &r.Source, &r.Payload,
			&r.Detail, &r.Format, &r.SizeBytes, &r.MS, &r.IP, &r.UA, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// recordStats 给管理端做一眼可见的汇总。
func RecordStats() map[string]any {
	stat := map[string]any{}
	var total, gens, decs, today int
	_ = DB.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&total)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind = 'gen'`).Scan(&gens)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind = 'decode'`).Scan(&decs)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM records WHERE substr(created_at, 1, 10) = substr(?, 1, 10)`,
		NowStr()).Scan(&today)
	stat["total"] = total
	stat["gen"] = gens
	stat["decode"] = decs
	stat["today"] = today
	var users int
	_ = DB.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'user'`).Scan(&users)
	stat["users"] = users
	return stat
}

var errBadInvite = fmt.Errorf("邀请码无效或已用尽")

// CreateInvite 新建邀请码，返回本次生成批次号（导出本次生成用）。
func CreateInvite(code string, maxUses int, expiresAt, note string) (string, error) {
	if maxUses <= 0 {
		maxUses = 1
	}
	batch := newBatchID()
	_, err := DB.Exec(`INSERT INTO invite_codes(code, max_uses, used, expires_at, note, created_at, batch_id)
		VALUES(?, ?, 0, ?, ?, ?, ?)`, code, maxUses, expiresAt, note, NowStr(), batch)
	if err != nil && isUniqueErr(err) {
		return "", fmt.Errorf("邀请码已存在")
	}
	if err != nil {
		return "", err
	}
	return batch, nil
}

// ListInvites 列出邀请码；batch 非空时只返回该批次（导出本次生成用）。
func ListInvites(batch string) ([]model.InviteCode, error) {
	query := `SELECT code, max_uses, used, expires_at, note, created_at, batch_id
		FROM invite_codes`
	args := []any{}
	if batch = strings.TrimSpace(batch); batch != "" {
		query += ` WHERE batch_id = ?`
		args = append(args, batch)
	}
	query += ` ORDER BY created_at DESC, code`
	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.InviteCode{}
	for rows.Next() {
		var c model.InviteCode
		if err := rows.Scan(&c.Code, &c.MaxUses, &c.Used, &c.ExpiresAt, &c.Note, &c.CreatedAt, &c.BatchID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// listInvitesPage 供后台列表分页使用；导出仍走 listInvites 取全量。
func ListInvitesPage(limit, offset int) ([]model.InviteCode, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM invite_codes`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := DB.Query(`SELECT code, max_uses, used, expires_at, note, created_at, batch_id
		FROM invite_codes ORDER BY created_at DESC, code LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.InviteCode{}
	for rows.Next() {
		var c model.InviteCode
		if err := rows.Scan(&c.Code, &c.MaxUses, &c.Used, &c.ExpiresAt, &c.Note, &c.CreatedAt, &c.BatchID); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func DeleteInvite(code string) error {
	_, err := DB.Exec(`DELETE FROM invite_codes WHERE code = ?`, code)
	return err
}

// consumeInvite 校验并占用一次。用事务保证并发下不会超发。
func ConsumeInvite(code string) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var max, used int
	var exp string
	err = tx.QueryRow(`SELECT max_uses, used, expires_at FROM invite_codes WHERE code = ?`, code).
		Scan(&max, &used, &exp)
	if err != nil {
		if err == sql.ErrNoRows {
			return errBadInvite
		}
		return err
	}
	if used >= max {
		return errBadInvite
	}
	if exp != "" && exp < NowStr() {
		return errBadInvite
	}
	if _, err = tx.Exec(`UPDATE invite_codes SET used = used + 1 WHERE code = ?`, code); err != nil {
		return err
	}
	return tx.Commit()
}

// purgeRecordsBefore 手动清理早于指定日期的记录（后台「立即清理」按钮用）。
//
// 返回删除的条数。
func PurgeRecordsBefore(cutoff string) (int64, error) {
	res, err := DB.Exec(`DELETE FROM records WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
