package api

import (
	"errors"
	"net/http"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/httpx"
	"wxcode/internal/service"
)

// ============================================================
// 在线更新（仅管理员）
//
//   GET  /api/admin/update/check?force=1   读远端 VERSION 对比当前版本
//   POST /api/admin/update/apply           下载产物 → 替换 → 自动重启
//
// 想彻底关掉（比如生产环境只允许走发布流程）：UPDATE_DISABLE=1
// ============================================================

// updateDisabled 报告是否被环境变量关掉。
func updateDisabled() bool { return config.EnvTrue("UPDATE_DISABLE") }

// handleUpdateCheck 检查是否有新版本。
func handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if updateDisabled() {
		httpx.WriteErr(w, http.StatusForbidden, errors.New("在线更新已被管理员关闭（UPDATE_DISABLE=1）"))
		return
	}
	force := r.URL.Query().Get("force") == "1"
	info := service.CheckUpdate(r.Context(), force)
	httpx.WriteJSON(w, http.StatusOK, info)
}

// handleUpdateApply 下载并替换程序，随后自动重启（页面需要轮询 /api/health）。
func handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errors.New("只支持 POST"))
		return
	}
	if updateDisabled() {
		httpx.WriteErr(w, http.StatusForbidden, errors.New("在线更新已被管理员关闭（UPDATE_DISABLE=1）"))
		return
	}

	res, err := service.ApplyUpdate(r.Context())
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"version":     res.Version,
		"asset":       res.Asset,
		"restart":     res.Restart,
		"inContainer": res.InContainer,
	})
	// 给响应留出写回时间再换进程
	service.ScheduleRestart(800 * time.Millisecond)
}
