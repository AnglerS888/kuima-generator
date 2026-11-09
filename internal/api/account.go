package api

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"

	"wxcode/internal/httpx"
	"wxcode/internal/store"
)

// handleMyRecordsExport 导出当前用户的记录为 CSV。
// GET /api/me/records/export
func handleMyRecordsExport(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	items, err := store.ListUserRecordsForExport(u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="wxcode-records.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	wr := csv.NewWriter(w)
	_ = wr.Write([]string{"id", "time", "kind", "source", "content", "format", "sizeBytes", "ms", "ip", "userAgent", "detail"})
	for _, rec := range items {
		_ = wr.Write([]string{
			strconv.FormatInt(rec.ID, 10), rec.CreatedAt, rec.Kind, rec.Source,
			store.CsvSafe(rec.Payload), rec.Format, strconv.Itoa(rec.SizeBytes),
			strconv.Itoa(rec.MS), rec.IP, store.CsvSafe(rec.UA), store.CsvSafe(rec.Detail),
		})
	}
	wr.Flush()
	if err := wr.Error(); err != nil {
		return
	}
}

// handleMyRecordDelete 删除当前用户的一条记录。
// DELETE /api/me/records/{id}
func handleMyRecordDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("记录 ID 无效"))
		return
	}
	ok, err := store.DeleteUserRecord(u.ID, id)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		httpx.WriteErr(w, http.StatusNotFound, store.ErrNotFound)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMyRecordsClear 清空当前用户的全部记录。
// DELETE /api/me/records
func handleMyRecordsClear(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	n, err := store.DeleteAllUserRecords(u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
}
