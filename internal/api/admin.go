package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"wxcode/internal/config"
	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/logger"
	"wxcode/internal/middleware"
	"wxcode/internal/model"
	"wxcode/internal/service"
	"wxcode/internal/store"
	"wxcode/internal/util"
)

// ============================================================
// 管理端接口：/api/admin/*
//
// 全部套 withAuth + requireAdmin：非管理员拿到的是 404，
// 连"这个后台存不存在"都探测不到。
// ============================================================

// handleAdminOverview 后台首页数据：用户数、记录数、当前配置。
// GET /api/admin/overview
func handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"stats":    store.RecordStats(),
		"settings": store.LoadSettings(),
		"logging":  logConfigView(),
		"system":   collectSystemInfo(),
	})
}

// logConfigView 返回当前日志配置（给后台展示）。
// 只暴露读得到的信息，不含任何敏感内容。
func logConfigView() map[string]any {
	cfg := logger.CurrentLogConfig()
	return map[string]any{
		"level":   cfg.Level().String(),
		"format":  cfg.Format(),
		"output":  cfg.Output(),
		"access":  cfg.AccessLog(),
		"skip":    sortedKeys(cfg.SkipPaths()),
		"version": config.Version,
		"build":   config.BuildTime,
	}
}

// sortedKeys 把集合的键排序后返回，保证输出稳定（便于测试与前端比较）。
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// handleAdminGetSettings 读配置。
// GET /api/admin/settings
func handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"settings": store.LoadSettings()})
}

// handleAdminSaveSettings 改配置。
// PUT /api/admin/settings
func handleAdminSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RegisterOpen          *bool   `json:"registerOpen"`
		InviteRequired        *bool   `json:"inviteRequired"`
		RequireLogin          *bool   `json:"requireLogin"`
		CaptchaEnabled        *bool   `json:"captchaEnabled"`
		DefaultQuota          *int    `json:"defaultQuota"`
		SiteName              *string `json:"siteName"`
		FooterHTML            *string `json:"footerHtml"`
		SiteKeywords          *string `json:"siteKeywords"`
		SiteAnnounce          *string `json:"siteAnnounce"`
		CORSOrigins           *string `json:"corsOrigins"`
		RecordRetentionDays   *int    `json:"recordRetentionDays"`
		RecordEnabled         *bool   `json:"recordEnabled"`
		RecordPayloadMode     *string `json:"recordPayloadMode"`
		AnonLogoKB            *int    `json:"anonLogoKb"`
		UserLogoKB            *int    `json:"userLogoKb"`
		AnonImageKB           *int    `json:"anonImageKb"`
		UserImageKB           *int    `json:"userImageKb"`
		EmailVerifyEnabled    *bool   `json:"emailVerifyEnabled"`
		RegisterEmailRequired *bool   `json:"registerEmailRequired"`
		SMTPHost              *string `json:"smtpHost"`
		SMTPPort              *int    `json:"smtpPort"`
		SMTPUser              *string `json:"smtpUser"`
		SMTPPass              *string `json:"smtpPass"`
		SMTPPassClear         *bool   `json:"smtpPassClear"`
		SMTPFrom              *string `json:"smtpFrom"`
		SMTPFromName          *string `json:"smtpFromName"`
		SMTPTLS               *string `json:"smtpTls"`

		WatermarkMode     *string `json:"watermarkMode"`
		WatermarkText     *string `json:"watermarkText"`
		WatermarkSize     *int    `json:"watermarkSize"`
		WatermarkColor    *string `json:"watermarkColor"`
		WatermarkOpacity  *int    `json:"watermarkOpacity"`
		WatermarkPosition *string `json:"watermarkPosition"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	set := func(k, v string) error { return store.SetSetting(k, v) }
	if body.RegisterOpen != nil {
		_ = set(store.SetRegisterOpen, boolStr(*body.RegisterOpen))
	}
	if body.InviteRequired != nil {
		_ = set(store.SetInviteRequired, boolStr(*body.InviteRequired))
	}
	if body.RequireLogin != nil {
		_ = set(store.SetRequireLogin, boolStr(*body.RequireLogin))
	}
	if body.CaptchaEnabled != nil {
		_ = set(store.SetCaptchaEnabled, boolStr(*body.CaptchaEnabled))
	}
	if body.DefaultQuota != nil {
		q := *body.DefaultQuota
		if q < 0 {
			q = 0
		}
		if q > 1_000_000 {
			q = 1_000_000
		}
		_ = set(store.SetDefaultQuota, strconv.Itoa(q))
	}
	if body.SiteName != nil {
		name := util.Truncate(strings.TrimSpace(*body.SiteName), 40)
		if name == "" {
			name = "葵码生成器"
		}
		_ = set(store.SetSiteName, name)
	}
	if body.FooterHTML != nil {
		// 统计代码原样保存（会插到 </body> 前执行），只做长度限制防止把库撑爆
		code := *body.FooterHTML
		if len(code) > maxFooterHTML {
			httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("统计代码太长（上限 %d 字节）", maxFooterHTML))
			return
		}
		_ = set(store.SetFooterHTML, code)
	}
	if body.SiteKeywords != nil {
		kw := strings.TrimSpace(*body.SiteKeywords)
		if utf8.RuneCountInString(kw) > maxKeywordsLen {
			httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("搜索关键词最多 %d 个字符", maxKeywordsLen))
			return
		}
		_ = set(store.SetSiteKeywords, kw)
	}
	if body.SiteAnnounce != nil {
		// 公告按 HTML 原样注入（后台可写标签），这里只做长度限制
		text := strings.TrimSpace(strings.ReplaceAll(*body.SiteAnnounce, "\r\n", "\n"))
		if utf8.RuneCountInString(text) > maxAnnounceLen {
			httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("网站公告最多 %d 个字符", maxAnnounceLen))
			return
		}
		_ = set(store.SetSiteAnnounce, text)
	}
	// 跨域来源白名单：逗号分隔。留空 = 不跨域（默认）。
	// 只接受 http(s):// 开头或 *，避免把乱七八糟的字符串写进去。
	if body.CORSOrigins != nil {
		_ = set(store.SetCORSOrigins, middleware.NormalizeCORSOrigins(*body.CORSOrigins))
	}
	if body.RecordEnabled != nil {
		_ = set(store.SetRecordEnabled, boolStr(*body.RecordEnabled))
	}
	if body.RecordPayloadMode != nil {
		_ = set(store.SetRecordPayloadMode, store.NormalizeRecordPayloadMode(*body.RecordPayloadMode))
	}
	// 记录保留天数：0 = 永久保留（默认）。越界统一夹到 [0, 3650]。
	if body.RecordRetentionDays != nil {
		d := *body.RecordRetentionDays
		if d < 0 {
			d = 0
		}
		if d > service.RetentionMaxDays {
			d = service.RetentionMaxDays
		}
		_ = set(store.SetRecordRetentionDays, strconv.Itoa(d))
	}
	// 上传大小上限（KB）：≤0 表示恢复默认；越界统一夹到 [128, 65536]
	uploadDefaults := map[string]int{
		store.SetAnonLogoKB:  8192,
		store.SetUserLogoKB:  8192,
		store.SetAnonImageKB: 24576,
		store.SetUserImageKB: 24576,
	}
	for _, item := range []struct {
		val *int
		key string
	}{
		{body.AnonLogoKB, store.SetAnonLogoKB},
		{body.UserLogoKB, store.SetUserLogoKB},
		{body.AnonImageKB, store.SetAnonImageKB},
		{body.UserImageKB, store.SetUserImageKB},
	} {
		if item.val == nil {
			continue
		}
		v := *item.val
		if v <= 0 {
			v = uploadDefaults[item.key]
		}
		_ = set(item.key, strconv.Itoa(clampUploadKB(v)))
	}

	// 邮箱验证 / 发信配置。
	//
	// 先算出「合并后」的结果再校验：管理员可能只改其中一项（比如换端口），
	// 其余字段沿用已保存的值。只有当最终状态是「开启」时才强制要求配置完整 ——
	// 这样不会出现「开关是开的、但发不出去信」的假可用状态。
	host := store.GetSetting(store.SetSMTPHost, "")
	port := store.SettingInt(store.SetSMTPPort, store.SMTPDefaultPort)
	user := store.GetSetting(store.SetSMTPUser, "")
	pass := store.GetSetting(store.SetSMTPPass, "")
	from := store.GetSetting(store.SetSMTPFrom, "")
	fromName := store.GetSetting(store.SetSMTPFromName, "")
	tlsMode := model.NormalizeSMTPTLS(store.GetSetting(store.SetSMTPTLS, store.SMTPTLSDefault))
	enabled := store.SettingBool(store.SetEmailVerify)
	registerMail := store.SettingBool(store.SetRegisterMail)

	if body.SMTPHost != nil {
		host = strings.TrimSpace(*body.SMTPHost)
	}
	if body.SMTPPort != nil {
		port = *body.SMTPPort
	}
	if body.SMTPUser != nil {
		user = strings.TrimSpace(*body.SMTPUser)
	}
	if body.SMTPPassClear != nil && *body.SMTPPassClear {
		pass = ""
	} else if body.SMTPPass != nil && strings.TrimSpace(*body.SMTPPass) != "" {
		pass = *body.SMTPPass
	}
	if body.SMTPFrom != nil {
		from = strings.TrimSpace(*body.SMTPFrom)
	}
	if body.SMTPFromName != nil {
		fromName = strings.TrimSpace(*body.SMTPFromName)
	}
	if body.SMTPTLS != nil {
		tlsMode = model.NormalizeSMTPTLS(*body.SMTPTLS)
	}
	if body.EmailVerifyEnabled != nil {
		enabled = *body.EmailVerifyEnabled
	}
	if body.RegisterEmailRequired != nil {
		registerMail = *body.RegisterEmailRequired
	}

	if utf8.RuneCountInString(host) > 255 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("SMTP 服务器地址过长"))
		return
	}
	if utf8.RuneCountInString(user) > util.EmailMaxLen {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("SMTP 账号最多 120 个字符"))
		return
	}
	if utf8.RuneCountInString(pass) > 200 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("SMTP 密码/授权码最多 200 个字符"))
		return
	}
	if from != "" && !util.ValidEmail(from) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
		return
	}
	if utf8.RuneCountInString(fromName) > 60 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("发信人名称最多 60 个字符"))
		return
	}
	if strings.ContainsAny(fromName, "\r\n") {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("发信人名称不能包含换行"))
		return
	}
	if enabled || registerMail {
		cfg := model.SMTPConfig{Host: host, Port: port, User: user, Pass: pass, From: from, FromName: fromName, TLS: tlsMode}
		if err := cfg.Validate(); err != nil {
			hint := "开启邮箱验证前请先完成发信配置：%v"
			if registerMail && !enabled {
				hint = "开启邮箱注册验证前请先完成发信配置：%v"
			}
			httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf(hint, err))
			return
		}
	}
	if pass == "" {
		// 空密码没必要占一行配置；清掉反而让「是否已设置」的判断更直观
		_ = set(store.SetSMTPPass, "")
	} else {
		_ = set(store.SetSMTPPass, pass)
	}
	_ = set(store.SetSMTPHost, host)
	_ = set(store.SetSMTPPort, strconv.Itoa(port))
	_ = set(store.SetSMTPUser, user)
	_ = set(store.SetSMTPFrom, from)
	_ = set(store.SetSMTPFromName, fromName)
	_ = set(store.SetSMTPTLS, tlsMode)
	_ = set(store.SetEmailVerify, boolStr(enabled))
	_ = set(store.SetRegisterMail, boolStr(registerMail))

	// 水印：先整体校验，再逐项落库（避免存进半套配置）
	cur := store.LoadSettings()
	wm, err := validateWatermarkSettings(body.WatermarkMode, body.WatermarkText,
		body.WatermarkSize, body.WatermarkOpacity, body.WatermarkColor, body.WatermarkPosition, cur)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if body.WatermarkMode != nil {
		_ = set(store.SetWatermarkMode, wm.WatermarkMode)
	}
	if body.WatermarkText != nil {
		_ = set(store.SetWatermarkText, wm.WatermarkText)
	}
	if body.WatermarkSize != nil {
		_ = set(store.SetWatermarkSize, strconv.Itoa(wm.WatermarkSize))
	}
	if body.WatermarkColor != nil {
		_ = set(store.SetWatermarkColor, wm.WatermarkColor)
	}
	if body.WatermarkOpacity != nil {
		_ = set(store.SetWatermarkOpacity, strconv.Itoa(wm.WatermarkOpacity))
	}
	if body.WatermarkPosition != nil {
		_ = set(store.SetWatermarkPosition, wm.WatermarkPosition)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": store.LoadSettings()})
}

const maxKeywordsLen = 200 // 搜索关键词上限（字符）

const maxAnnounceLen = 2000 // 网站公告上限（字符，含 HTML 标签）

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// userAdminView 是管理端列表里的一行，比 publicView 多几个运营字段。
func userAdminView(u *model.User) map[string]any {
	v := u.PublicView()
	v["apiKeyMasked"] = maskKey(u.APIKey)
	return v
}

func maskKey(k string) string {
	if len(k) <= 10 {
		return k
	}
	return k[:8] + "…" + k[len(k)-4:]
}

// handleAdminListUsers 用户列表（可按用户名/邮箱搜索、按状态过滤）。
// GET /api/admin/users?q=&status=&limit=&offset=
func handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	users, total, err := store.ListUsers(q, status, limit, offset)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	items := []map[string]any{}
	for _, u := range users {
		items = append(items, userAdminView(u))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// handleAdminCreateUser 后台直接建号（注册关闭时也能加人）。
// POST /api/admin/users
func handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Note     string `json:"note"`
		Quota    *int   `json:"quota"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
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
	role := "user"
	if body.Role == "admin" {
		role = "admin"
	}
	email := util.NormalizeEmail(body.Email)
	if email != "" && !util.ValidEmail(email) {
		httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
		return
	}
	hash, err := service.HashPassword(body.Password)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	u, err := store.CreateUser(body.Username, hash, role, body.Quota)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if email != "" || body.Note != "" {
		_ = store.UpdateUserProfile(u.ID,
			util.Truncate(email, 120), util.Truncate(strings.TrimSpace(body.Note), 200))
		u, _ = store.GetUserByID(u.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": userAdminView(u)})
}

// handleAdminUpdateUser 改单个用户：额度、状态、邮箱、备注。
// PUT /api/admin/users/{id}
func handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("用户 id 不合法"))
		return
	}
	u, err := store.GetUserByID(id)
	if err != nil {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("用户不存在"))
		return
	}
	var body struct {
		Email  *string `json:"email"`
		Note   *string `json:"note"`
		Quota  *int    `json:"quota"`
		Status *string `json:"status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if body.Email != nil {
		email := util.NormalizeEmail(*body.Email)
		if email != "" && !util.ValidEmail(email) {
			httpx.WriteErr(w, http.StatusBadRequest, service.ErrEmailFormat)
			return
		}
		u.Email = util.Truncate(email, 120)
	}
	if body.Note != nil {
		u.Note = util.Truncate(strings.TrimSpace(*body.Note), 200)
	}
	if body.Quota != nil {
		q := *body.Quota
		if q < 0 {
			q = 0
		}
		if q > 10_000_000 {
			q = 10_000_000
		}
		u.Quota = q
	}
	if body.Status != nil {
		s := strings.TrimSpace(*body.Status)
		if s != "active" && s != "banned" {
			httpx.WriteErr(w, http.StatusBadRequest, errors.New("status 只能是 active / banned"))
			return
		}
		u.Status = s
	}
	err = store.UpdateUserFields(u)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if u.IsBanned() {
		// 封禁立即生效：踢掉所有会话，API Key 也会在 withAuth 里被拒
		_ = store.DeleteUserSessions(u.ID)
	}
	if body.Email != nil {
		// 邮箱被管理员改过：旧的验证码可能还发往老地址，一并作废
		store.DeleteAllEmailCodes(service.EmailSubjectKey(u.ID))
	}
	fresh, _ := store.GetUserByID(u.ID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": userAdminView(fresh)})
}

// handleAdminResetPassword 重置密码。
// POST /api/admin/users/{id}/password
func handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if len(body.Password) < 6 {
		httpx.WriteErr(w, http.StatusBadRequest, errWeakPassword)
		return
	}
	hash, err := service.HashPassword(body.Password)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	ok, err := store.ResetPassword(id, hash)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("用户不存在"))
		return
	}
	_ = store.DeleteUserSessions(id)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminPurgeRecords 手动清理记录（后台「立即清理」按钮）。
//
// 支持两种清理方式：
//   - ?before=YYYY-MM-DD：删掉该日期之前的记录
//   - 不带参数：按当前的保留天数删掉过期的那些
//
// POST /api/admin/records/purge
func handleAdminPurgeRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}

	cutoff := ""
	if before := strings.TrimSpace(r.URL.Query().Get("before")); before != "" {
		t, err := store.ParseTimeLoose(before)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, errors.New("before 需要是 2006-01-02 或 RFC3339"))
			return
		}
		cutoff = t.Format(time.RFC3339)
	} else {
		days := service.RecordRetentionDays()
		if days <= 0 {
			httpx.WriteErr(w, http.StatusBadRequest,
				errors.New("当前设置为永久保留（保留天数为 0），请指定 before 参数，或先设置保留天数"))
			return
		}
		cutoff = time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	}

	n, err := store.PurgeRecordsBefore(cutoff)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "deleted": n, "before": cutoff,
	})
}

// handleAdminRecords 全部记录，可按用户/类型过滤。
// GET /api/admin/records?userId=&kind=&limit=&offset=
func handleAdminRecords(w http.ResponseWriter, r *http.Request) {
	uid, _ := strconv.ParseInt(r.URL.Query().Get("userId"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := store.ListRecords(store.RecordFilter{
		UserID: uid, Kind: r.URL.Query().Get("kind"), Limit: limit, Offset: offset,
	})
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	// 顺带返回保留策略与当前最早一条，方便后台显示
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "retention": service.RecordRetentionSummary(),
	})
}

// handleAdminInvites 邀请码：GET 列表，POST 新建。
// GET/POST /api/admin/invites
func handleAdminInvites(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		list, total, err := store.ListInvitesPage(limit, offset)
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list, "total": total})

	case http.MethodPost:
		var body struct {
			Code      string `json:"code"`
			MaxUses   int    `json:"maxUses"`
			ExpiresAt string `json:"expiresAt"`
			Note      string `json:"note"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpx.WriteErr(w, StatusOf(err), err)
			return
		}
		code := strings.ToUpper(strings.TrimSpace(body.Code))
		if code == "" {
			code = strings.ToUpper(util.RandomHex(4)) // 8 位
		}
		if len(code) < 4 || len(code) > 32 {
			httpx.WriteErr(w, http.StatusBadRequest, errors.New("邀请码长度需 4~32 位"))
			return
		}
		expires := ""
		if body.ExpiresAt != "" {
			t, err := store.ParseTimeLoose(body.ExpiresAt)
			if err != nil {
				httpx.WriteErr(w, http.StatusBadRequest, errors.New("expiresAt 需要是 2006-01-02 或 RFC3339"))
				return
			}
			expires = t.Format(time.RFC3339) // 统一成 RFC3339，consumeInvite 才能按字符串比较
		}
		batch, err := store.CreateInvite(code, body.MaxUses, expires, util.Truncate(body.Note, 120))
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "code": code, "batch": batch})

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}
}

// handleAdminInviteDelete 删除邀请码。
// DELETE /api/admin/invites/{code}
func handleAdminInviteDelete(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	if code == "" {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("缺少邀请码"))
		return
	}
	if err := store.DeleteInvite(code); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// maxFooterHTML 是统计代码的长度上限：正常一段 <script> 也就几百字节。
const maxFooterHTML = 8000

// handleAdminBlacklist 黑名单：GET 列表，POST 新增。
//
// kind=keyword：生成内容里包含该词就直接 403；
// kind=ip：该来源的所有请求都返回 403（管理员账号放行，避免把自己锁在外面）。
//
// GET/POST /api/admin/blacklist
func handleAdminBlacklist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		items, total, err := store.ListBlacklistPage(limit, offset)
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})

	case http.MethodPost:
		var body struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
			Note  string `json:"note"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpx.WriteErr(w, StatusOf(err), err)
			return
		}
		entry, err := store.AddBlacklistEntry(strings.TrimSpace(body.Kind), body.Value, util.Truncate(body.Note, 120))
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": entry})

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}
}

// handleAdminBlacklistUpdate 修改一条黑名单的内容 / 备注。
// PUT /api/admin/blacklist/{id}
func handleAdminBlacklistUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("id 不合法"))
		return
	}
	var body struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
		Note  string `json:"note"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	entry, err := store.UpdateBlacklistEntry(id, body.Kind, body.Value, util.Truncate(body.Note, 120))
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": entry})
}

// handleAdminBlacklistDelete 删除一条黑名单。
// DELETE /api/admin/blacklist/{id}
func handleAdminBlacklistDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("id 不合法"))
		return
	}
	if err := store.DeleteBlacklistEntry(id); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminUploadIcon 上传站点图标。
//
// 请求方式跟 /api/decode 一致：multipart（字段 icon / file / image）、原始图片字节、
// 或 JSON {image:"data:image/png;base64,..."}。服务端统一裁方、缩到 256×256、转 PNG 再存，
// 这样不管管理员传的是 jpg、竖图还是截图，浏览器页签和 iOS 主屏都能正常显示。
//
// POST /api/admin/icon
func handleAdminUploadIcon(w http.ResponseWriter, r *http.Request) {
	img, err := imageFromRequest(r, imagex.MaxImageBytes)
	if err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	pngBytes, err := imagex.IconFromImage(img, imagex.IconMaxSide)
	if err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if len(pngBytes) > 512<<10 {
		httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("图标处理后仍超过 512KB，请换一张简单点的图"))
		return
	}
	if err := store.SetSetting(store.SetSiteIcon, base64.StdEncoding.EncodeToString(pngBytes)); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	// 换版本号 → 页面里 /icon.png?v= 跟着变，浏览器立刻拿新图
	if err := store.SetSetting(store.SetSiteIconVer, util.RandomHex(4)); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "iconVer": store.LoadSettings().IconVer, "bytes": len(pngBytes),
	})
}

// handleAdminDeleteIcon 恢复默认图标。
// DELETE /api/admin/icon
func handleAdminDeleteIcon(w http.ResponseWriter, r *http.Request) {
	if err := store.SetSetting(store.SetSiteIcon, ""); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	_ = store.SetSetting(store.SetSiteIconVer, "")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
