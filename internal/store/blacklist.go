package store

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"unicode/utf8"

	"wxcode/internal/model"
)

// 黑名单：内容关键词（禁止生成）+ IP（整站拒绝访问）。
const blKindKeyword = "keyword" // 内容关键词：生成内容包含即拒绝

const blKindIP = "ip" // IP：该来源的请求一律 403

const blMaxKeywordLen = 64 // 单个关键词最多 64 个字符

const blMaxEntries = 500 // 全表上限，防止被塞爆内存

var (
	blMu       sync.RWMutex
	blLoaded   bool
	blKeywords []string        // 已转小写，匹配时对内容同样转小写
	blIPSet    map[string]bool // 归一化后的 IP 字符串
)

// invalidateBlacklist 让下一次查询重新从库里加载（增删后调用）。
func invalidateBlacklist() {
	blMu.Lock()
	blLoaded = false
	blMu.Unlock()
}

func blacklistSnapshot() ([]string, map[string]bool) {
	blMu.RLock()
	if blLoaded {
		keys, ips := blKeywords, blIPSet
		blMu.RUnlock()
		return keys, ips
	}
	blMu.RUnlock()

	blMu.Lock()
	defer blMu.Unlock()
	if blLoaded {
		return blKeywords, blIPSet
	}
	keys := make([]string, 0, 16)
	ips := make(map[string]bool, 16)
	rows, err := DB.Query(`SELECT kind, value FROM blacklist`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var kind, value string
			if rows.Scan(&kind, &value) != nil {
				continue
			}
			switch kind {
			case blKindKeyword:
				if v := strings.ToLower(strings.TrimSpace(value)); v != "" {
					keys = append(keys, v)
				}
			case blKindIP:
				if v := strings.TrimSpace(value); v != "" {
					ips[v] = true
				}
			}
		}
	}
	blKeywords, blIPSet, blLoaded = keys, ips, true
	return keys, ips
}

// ============================================================
// 命中判断
// ============================================================

// isIPBlocked 判断该 IP 是否在黑名单里（精确匹配）。
func IsIPBlocked(ip string) bool {
	if ip == "" {
		return false
	}
	_, ips := blacklistSnapshot()
	return ips[ip]
}

// blacklistKeywordHit 判断文本是否包含黑名单关键词（不区分大小写）。
// 命中返回 true；不返回具体命中词，避免接口被用来探测黑名单内容。
func BlacklistKeywordHit(text string) bool {
	if text == "" {
		return false
	}
	keys, _ := blacklistSnapshot()
	if len(keys) == 0 {
		return false
	}
	low := strings.ToLower(text)
	for _, k := range keys {
		if k != "" && strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// ============================================================
// 管理端读写
// ============================================================

func listBlacklist() ([]model.BlacklistEntry, error) {
	rows, err := DB.Query(`SELECT id, kind, value, note, created_at FROM blacklist ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.BlacklistEntry, 0, 32)
	for rows.Next() {
		var e model.BlacklistEntry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Value, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

// listBlacklistPage 供后台列表分页使用。
func ListBlacklistPage(limit, offset int) ([]model.BlacklistEntry, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM blacklist`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := DB.Query(`SELECT id, kind, value, note, created_at FROM blacklist
		ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]model.BlacklistEntry, 0, limit)
	for rows.Next() {
		var e model.BlacklistEntry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Value, &e.Note, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}

// normalizeBlacklistValue 校验并归一化：IP 只接受合法地址，关键词转小写去空白。
func normalizeBlacklistValue(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("内容不能为空")
	}
	switch kind {
	case blKindIP:
		ip := net.ParseIP(v)
		if ip == nil {
			return "", errors.New("IP 格式不正确（示例：120.239.90.132）")
		}
		return ip.String(), nil
	case blKindKeyword:
		if utf8.RuneCountInString(v) > blMaxKeywordLen {
			return "", fmt.Errorf("关键词最多 %d 个字符", blMaxKeywordLen)
		}
		return strings.ToLower(v), nil
	}
	return "", errors.New("类型只能是 keyword（内容）或 ip")
}

func AddBlacklistEntry(kind, value, note string) (*model.BlacklistEntry, error) {
	v, err := normalizeBlacklistValue(kind, value)
	if err != nil {
		return nil, err
	}
	var n int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM blacklist`).Scan(&n); err != nil {
		return nil, err
	}
	if n >= blMaxEntries {
		return nil, fmt.Errorf("黑名单最多 %d 条，请先清理", blMaxEntries)
	}
	now := NowStr()
	res, err := DB.Exec(`INSERT INTO blacklist(kind, value, note, created_at) VALUES(?,?,?,?)`,
		kind, v, strings.TrimSpace(note), now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, errors.New("这条已经在黑名单里了")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	invalidateBlacklist()
	return &model.BlacklistEntry{ID: id, Kind: kind, Value: v, Note: strings.TrimSpace(note), CreatedAt: now}, nil
}

func getBlacklistEntry(id int64) (*model.BlacklistEntry, error) {
	var e model.BlacklistEntry
	err := DB.QueryRow(`SELECT id, kind, value, note, created_at FROM blacklist WHERE id = ?`, id).
		Scan(&e.ID, &e.Kind, &e.Value, &e.Note, &e.CreatedAt)
	if err != nil {
		return nil, errors.New("记录不存在")
	}
	return &e, nil
}

// updateBlacklistEntry 修改内容 / 备注。类型不允许改（keyword ↔ ip 语义完全不同），
// 要换类型请删除后重新添加，避免一条记录在“关键词”和“IP”之间来回变导致误解。
func UpdateBlacklistEntry(id int64, kind, value, note string) (*model.BlacklistEntry, error) {
	old, err := getBlacklistEntry(id)
	if err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = old.Kind
	}
	if kind != old.Kind {
		return nil, errors.New("不能修改类型，请删除后重新添加")
	}
	v, err := normalizeBlacklistValue(kind, value)
	if err != nil {
		return nil, err
	}
	if _, err := DB.Exec(`UPDATE blacklist SET value = ?, note = ? WHERE id = ?`,
		v, strings.TrimSpace(note), id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, errors.New("这条已经在黑名单里了")
		}
		return nil, err
	}
	invalidateBlacklist()
	old.Value, old.Note = v, strings.TrimSpace(note)
	return old, nil
}

func DeleteBlacklistEntry(id int64) error {
	res, err := DB.Exec(`DELETE FROM blacklist WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("记录不存在")
	}
	invalidateBlacklist()
	return nil
}
