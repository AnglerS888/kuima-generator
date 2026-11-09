package api

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"strings"
	"sync"

	"wxcode/internal/httpx"
	"wxcode/internal/model"
	"wxcode/internal/store"
	webassets "wxcode/web"
)

const siteNameToken = "{{SITE_NAME}}"

// iconVerToken 给 /icon.png?v= 用：换了自定义图标 URL 就变，避免浏览器拿旧图。
const iconVerToken = "{{ICON_V}}"

// assetVersionToken 是页面里 /auth.css?v= 的占位符。
//
// 页面本身是 no-cache，样式表却要缓存；一旦只换了样式表内容、URL 没变，用户就会
// 拿"新 HTML + 旧 CSS"渲染 —— 实测表现是后台头部 logo 被撑成一坨大图、
// 主题按钮变成一个细长条（新 HTML 的元素在旧 CSS 里没有对应规则）。
// 用内容哈希做版本号：样式改一次 URL 就变一次，浏览器立刻取新的；
// 内容没变则命中长缓存，不会每次都重下。
const assetVersionToken = "{{ASSET_V}}"

var (
	assetVerOnce sync.Once
	assetVer     string
)

// assetVersion 返回静态资源内容的短哈希（8 位十六进制）。
// auth.css 与 layer.js 一起算：两者总是同版部署，共用一个版本号够用。
func assetVersion() string {
	assetVerOnce.Do(func() {
		h := sha256.New()
		h.Write(webassets.AuthCSS)
		h.Write(webassets.LayerJS)
		sum := h.Sum(nil)
		assetVer = hex.EncodeToString(sum[:4])
	})
	return assetVer
}

// servePage 输出页面：替换站名 / 资源版本 / 图标版本 / 底部统计代码，禁缓存。
func servePage(w http.ResponseWriter, page []byte) {
	servePageMode(w, page, true)
}

// servePageNoAnnounce 供登录 / 注册 / 管理后台使用：只保留关键词与底部代码，不插入公告。
//
// 这些页面要么是「进站第一屏」（公告会挤掉表单），要么是后台工作区（公告是给访客看的），
// 都不适合出现公告横幅。
func servePageNoAnnounce(w http.ResponseWriter, page []byte) {
	servePageMode(w, page, false)
}

func servePageMode(w http.ResponseWriter, page []byte, withAnnounce bool) {
	st := store.LoadSettings()
	body := strings.ReplaceAll(string(page), siteNameToken, html.EscapeString(st.SiteName))
	body = strings.ReplaceAll(body, assetVersionToken, assetVersion())
	body = strings.ReplaceAll(body, iconVerToken, st.IconVer)
	body = decoratePageWithAnnounce(body, st, withAnnounce)
	body = injectFooter(body, st)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(body))
}

// decoratePage 注入后台可配的两块内容：
//   - 搜索关键词 → <meta name="keywords">（SEO，所有页面）
//   - 网站公告   → <main> 之前的一条横幅（所有页面）
//
// 公告支持 HTML：写了标签就原样注入（<b>、<a href>、<br>、<span style> 等），
// 纯文本则转义后靠 CSS 的 pre-wrap 保留换行。信任级别与「底部统计代码」一致 ——
// 能进后台本来就能改整站页面。
func decoratePage(body string, st model.Settings) string {
	return decoratePageWithAnnounce(body, st, true)
}

func decoratePageWithAnnounce(body string, st model.Settings, withAnnounce bool) string {
	if kw := strings.TrimSpace(st.SiteKeywords); kw != "" {
		tag := `<meta name="keywords" content="` + html.EscapeString(kw) + `">` + "\n"
		if i := strings.Index(body, "</head>"); i >= 0 {
			body = body[:i] + tag + body[i:]
		}
	}
	if text := strings.TrimSpace(st.SiteAnnounce); withAnnounce && text != "" {
		// 只在 </head> 之后找主内容起点：CSS 注释或内联脚本里也可能出现
		// "<main" 这几个字，直接搜全文会把公告插进注释/脚本里，浏览器不渲染。
		from := strings.Index(body, "</head>")
		if from < 0 {
			from = 0
		}
		if i := strings.Index(body[from:], "<main"); i >= 0 {
			i += from
			body = body[:i] + announceHTML(text) + "\n" + body[i:]
		}
	}
	return body
}

// announceHTML 生成公告横幅（圆底图标 + 内容），不依赖任何外部资源。
// 内容里写了标签就原样注入（支持 HTML）；纯文本则转义后交给 pre-wrap 保留换行。
func announceHTML(inner string) string {
	// 写了标签就关掉 pre-wrap：否则多行排版的 HTML 会在页面上多出一堆空行。
	cls := "announce-text"
	body := html.EscapeString(inner)
	if looksLikeHTML(inner) {
		cls += " is-html"
		body = inner
	}
	return `<div class="site-announce container"><div class="announce-inner">` +
		`<span class="announce-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ` +
		`stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` +
		`<path d="M3 11v2a1 1 0 0 0 1 1h2l4 4V6L6 10H4a1 1 0 0 0-1 1Z"/>` +
		`<path d="M14 8a5 5 0 0 1 0 8"/><path d="M17 5a9 9 0 0 1 0 14"/></svg></span>` +
		`<div class="` + cls + `">` + body + `</div></div></div>`
}

// looksLikeHTML 判断公告里有没有写标签，只用来决定要不要保留 pre-wrap 换行。
// 不做任何过滤：公告内容由管理员维护，能和「底部统计代码」一样原样生效。
func looksLikeHTML(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		if c := s[i+1]; c == '/' || c == '!' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') {
			return true
		}
	}
	return false
}

// handleNotFound 未匹配到任何路由时：
//   - /api/* 返回 JSON 404（客户端按接口语义处理，别拿到一张 HTML）
//   - 其它路径返回 404 页面，3 秒后自动回首页
func handleNotFound(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("接口不存在"))
		return
	}
	// 直接复用站点的 auth.css 与令牌，保证 404 页和登录/个人中心长得一样
	st := store.LoadSettings()
	body := strings.ReplaceAll(string(webassets.NotFoundHTML), siteNameToken, html.EscapeString(st.SiteName))
	body = strings.ReplaceAll(body, assetVersionToken, assetVersion())
	body = strings.ReplaceAll(body, iconVerToken, st.IconVer)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(body))
}

// injectFooter 把管理员配置的统计代码插到 </body> 前。
//
// 原样注入（不转义）是刻意的：统计代码本身就是 <script> 片段，转义了就没意义。
// 这个配置只有管理员能改，等于管理员对自己站点有完全控制权，不构成越权。
func injectFooter(body string, st model.Settings) string {
	if strings.TrimSpace(st.FooterHTML) == "" {
		return body
	}
	if i := strings.LastIndex(body, "</body>"); i >= 0 {
		return body[:i] + st.FooterHTML + "\n" + body[i:]
	}
	return body + "\n" + st.FooterHTML
}

// handleAuthCSS 账号页面的共享样式。
//
// 带了正确的 ?v=<内容哈希> 才给长缓存（内容变则 URL 变，绝不会拿到旧样式）；
// 直接访问 /auth.css 的一律 no-cache，避免以后有人手敲地址被缓存一年。
//
// GET /auth.css
func handleAuthCSS(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, "text/css; charset=utf-8", webassets.AuthCSS)
}

// handleLayerJS 轻量弹层（替代原生 alert/confirm/prompt）。
// GET /layer.js
func handleLayerJS(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, "application/javascript; charset=utf-8", webassets.LayerJS)
}

// serveAsset 带版本号命中长缓存、否则回源校验。
func serveAsset(w http.ResponseWriter, r *http.Request, ctype string, body []byte) {
	if r.URL.Query().Get("v") == assetVersion() {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Content-Type", ctype)
	w.Write(body)
}

// handleLoginPage 登录页。已登录直接跳走，省得重复登录。
// GET /login
func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if u := currentUser(r); u != nil {
		if u.IsAdmin() {
			http.Redirect(w, r, "/admin", http.StatusFound)
		} else {
			http.Redirect(w, r, "/me", http.StatusFound)
		}
		return
	}
	servePageNoAnnounce(w, webassets.LoginHTML)
}

// handleForgotPage 找回密码页。
//
// 页面本身不做权限判断：邮箱找回是否可用由 /api/me 里的
// emailResetAvailable（开关 + 发信配置）与提交时的接口结果共同决定。
// GET /forgot
func handleForgotPage(w http.ResponseWriter, r *http.Request) {
	servePageNoAnnounce(w, webassets.ForgotHTML)
}

// handleRegisterPage 注册页（是否开放、是否需要邀请码由页面里的 JS 按 /api/me 判断）。
// GET /register
func handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/me", http.StatusFound)
		return
	}
	servePageNoAnnounce(w, webassets.RegisterHTML)
}

// handleMePage 个人中心：需要登录，未登录跳登录页。
// GET /me
func handleMePage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) == nil {
		http.Redirect(w, r, "/login?next=/me", http.StatusFound)
		return
	}
	servePage(w, webassets.MeHTML)
}

// handleAdminPage 管理后台。
//
// 非管理员一律 404：普通用户既看不到入口，也探测不到这个页面是否存在。
// 管理员从 /login 登录后访问 /admin。
// GET /admin
func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !u.IsAdmin() {
		http.NotFound(w, r)
		return
	}
	servePageNoAnnounce(w, webassets.AdminHTML)
}
