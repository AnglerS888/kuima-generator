package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"wxcode/internal/model"
)

const recordExportLimit = 10000

func NormalizeRecordPayloadMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "hash", "hashed":
		return "hash"
	case "none", "off", "empty":
		return "none"
	default:
		return "full"
	}
}

// recordStorePayload 返回真正落库的内容。enabled=false 表示已关闭记录。
func RecordStorePayload(payload string) (string, bool) {
	st := LoadSettings()
	if !st.RecordEnabled {
		return "", false
	}
	switch NormalizeRecordPayloadMode(st.RecordPayloadMode) {
	case "none":
		return "", true
	case "hash":
		if payload == "" {
			return "", true
		}
		sum := sha256.Sum256([]byte(strings.TrimSpace(payload)))
		return "sha256:" + hex.EncodeToString(sum[:]), true
	default:
		return payload, true
	}
}

func DeleteUserRecord(userID, id int64) (bool, error) {
	res, err := DB.Exec(`DELETE FROM records WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func DeleteAllUserRecords(userID int64) (int64, error) {
	res, err := DB.Exec(`DELETE FROM records WHERE user_id = ?`, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func ListUserRecordsForExport(userID int64) ([]model.Record, error) {
	rows, err := DB.Query(`SELECT id, user_id, username, kind, source, payload, detail,
		format, size_bytes, ms, ip, ua, created_at FROM records
		WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, recordExportLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Record, 0, 64)
	for rows.Next() {
		var r model.Record
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &r.Kind, &r.Source, &r.Payload,
			&r.Detail, &r.Format, &r.SizeBytes, &r.MS, &r.IP, &r.UA, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// csvSafe 避免 Excel 把用户内容当作公式执行。
func CsvSafe(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}
