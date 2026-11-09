package api

import (
	"context"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/httpx"
	"wxcode/internal/store"
)

// ============================================================
// 健康检查
// ============================================================

// handleHealth 健康检查（Docker HEALTHCHECK 也用它）
//
// 只回 ok:true 会漏掉一类故障：数据库文件被删、目录权限不对时进程照样活着，
// 容器一直是 healthy，直到有人真的来生成才发现全挂。这里带一次 Ping。
//
// GET /api/health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if store.Ping(ctx) != nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"ok": false, "service": "wxcode", "version": config.Version, "db": "unavailable",
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"service": "wxcode",
		"version": config.Version,
		"db":      "ok",
		// 进程启动时间：强制更新后版本号可能没变，前端用它的变化判断「确实重启过」
		"startedAt": processStarted.UTC().Format(time.RFC3339),
	})
}
