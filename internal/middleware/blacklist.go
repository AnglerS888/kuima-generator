package middleware

import (
	"net/http"

	"wxcode/internal/httpx"
	"wxcode/internal/netutil"
	"wxcode/internal/store"
	"wxcode/internal/util"
)

// ============================================================
// 请求拦截
// ============================================================

// blockBlacklistIP 把黑名单 IP 的请求一律拦在门外（403 页面）。
//
// 例外：已登录管理员（会话或 API Key）放行 —— 否则手滑把自己的 IP 加进黑名单，
// 连后台都进不去，只能改数据库才能救回来。
func BlockBlacklistIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := netutil.ClientIP(r)
		if ip == "" || !store.IsIPBlocked(ip) {
			next.ServeHTTP(w, r)
			return
		}
		if isAdminRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(blockedPageHTML()))
	})
}

// isAdminRequest 判断请求是否带着有效的管理员身份（只在命中黑名单时调用，成本可接受）。
func isAdminRequest(r *http.Request) bool {
	if key := httpx.APIKeyFrom(r); key != "" {
		u, err := store.GetUserByAPIKey(key)
		return err == nil && u != nil && u.IsAdmin() && !u.IsBanned()
	}
	if c, err := r.Cookie(store.SessionCookie); err == nil {
		if u := store.UserBySession(c.Value); u != nil {
			return u.IsAdmin()
		}
	}
	return false
}

// blockedPageHTML 是给被拦截访客看的静态页（不引任何外部资源）。
func blockedPageHTML() string {
	name := util.HTMLEscape(store.LoadSettings().SiteName)
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>访问受限</title>` +
		`<style>body{margin:0;min-height:100vh;display:grid;place-items:center;` +
		`background:#f4f6fa;color:#131a26;font:15px/1.7 -apple-system,BlinkMacSystemFont,"Segoe UI",` +
		`"PingFang SC","Microsoft YaHei",sans-serif}` +
		`.b{max-width:22rem;margin:1.5rem;padding:1.6rem 1.5rem;background:#fff;border:1px solid #e6eaf1;` +
		`border-radius:14px;box-shadow:0 10px 30px -20px rgba(18,26,40,.4);text-align:center}` +
		`h1{margin:0 0 .5rem;font-size:1.05rem}p{margin:.35rem 0;color:#67707f;font-size:.85rem}` +
		`code{font-size:.78rem;color:#d93025}</style></head><body><div class="b">` +
		`<h1>访问受限</h1><p>你的 IP 已被本站加入黑名单，无法继续访问。</p>` +
		`<p>如有疑问请联系站点管理员。</p><p><code>` + name + `</code></p></div></body></html>`
}
