package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"wxcode/internal/model"
	"wxcode/internal/util"
)

const cardMaxAmount = 1_000_000

const cardMaxBatch = 500

var errCardInvalid = errors.New("卡密无效")

var ErrCardUsed = errors.New("该卡密已被兑换")

var ErrCardExpired = errors.New("该卡密已过期")

func normalizeCardCode(v string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(v), " ", ""))
}

func newCardCode() string {
	raw := strings.ToUpper(util.RandomHex(8)) // 16 hex
	return fmt.Sprintf("WX-%s-%s-%s-%s", raw[0:4], raw[4:8], raw[8:12], raw[12:16])
}

func cardStatus(c model.CardKey, now string) string {
	if c.UsedBy > 0 {
		return "used"
	}
	if c.ExpiresAt != "" && c.ExpiresAt < now {
		return "expired"
	}
	return "unused"
}

// generateCards 在一个事务里生成一批卡密，返回生成结果。
func GenerateCards(amount, count int, expiresAt, note string) ([]model.CardKey, string, error) {
	if amount <= 0 {
		amount = 1
	}
	if amount > cardMaxAmount {
		return nil, "", fmt.Errorf("单张次数不能超过 %d", cardMaxAmount)
	}
	if count <= 0 {
		count = 1
	}
	if count > cardMaxBatch {
		return nil, "", fmt.Errorf("单次最多生成 %d 张", cardMaxBatch)
	}
	note = util.Truncate(strings.TrimSpace(note), 120)
	ts := NowStr()

	tx, err := DB.Begin()
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback() }()

	batch := newBatchID()
	out := make([]model.CardKey, 0, count)
	for i := 0; i < count; i++ {
		var code string
		for attempt := 0; attempt < 8; attempt++ {
			code = newCardCode()
			// kind 保留为历史兼容字段，新卡统一写 quota。
			_, err = tx.Exec(`INSERT INTO card_keys(code, kind, amount, used_by, used_at, expires_at, note, created_at, batch_id)
				VALUES(?, 'quota', ?, 0, '', ?, ?, ?, ?)`, code, amount, expiresAt, note, ts, batch)
			if err == nil {
				break
			}
			if !isUniqueErr(err) {
				return nil, "", err
			}
		}
		if err != nil {
			return nil, "", fmt.Errorf("生成卡密失败: %w", err)
		}
		out = append(out, model.CardKey{
			Code: code, Amount: amount, ExpiresAt: expiresAt, Note: note, CreatedAt: ts,
			BatchID: batch, Status: cardStatus(model.CardKey{ExpiresAt: expiresAt}, ts),
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return out, batch, nil
}

func queryCards(status, batch string, limit, offset int) ([]model.CardKey, int, error) {
	where := []string{"1=1"}
	args := []any{}
	now := NowStr()
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "unused":
		where = append(where, "c.used_by = 0 AND (c.expires_at = '' OR c.expires_at >= ?)")
		args = append(args, now)
	case "used":
		where = append(where, "c.used_by > 0")
	case "expired":
		where = append(where, "c.used_by = 0 AND c.expires_at != '' AND c.expires_at < ?")
		args = append(args, now)
	}
	if batch = strings.TrimSpace(batch); batch != "" {
		where = append(where, "c.batch_id = ?")
		args = append(args, batch)
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM card_keys c`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := DB.Query(`SELECT c.code, c.amount, c.used_by, COALESCE(u.username, ''),
		c.used_at, c.expires_at, c.note, c.created_at, c.batch_id
		FROM card_keys c LEFT JOIN users u ON u.id = c.used_by`+clause+
		` ORDER BY c.created_at DESC, c.code LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.CardKey{}
	for rows.Next() {
		var c model.CardKey
		if err := rows.Scan(&c.Code, &c.Amount, &c.UsedBy, &c.UsedByName,
			&c.UsedAt, &c.ExpiresAt, &c.Note, &c.CreatedAt, &c.BatchID); err != nil {
			return nil, 0, err
		}
		c.Status = cardStatus(c, now)
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func ListCards(status string, limit, offset int) ([]model.CardKey, int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return queryCards(status, "", limit, offset)
}

// ListCardsForExport 导出卡密；batch 非空时只导出该批次（「导出本次生成」）。
func ListCardsForExport(status, batch string) ([]model.CardKey, error) {
	items, _, err := queryCards(status, batch, 10000, 0)
	return items, err
}

func DeleteCard(code string) (bool, error) {
	code = normalizeCardCode(code)
	if code == "" {
		return false, errCardInvalid
	}
	res, err := DB.Exec(`DELETE FROM card_keys WHERE code = ? AND used_by = 0`, code)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// redeemCard 原子完成“占用卡密 + 增加用户通用额度”。
func RedeemCard(userID int64, code string) (amount int, err error) {
	code = normalizeCardCode(code)
	if code == "" {
		return 0, errCardInvalid
	}
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var usedBy int64
	var expiresAt string
	err = tx.QueryRow(`SELECT amount, used_by, expires_at FROM card_keys WHERE code = ?`, code).
		Scan(&amount, &usedBy, &expiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, errCardInvalid
		}
		return 0, err
	}
	if usedBy > 0 {
		return 0, ErrCardUsed
	}
	now := NowStr()
	if expiresAt != "" && expiresAt < now {
		return 0, ErrCardExpired
	}
	res, err := tx.Exec(`UPDATE card_keys SET used_by = ?, used_at = ? WHERE code = ? AND used_by = 0`,
		userID, now, code)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrCardUsed
	}
	if _, err = tx.Exec(`UPDATE users SET quota = quota + ?, updated_at = ? WHERE id = ?`,
		amount, now, userID); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return amount, nil
}

// cardExpiry 把后台输入的日期统一成 RFC3339；空字符串表示长期有效。
func CardExpiry(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	t, err := ParseTimeLoose(v)
	if err != nil {
		return "", errors.New("有效期需要是 2006-01-02 或 RFC3339")
	}
	return t.Format(time.RFC3339), nil
}
