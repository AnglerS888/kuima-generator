package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"wxcode/internal/logger"
	"wxcode/internal/netutil"
	"wxcode/internal/util"
)

// withRequestID 给每个请求挂一个 request_id：
// 客户端带 X-Request-ID 就沿用（方便跨服务串联），否则自己生成，
// 并回写到响应头，调用方报障时直接把这个 id 给我们即可。
//
// 顺便把 reqState 放进 context —— 它必须在最外层创建，
// 否则认证中间件往里写的用户名访问日志读不到。
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := logger.SanitizeRequestID(r.Header.Get("X-Request-ID"))
		if rid == "" {
			rid = logger.NewRequestID()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), logger.ReqIDCtxKey, rid)
		ctx = context.WithValue(ctx, reqStateCtxKey, &reqState{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ============================================================
// 请求状态（供访问日志与认证中间件共用）
// ============================================================

// reqState 是一次请求的共享状态。放在 context 里由中间件创建，
// 各层往里写（登录中间件写用户名），最外层的访问日志最后统一读。
type reqState struct {
	mu     sync.Mutex
	user   string
	viaAPI bool
}

func (s *reqState) SetAuth(user string, viaAPI bool) {
	s.mu.Lock()
	s.user = user
	s.viaAPI = viaAPI
	s.mu.Unlock()
}

func (s *reqState) auth() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user, s.viaAPI
}

// reqStateCtxKey 请求状态在 context 里的键。
const reqStateCtxKey ctxKey = "wxcode-reqstate"

// reqStateFrom 取请求状态；没有就返回一个空壳（不 nil，方便调用方直接写）。
func ReqStateFrom(ctx context.Context) *reqState {
	if ctx == nil {
		return &reqState{}
	}
	if v, ok := ctx.Value(reqStateCtxKey).(*reqState); ok && v != nil {
		return v
	}
	return &reqState{}
}

// ============================================================
// 访问日志
// ============================================================

// statusWriter 记录响应码与字节数，供访问日志使用。
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// logRequests 打印访问日志：方法、路径、状态码、耗时、字节数、来源 IP、
// UA、request_id，以及登录用户（能取到的话）。
//
// 分级规则：5xx → error（要报警），401/403/429 → warn（被拒/被限流），
// 其余 → info。健康检查等路径按 LOG_SKIP_PATHS 跳过。
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}

		cfg := logger.CurrentLogConfig()
		if !cfg.AccessLog() || cfg.SkipPath(r.URL.Path) {
			return
		}

		lvl := logger.LevelInfo
		switch {
		case sw.status >= 500:
			// 服务端出错了，必须能被告警规则捞出来
			lvl = logger.LevelError
		case sw.status >= 400:
			// 4xx 是客户端问题（404 路径写错、401 未登录、429 被限流），
			// 值得留意但不是故障，记 warn。
			lvl = logger.LevelWarn
		}

		kv := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", sw.bytes,
			"ip", netutil.ClientIP(r),
		}
		if ua := r.UserAgent(); ua != "" {
			kv = append(kv, "ua", util.Truncate(ua, 120))
		}
		if rid := logger.RequestIDOf(r); rid != "" {
			kv = append(kv, "request_id", rid)
		}
		if st := ReqStateFrom(r.Context()); st != nil {
			if user, viaAPI := st.auth(); user != "" {
				kv = append(kv, "user", user)
				if viaAPI {
					kv = append(kv, "via", "api_key")
				}
			}
		}
		logger.LogAt(r.Context(), lvl, "http_request", kv...)
	})
}

// headerTracker 记录响应头是否已经写出。
type headerTracker struct {
	http.ResponseWriter
	wrote bool
}

func (t *headerTracker) WriteHeader(code int) {
	if !t.wrote {
		t.wrote = true
		t.ResponseWriter.WriteHeader(code)
	}
}

func (t *headerTracker) Write(b []byte) (int, error) {
	if !t.wrote {
		t.wrote = true
	}
	return t.ResponseWriter.Write(b)
}

// withRecover 捕获 handler 里的 panic。
//
// 容器环境里 panic = 进程退出 = 容器重启，用户侧表现为随机 502。
// 所以必须兜住：写 error 日志（含堆栈），能改状态码就返回 500。
func WithRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ht := &headerTracker{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				logger.LogAt(r.Context(), logger.LevelError, "panic_recovered",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprint(rec),
					"stack", logger.StackTrace(),
				)
				if !ht.wrote {
					// 还没开始写响应，可以给一个体面的 500。
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.Header().Set("Cache-Control", "no-store")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"服务器内部错误，请稍后重试"}`))
				}
			}
		}()
		next.ServeHTTP(ht, r)
	})
}

type ctxKey string
