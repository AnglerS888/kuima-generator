package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"wxcode/internal/store"
)

// CORS 相关的固定头部值。
const corsAllowMethods = "GET, POST, PUT, DELETE, OPTIONS"

const corsAllowHeaders = "Content-Type, Authorization, X-API-Key, Accept, Origin"

const (
	corsExposeHeaders = "X-WXCode-Version, X-WXCode-Level, X-WXCode-Mask, X-WXCode-Typebits," +
		"X-WXCode-Mode, X-WXCode-Codewords, X-WXCode-ECCodewords, X-WXCode-DataCodewords, Retry-After," +
		// 请求 ID：前端报错时把它一起带上，后端能直接定位到那一条日志
		"X-Request-ID"
)

// corsAllowedOrigins 返回允许的来源白名单（已去空白、去掉末尾斜杠）。
func corsAllowedOrigins() []string {
	raw := strings.TrimSpace(store.LoadSettings().CORSOrigins)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, strings.TrimRight(p, "/"))
	}
	return out
}

// corsOriginAllowed 判断请求来源是否在白名单里。
//
// 返回值是应该写进 Access-Control-Allow-Origin 的值：
//   - 通配符配置 → "*"
//   - 命中白名单 → 原样回显该来源
//   - 未命中     → ""（不放行）
func corsOriginAllowed(origin string) string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		return ""
	}
	for _, allow := range corsAllowedOrigins() {
		if allow == "*" {
			return "*"
		}
		if strings.EqualFold(allow, origin) {
			return origin
		}
	}
	return ""
}

// normalizeCORSOrigins 清洗来源白名单：只留 http(s):// 开头或 "*" 的项。
//
// 返回清洗后的逗号分隔字符串。非法项直接丢掉（而不是报错）——
// 管理员手滑多打一个逗号不该让整个配置保存失败。
func NormalizeCORSOrigins(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimRight(strings.TrimSpace(p), "/")
		if p == "" {
			continue
		}
		if p == "*" {
			out = append(out, "*")
			continue
		}
		u, err := url.Parse(p)
		if err != nil || u.Host == "" {
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// corsMiddleware 处理跨域：命中白名单就补上允许头，并直接应答 OPTIONS 预检。
//
// 预检（OPTIONS）必须在这里短路返回 200 —— 浏览器不会把预检响应交给业务代码，
// 要是它落到业务路由，多半会得到 404/405，浏览器就判定跨域失败。
func CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := corsOriginAllowed(origin)

		if allowed != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allowed)
			h.Set("Access-Control-Allow-Methods", corsAllowMethods)
			h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			h.Set("Access-Control-Max-Age", "86400")
			// 用了 Cookie 时不能配 "*"，必须回显具体来源并允许带凭证。
			if allowed != "*" {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			h.Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			// 预检：无论是否命中白名单都直接结束，不进业务逻辑。
			if allowed == "" {
				// 没命中就不给放行头，但预检本身回 204，
				// 让浏览器能明确拿到「被拒」而不是超时。
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
