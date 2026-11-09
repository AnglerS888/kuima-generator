package middleware

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/logger"
	"wxcode/internal/netutil"
	"wxcode/internal/store"
)

// ============================================================
// 四、中间件：安全响应头 + 限速
// ============================================================

// securityHeaders 给所有响应加基础安全头。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		if httpx.CookieSecure(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		// 页面用内联脚本/样式，所以放开 inline；外部资源默认一律禁止，图片允许 data:/blob: 预览。
		//
		// 例外：管理员在后台填了底部统计代码（51.la / 百度统计 / Umami 之类），
		// 这些脚本必然来自外部域名，此时才放开 https: —— 没填就保持封闭。
		ext := ""
		if strings.TrimSpace(store.LoadSettings().FooterHTML) != "" {
			ext = " https:"
		}
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline'"+ext+"; "+
				"style-src 'self' 'unsafe-inline'"+ext+"; "+
				"img-src 'self' data: blob:"+ext+"; "+
				"connect-src 'self'"+ext+"; "+
				"font-src 'self' data:"+ext+"; "+
				"frame-src 'self'"+ext+"; "+
				"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// protectAPI 给 API 路由套上限速与并发保护。
func ProtectAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !netutil.ApiLimiter.Allow(netutil.ClientIP(r)) {
			w.Header().Set("Retry-After", "60")
			// debug 级：被限速通常是正常流量尖峰或有人在刷接口。
			// 默认 info 下不刷屏，但排查「为什么突然 429」时把 LOG_LEVEL 调到 debug 就能看到。
			logger.LogCtx(r.Context(), logger.LevelDebug, "api_rate_limited",
				"ip", netutil.ClientIP(r),
				"path", r.URL.Path,
			)
			httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("请求过于频繁，请稍后再试"))
			return
		}
		next(w, r)
	}
}

// sameOriginGuard 防止第三方站点借用浏览器里的会话 Cookie 发起写操作。
// 带 API Key 的程序调用不依赖 ambient credential，不走浏览器 CSRF 判定；
// 没有 Origin 的 curl / 服务端调用也保持兼容。
func SameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || httpx.APIKeyFrom(r) != "" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
			logger.LogCtx(r.Context(), logger.LevelDebug, "csrf_rejected",
				"reason", "sec-fetch-site",
				"path", r.URL.Path,
				"origin", r.Header.Get("Origin"),
			)
			httpx.WriteErr(w, http.StatusForbidden, errors.New("拒绝跨站请求"))
			return
		}
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			o, err := url.Parse(origin)
			base, baseErr := url.Parse(httpx.RequestBaseURL(r))
			if err != nil || baseErr != nil || o.Host == "" ||
				!strings.EqualFold(o.Scheme, base.Scheme) || !strings.EqualFold(o.Host, base.Host) {
				logger.LogCtx(r.Context(), logger.LevelDebug, "csrf_rejected",
					"reason", "origin_mismatch",
					"path", r.URL.Path,
					"origin", origin,
				)
				httpx.WriteErr(w, http.StatusForbidden, errors.New("请求来源与站点不一致"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withHeavyGuard 包住耗 CPU 的处理（编码/解码），拿不到令牌直接 503。
func WithHeavyGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !imagex.AllowHeavy() {
			w.Header().Set("Retry-After", "1")
			// 并发打满 = 容量不够，是扩容的信号，比普通 503 更值得单独记一条
			logger.LogCtx(r.Context(), logger.LevelWarn, "heavy_concurrency_full",
				"path", r.URL.Path,
				"max_concurrent", netutil.MaxConcurrentHeavy,
			)
			httpx.WriteErr(w, http.StatusServiceUnavailable, errors.New("服务繁忙，请稍后重试"))
			return
		}
		defer imagex.ReleaseHeavy()
		next(w, r)
	}
}
