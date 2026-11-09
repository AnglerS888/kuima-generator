package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/logger"
	"wxcode/internal/model"
	"wxcode/internal/netutil"
	"wxcode/internal/service"
	"wxcode/internal/store"
	"wxcode/internal/util"
)

// ---------- 注册邮箱验证 ----------

// handleRegisterEmailSend 注册页「发送验证码」。
//
// POST /api/register/email/send  {email}
//
// 只有在「开放注册 + 强制邮箱注册 + 发信配置完整」时才发信；
// 邮箱已绑定其他账号时直接拒绝，省得用户填完验证码才失败。
func handleRegisterEmailSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	st := store.LoadSettings()
	if !st.RegisterOpen {
		httpx.WriteErr(w, http.StatusForbidden, errors.New("管理员已关闭注册"))
		return
	}
	if !st.RegisterEmailRequired {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrRegisterMailOff)
		return
	}
	if !st.SMTPReady() {
		httpx.WriteErr(w, http.StatusServiceUnavailable, service.ErrRegisterMailNotReady)
		return
	}
	if !netutil.EmailLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("操作过于频繁，请稍后再试"))
		return
	}

	email := util.NormalizeEmail(body.Email)
	if !util.ValidEmail(email) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
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

	subject := service.EmailAddrKey(email)
	code, err := store.IssueEmailCode(service.EmailPurposeRegister, subject, email)
	if err != nil {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, err)
		return
	}
	mailSubject := "【" + st.SiteName + "】注册验证码"
	if err := service.MailSender(st.SMTP(), email, mailSubject, service.RegisterMailText(st.SiteName, code)); err != nil {
		store.DeleteEmailCodes(service.EmailPurposeRegister, subject)
		logger.LogCtx(r.Context(), logger.LevelError, "发送注册验证码失败",
			"email", util.MaskEmail(email),
			"error", err.Error(),
		)
		httpx.WriteErr(w, http.StatusBadGateway, service.ErrEmailSendFail)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"email":     util.MaskEmail(email),
		"expiresIn": int(store.EmailCodeTTL.Seconds()),
		"retryIn":   int(util.EmailCodeResend.Seconds()),
	})
}

// ---------- 用户端接口 ----------

// handleForgotPasswordSend 忘记密码第一步：按用户名给已绑定邮箱发验证码。
//
// POST /api/password/forgot  {username}
//
// 为了不把「用户名是否存在」暴露出去，用户不存在与未绑定邮箱返回同一句话。
// 开关关闭 / 发信未配置则是全局状态，可以如实说明。
func handleForgotPasswordSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	st := store.LoadSettings()
	if condErr := emailResetGuard(st); condErr != nil {
		httpx.WriteErr(w, http.StatusForbidden, condErr)
		return
	}
	if !netutil.EmailLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("操作过于频繁，请稍后再试"))
		return
	}

	u, err := store.GetUserByUsername(strings.TrimSpace(body.Username))
	if err != nil || strings.TrimSpace(u.Email) == "" || !util.ValidEmail(u.Email) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailNoBound)
		return
	}
	if u.IsBanned() {
		httpx.WriteErr(w, http.StatusForbidden, errUserBanned)
		return
	}

	code, err := store.IssueEmailCode(service.EmailPurposeReset, service.EmailSubjectKey(u.ID), u.Email)
	if err != nil {
		// 冷却 / 次数超限都是「稍后再试」，用 429 让前端能区分
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, err)
		return
	}
	subject := "【" + st.SiteName + "】找回密码验证码"
	if err := service.MailSender(st.SMTP(), u.Email, subject, service.ResetPasswordMailText(st.SiteName, u.Username, code)); err != nil {
		store.DeleteEmailCodes(service.EmailPurposeReset, service.EmailSubjectKey(u.ID))
		logger.LogCtx(r.Context(), logger.LevelError, "发送找回密码邮件失败",
			"user_id", u.ID,
			"email", util.MaskEmail(u.Email),
			"error", err.Error(),
		)
		httpx.WriteErr(w, http.StatusBadGateway, service.ErrEmailSendFail)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"email":     util.MaskEmail(u.Email),
		"expiresIn": int(store.EmailCodeTTL.Seconds()),
		"retryIn":   int(util.EmailCodeResend.Seconds()),
	})
}

// handleForgotPasswordReset 忘记密码第二步：验证码 + 新密码。
//
// POST /api/password/reset  {username, code, newPassword}
//
// 改密成功后会踢掉该用户的全部会话 —— 找回密码的常见诉求就是「把别人踹下去」。
func handleForgotPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		Code        string `json:"code"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if condErr := emailResetGuard(store.LoadSettings()); condErr != nil {
		httpx.WriteErr(w, http.StatusForbidden, condErr)
		return
	}
	if len(body.NewPassword) < 6 {
		httpx.WriteErr(w, http.StatusBadRequest, errWeakPassword)
		return
	}
	if !netutil.EmailLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("操作过于频繁，请稍后再试"))
		return
	}

	u, err := store.GetUserByUsername(strings.TrimSpace(body.Username))
	if err != nil || strings.TrimSpace(u.Email) == "" {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailNoBound)
		return
	}
	if u.IsBanned() {
		httpx.WriteErr(w, http.StatusForbidden, errUserBanned)
		return
	}
	if err := store.VerifyEmailCode(service.EmailPurposeReset, service.EmailSubjectKey(u.ID), body.Code); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
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
	_ = store.DeleteUserSessions(u.ID)
	store.DeleteAllEmailCodes(service.EmailSubjectKey(u.ID))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// emailResetGuard 统一判断「系统层面能不能走邮箱找回」。
func emailResetGuard(st model.Settings) error {
	if !st.EmailVerifyEnabled {
		return service.ErrEmailDisabled
	}
	if !st.SMTPReady() {
		return service.ErrEmailNotReady
	}
	return nil
}

// handleMyEmailSend 已登录用户给自己的邮箱发绑定验证码。
// POST /api/me/email/send  {email}
func handleMyEmailSend(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	st := store.LoadSettings()
	if condErr := emailResetGuard(st); condErr != nil {
		httpx.WriteErr(w, http.StatusForbidden, condErr)
		return
	}
	email := util.NormalizeEmail(body.Email)
	if !util.ValidEmail(email) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
		return
	}
	if email == util.NormalizeEmail(u.Email) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailSame)
		return
	}
	taken, err := store.EmailTakenByOther(email, u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if taken {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailTaken)
		return
	}

	code, err := store.IssueEmailCode(store.EmailPurposeBind, service.EmailSubjectKey(u.ID), email)
	if err != nil {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, err)
		return
	}
	subject := "【" + st.SiteName + "】绑定邮箱验证码"
	if err := service.MailSender(st.SMTP(), email, subject, service.BindEmailMailText(st.SiteName, u.Username, code)); err != nil {
		store.DeleteEmailCodes(store.EmailPurposeBind, service.EmailSubjectKey(u.ID))
		logger.LogCtx(r.Context(), logger.LevelError, "发送绑定邮箱邮件失败",
			"user_id", u.ID,
			"email", util.MaskEmail(email),
			"error", err.Error(),
		)
		httpx.WriteErr(w, http.StatusBadGateway, service.ErrEmailSendFail)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"email":     util.MaskEmail(email),
		"expiresIn": int(store.EmailCodeTTL.Seconds()),
		"retryIn":   int(util.EmailCodeResend.Seconds()),
	})
}

// handleMyEmailBind 绑定邮箱：验证码通过后落到 users.email。
// POST /api/me/email/bind  {email, code}
func handleMyEmailBind(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if condErr := emailResetGuard(store.LoadSettings()); condErr != nil {
		httpx.WriteErr(w, http.StatusForbidden, condErr)
		return
	}
	email := util.NormalizeEmail(body.Email)
	if !util.ValidEmail(email) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
		return
	}
	if err := store.VerifyEmailCode(store.EmailPurposeBind, service.EmailSubjectKey(u.ID), body.Code); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	// 验证码有效期间别人可能刚绑了同一邮箱，落库前再查一次
	taken, err := store.EmailTakenByOther(email, u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if taken {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailTaken)
		return
	}
	if err := store.SetUserEmail(u.ID, email); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	store.DeleteAllEmailCodes(service.EmailSubjectKey(u.ID))
	fresh, err := store.GetUserByID(u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": fresh.PublicView()})
}

// handleMyEmailUnbind 解绑邮箱（需要当前密码，防止会话被盗后直接改走找回通道）。
// POST /api/me/email/unbind  {password}
func handleMyEmailUnbind(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if !service.CheckPassword(u, body.Password) {
		httpx.WriteErr(w, http.StatusForbidden, service.ErrEmailPassWrong)
		return
	}
	if err := store.SetUserEmail(u.ID, ""); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	store.DeleteAllEmailCodes(service.EmailSubjectKey(u.ID))
	fresh, err := store.GetUserByID(u.ID)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": fresh.PublicView()})
}

// ---------- 管理端：测试发信 ----------

// handleAdminTestEmail 用「当前表单值」试发一封邮件，不落库。
// 密码留空时回落到已保存的授权码，方便只改收件人就能测。
//
// POST /api/admin/email/test  {to, host?, port?, user?, pass?, from?, fromName?, tls?}
func handleAdminTestEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To       string `json:"to"`
		Host     string `json:"host"`
		Port     *int   `json:"port"`
		User     string `json:"user"`
		Pass     string `json:"pass"`
		From     string `json:"from"`
		FromName string `json:"fromName"`
		TLS      string `json:"tls"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	st := store.LoadSettings()
	cfg := st.SMTP()
	if v := strings.TrimSpace(body.Host); v != "" {
		cfg.Host = v
	}
	if body.Port != nil && *body.Port > 0 {
		cfg.Port = *body.Port
	}
	if v := strings.TrimSpace(body.User); v != "" {
		cfg.User = v
	}
	if v := strings.TrimSpace(body.Pass); v != "" {
		cfg.Pass = v
	}
	if v := strings.TrimSpace(body.From); v != "" {
		cfg.From = v
	}
	if v := strings.TrimSpace(body.FromName); v != "" {
		cfg.FromName = v
	}
	if v := strings.TrimSpace(body.TLS); v != "" {
		cfg.TLS = model.NormalizeSMTPTLS(v)
	}
	if cfg.User == "" {
		cfg.Pass = ""
	}
	to := util.NormalizeEmail(body.To)
	if to == "" {
		to = st.SMTPFrom
	}
	if !util.ValidEmail(to) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
		return
	}

	start := time.Now()
	subject := "【" + st.SiteName + "】测试邮件"
	text := "这是一封来自 " + st.SiteName + " 的测试邮件。\r\n\r\n" +
		"收到它说明发信配置可用，可以开启邮箱验证了。\r\n" +
		"发送时间：" + time.Now().Format("2006-01-02 15:04:05") + "\r\n"
	if err := service.MailSender(cfg, to, subject, text); err != nil {
		httpx.WriteErr(w, http.StatusBadGateway, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "to": to, "ms": time.Since(start).Milliseconds(),
	})
}
