package api

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/netutil"
	"wxcode/internal/store"
)

func cardStatusLabel(status string) string {
	switch status {
	case "used":
		return "已兑换"
	case "expired":
		return "已过期"
	default:
		return "未兑换"
	}
}

// writeCSVDownload 输出带 UTF-8 BOM 的 CSV，Excel 直接打开不乱码。
func writeCSVDownload(w http.ResponseWriter, filename string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	for _, row := range rows {
		for i := range row {
			row[i] = store.CsvSafe(row[i])
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}

// handleAdminCards 卡密列表 / 批量生成。
// GET/POST /api/admin/cards
func handleAdminCards(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		items, total, err := store.ListCards(r.URL.Query().Get("status"), limit, offset)
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})

	case http.MethodPost:
		var body struct {
			Amount    int    `json:"amount"`
			Count     int    `json:"count"`
			ExpiresAt string `json:"expiresAt"`
			Note      string `json:"note"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpx.WriteErr(w, StatusOf(err), err)
			return
		}
		expires, err := store.CardExpiry(body.ExpiresAt)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
		items, batch, err := store.GenerateCards(body.Amount, body.Count, expires, body.Note)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"ok": true, "count": len(items), "batch": batch, "items": items,
		})

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}
}

// handleAdminCardDelete 删除未兑换的卡密；已兑换的保留作审计。
// DELETE /api/admin/cards/{code}
func handleAdminCardDelete(w http.ResponseWriter, r *http.Request) {
	ok, err := store.DeleteCard(r.PathValue("code"))
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if !ok {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("卡密不存在或已兑换，已兑换的卡密不能删除"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminCardsExport 导出卡密 CSV。
//
// GET /api/admin/cards/export?status=&batch=
// batch 为空导出全部（按 status 过滤）；带上刚生成返回的批次号则只导出这一批。
func handleAdminCardsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	batch := strings.TrimSpace(r.URL.Query().Get("batch"))
	if len(batch) > 64 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("batch 参数不合法"))
		return
	}
	items, err := store.ListCardsForExport(r.URL.Query().Get("status"), batch)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	rows := [][]string{{"卡密", "次数", "状态", "兑换用户", "兑换时间", "有效期", "备注", "创建时间"}}
	for _, c := range items {
		rows = append(rows, []string{
			c.Code, strconv.Itoa(c.Amount), cardStatusLabel(c.Status),
			c.UsedByName, c.UsedAt, c.ExpiresAt, c.Note, c.CreatedAt,
		})
	}
	name := "wxcode-cards-" + time.Now().Format("20060102")
	if batch != "" {
		name = "wxcode-cards-" + batch // 批次号本身带日期，避免重复
	}
	writeCSVDownload(w, name+".csv", rows)
}

// handleAdminInvitesExport 导出邀请码 CSV。
//
// GET /api/admin/invites/export?batch=
// batch 为空导出全部；带上刚生成返回的批次号则只导出这一批。
func handleAdminInvitesExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	batch := strings.TrimSpace(r.URL.Query().Get("batch"))
	if len(batch) > 64 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("batch 参数不合法"))
		return
	}
	items, err := store.ListInvites(batch)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	rows := [][]string{{"邀请码", "可用次数", "已用", "剩余", "有效期", "备注", "创建时间"}}
	for _, c := range items {
		rows = append(rows, []string{
			c.Code, strconv.Itoa(c.MaxUses), strconv.Itoa(c.Used),
			strconv.Itoa(max(0, c.MaxUses-c.Used)), c.ExpiresAt, c.Note, c.CreatedAt,
		})
	}
	name := "wxcode-invites-" + time.Now().Format("20060102")
	if batch != "" {
		name = "wxcode-invites-" + batch
	}
	writeCSVDownload(w, name+".csv", rows)
}

// handleRedeemCard 用户兑换卡密，增加通用额度。
// POST /api/me/redeem
func handleRedeemCard(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		httpx.WriteErr(w, http.StatusUnauthorized, errors.New("请先登录"))
		return
	}
	if !netutil.RedeemLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("兑换尝试过于频繁，请稍后再试"))
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	amount, err := store.RedeemCard(u.ID, body.Code)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	fresh, _ := store.GetUserByID(u.ID)
	resp := map[string]any{"ok": true, "amount": amount}
	if fresh != nil {
		resp["user"] = fresh.PublicView()
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}
