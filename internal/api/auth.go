package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/middleware"
	"wxcode/internal/model"
	"wxcode/internal/netutil"
	"wxcode/internal/service"
	"wxcode/internal/store"
	"wxcode/internal/util"
)

// ============================================================
// 认证：会话 Cookie、API Key、权限中间件、用户端接口
//
// 两种身份来源，优先级：API Key > 会话 Cookie
//   - API Key（X-API-Key / Authorization: Bearer / ?key=）：给程序调用，
//     一定消耗额度；
//   - 会话 Cookie（wx_sid）：给浏览器用，是否消耗额度看配置。
// ============================================================

type ctxKey string

const authCtxKey ctxKey = "wxcode-auth"

// authInfo 是一次请求的身份快照。
type authInfo struct {
	user   *model.User // nil = 匿名
	viaAPI bool        // true = 用 Key 调用
}

// withAuth 解析身份放进 context；Key 无效直接 401，不会"降级"成匿名。
func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ai *authInfo
		if key := httpx.APIKeyFrom(r); key != "" {
			u, err := store.GetUserByAPIKey(key)
			if err != nil {
				httpx.WriteErr(w, http.StatusUnauthorized, errors.New("API Key 无效"))
				return
			}
			if u.IsBanned() {
				httpx.WriteErr(w, http.StatusForbidden, errUserBanned)
				return
			}
			ai = &authInfo{user: u, viaAPI: true}
		} else if c, err := r.Cookie(store.SessionCookie); err == nil {
			if u := store.UserBySession(c.Value); u != nil {
				ai = &authInfo{user: u}
			}
		}
		// 把身份写进请求状态，访问日志就能带上用户名
		if ai != nil && ai.user != nil {
			middleware.ReqStateFrom(r.Context()).SetAuth(ai.user.Username, ai.viaAPI)
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authCtxKey, ai)))
	}
}

func authOf(r *http.Request) *authInfo {
	if v, ok := r.Context().Value(authCtxKey).(*authInfo); ok && v != nil {
		return v
	}
	return &authInfo{}
}

func currentUser(r *http.Request) *model.User { return authOf(r).user }

// requireLogin 未登录返回 401（前端据此跳登录页）。
func requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r) == nil {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "请先登录", "needLogin": true,
			})
			return
		}
		next(w, r)
	}
}

// requireAdmin 非管理员一律 404 —— 后台是否存在对普通用户不可探测。
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		if u == nil || !u.IsAdmin() {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, sid string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     store.SessionCookie,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   httpx.CookieSecure(r),
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     store.SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   httpx.CookieSecure(r),
		MaxAge:   -1,
	})
}

// ---------- 请求体 ----------
const maxJSONBodySmall = 64 << 10

func decodeJSON(r *http.Request, v any) error {
	raw, err := httpx.ReadBody(r.Body, maxJSONBodySmall)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("请求体为空")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("JSON 解析失败: %w", err)
	}
	return nil
}

var errUserBanned = errors.New("账号已被封禁，请联系管理员")

var errBadLogin = errors.New("用户名或密码错误")

var errQuotaNoLeft = errors.New("可用次数已用完，请兑换卡密或联系管理员增加额度")

var errBadUsername = errors.New("用户名不合法（3~32 位，仅字母数字下划线中划线）")

var errWeakPassword = errors.New("密码至少 6 位")

// ============================================================
// 用户端接口
// ============================================================

// handleRegister 注册。是否开放 / 是否需要邀请码 / 是否必须验证邮箱由后台配置决定。
// POST /api/register  {username, password, inviteCode?, email?, emailCode?, captchaId, captchaCode}
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	st := store.LoadSettings()
	if !st.RegisterOpen {
		httpx.WriteErr(w, http.StatusForbidden, errors.New("管理员已关闭注册"))
		return
	}

	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		InviteCode  string `json:"inviteCode"`
		Email       string `json:"email"`
		EmailCode   string `json:"emailCode"`
		CaptchaID   string `json:"captchaId"`
		CaptchaCode string `json:"captchaCode"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if st.CaptchaEnabled {
		if err := service.CheckCaptcha(body.CaptchaID, body.CaptchaCode); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if !netutil.RegisterLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "3600")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("注册尝试过于频繁，请稍后再试"))
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if !util.ValidUsername(body.Username) {
		httpx.WriteErr(w, http.StatusBadRequest, errBadUsername)
		return
	}
	if len(body.Password) < 6 {
		httpx.WriteErr(w, http.StatusBadRequest, errWeakPassword)
		return
	}
	// 先查用户名，避免用户费劲验证完邮箱才发现用户名被占（也少浪费邀请码）
	if _, err := store.GetUserByUsername(body.Username); err == nil {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("用户名已存在"))
		return
	}

	// 强制邮箱注册：必须完成邮箱验证码校验才允许建号。
	// 开关关闭时完全跳过 —— 老流程、老用户都不受影响，也不会把未验证的邮箱写进账号。
	email := ""
	if st.RegisterEmailRequired {
		if !st.SMTPReady() {
			httpx.WriteErr(w, http.StatusServiceUnavailable, service.ErrRegisterMailNotReady)
			return
		}
		email = util.NormalizeEmail(body.Email)
		if !util.ValidEmail(email) {
			httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
			return
		}
		if strings.TrimSpace(body.EmailCode) == "" {
			httpx.WriteErr(w, http.StatusBadRequest, service.ErrRegisterMailNeeded)
			return
		}
		taken, err := store.EmailTakenByOther(email, 0)
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return
		}
		if taken {
			httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailTaken)
			return
		}
		if err := store.VerifyEmailCode(service.EmailPurposeRegister, service.EmailAddrKey(email), body.EmailCode); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
	}

	if st.InviteRequired {
		if err := store.ConsumeInvite(strings.TrimSpace(body.InviteCode)); err != nil {
			httpx.WriteErr(w, http.StatusForbidden, err)
			return
		}
	}

	hash, err := service.HashPassword(body.Password)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	u, err := store.CreateUser(body.Username, hash, "user", nil)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if email != "" {
		// 建号后、写邮箱前再做一次占用检查：两个请求同时验证同一邮箱时，
		// 后到的那个会在这里看到先到的账号，从而回滚，不会留下重复邮箱。
		taken, err := store.EmailTakenByOther(email, u.ID)
		if err != nil {
			_ = store.DeleteUser(u.ID)
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return
		}
		if taken {
			_ = store.DeleteUser(u.ID)
			httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailTaken)
			return
		}
		if err := store.SetUserEmail(u.ID, email); err != nil {
			// 建号成功但写邮箱失败：把账号删掉，别留一个"注册了但没邮箱"的半成品
			_ = store.DeleteUser(u.ID)
			httpx.WriteErr(w, http.StatusInternalServerError, fmt.Errorf("保存邮箱失败: %w", err))
			return
		}
		store.DeleteEmailCodes(service.EmailPurposeRegister, service.EmailAddrKey(email))
		u, _ = store.GetUserByID(u.ID)
	}
	sid, err := store.CreateSession(u.ID, false, netutil.ClientIP(r), r.UserAgent())
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	setSessionCookie(w, r, sid, store.SessionTTL)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u.PublicView(), "settings": store.LoadSettings()})
}

// handleLogin 登录。管理员走同一个入口，只是 role 不同。
// POST /api/login
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		CaptchaID   string `json:"captchaId"`
		CaptchaCode string `json:"captchaCode"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if store.LoadSettings().CaptchaEnabled {
		if err := service.CheckCaptcha(body.CaptchaID, body.CaptchaCode); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
	}
	// 防爆破：单 IP 每分钟最多 authRatePerMinute 次尝试（无论成功失败都计数）
	if !netutil.AuthLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("登录尝试过于频繁，请稍后再试"))
		return
	}
	u, err := store.GetUserByUsername(strings.TrimSpace(body.Username))
	if err != nil {
		// 已绑定邮箱的用户可以用邮箱当账号登录：只在用户名查不到时才回退，
		// 用户名优先，避免某个长得像邮箱的用户名被邮箱匹配抢走。
		login := strings.TrimSpace(body.Username)
		if strings.Contains(login, "@") {
			u, err = store.GetUserByEmail(login)
			if errors.Is(err, store.ErrEmailAmbiguous) {
				// 同一邮箱绑了多个账号：必须让用户改用用户名，不能随机登进其中一个
				httpx.WriteErr(w, http.StatusConflict, store.ErrEmailAmbiguous)
				return
			}
		}
	}
	if err != nil || !service.CheckPassword(u, body.Password) {
		// 不区分"用户不存在"和"密码错误"，避免被用来枚举用户名
		httpx.WriteErr(w, http.StatusUnauthorized, errBadLogin)
		return
	}
	if u.IsBanned() {
		httpx.WriteErr(w, http.StatusForbidden, errUserBanned)
		return
	}
	_ = store.TouchLogin(u.ID, netutil.ClientIP(r))
	u.LastLoginAt = store.NowStr()
	u.LastIP = netutil.ClientIP(r)

	sid, err := store.CreateSession(u.ID, u.IsAdmin(), netutil.ClientIP(r), r.UserAgent())
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	ttl := store.SessionTTL
	if u.IsAdmin() {
		ttl = store.AdminTTL
	}
	setSessionCookie(w, r, sid, ttl)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u.PublicView(), "settings": store.LoadSettings()})
}

// handleLogout 登出（幂等）。
// POST /api/logout
func handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(store.SessionCookie); err == nil {
		store.DeleteSession(c.Value)
	}
	clearSessionCookie(w, r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe 当前身份 + 前台需要的配置。匿名也返回 200（user 为 null）。
// GET /api/me
func handleMe(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	// 用 publicSettings：底部统计代码这类管理配置不下发给访客
	resp := map[string]any{"settings": store.PublicSettings()}
	if u == nil {
		resp["user"] = nil
	} else {
		resp["user"] = u.PublicView()
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// handleChangePassword 修改自己的密码。
// POST /api/me/password {oldPassword, newPassword}
func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if !service.CheckPassword(u, body.OldPassword) {
		httpx.WriteErr(w, http.StatusForbidden, errors.New("原密码不正确"))
		return
	}
	if len(body.NewPassword) < 6 {
		httpx.WriteErr(w, http.StatusBadRequest, errWeakPassword)
		return
	}
	hash, err := service.HashPassword(body.NewPassword)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := store.SetPassword(u.ID, hash); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	// 改密后踢掉自己以外的所有会话（保留当前这条）
	if c, err := r.Cookie(store.SessionCookie); err == nil {
		_ = store.DeleteOtherSessions(u.ID, c.Value)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRefreshKey 一键刷新 API Key（旧 key 立即失效）。
// POST /api/me/key
func handleRefreshKey(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	key, err := store.RefreshAPIKey(u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "apiKey": key})
}

// handleMyRecords 自己的生成/解析记录。
// GET /api/me/records?limit=50&offset=0&kind=gen
func handleMyRecords(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := store.ListRecords(store.RecordFilter{
		UserID: u.ID,
		Kind:   r.URL.Query().Get("kind"),
		Limit:  limit, Offset: offset,
	})
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}
