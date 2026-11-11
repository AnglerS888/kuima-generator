package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/netutil"
	"wxcode/internal/qrcode"
	"wxcode/internal/store"
)

// ============================================================
// 生成流水线：请求参数 → 编码 → 渲染 → 响应
// ============================================================

// runtimeMeta 是生成结果的摘要，供 JSON 输出与前端展示。
type runtimeMeta struct {
	Text          string `json:"text"`
	Format        string `json:"format"`
	Version       int    `json:"version"`
	Level         string `json:"level"`
	LevelIndex    int    `json:"levelIndex"`
	Mask          int    `json:"mask"`
	Typebits      int    `json:"typebits"`
	Mode          string `json:"mode"`
	Transformed   string `json:"transformed,omitempty"`
	Codewords     int    `json:"codewords"`
	ECCodewords   int    `json:"ecCodewords"`
	Size          int    `json:"size,omitempty"`
	Style         string `json:"style,omitempty"`
	Color         string `json:"color,omitempty"`
	Ghost         int    `json:"ghost,omitempty"`
	SVG           string `json:"svg,omitempty"`
	PNGBase64     string `json:"pngBase64,omitempty"`
	LogoAnimated  bool   `json:"logoAnimated,omitempty"`
	LogoFrames    int    `json:"logoFrames,omitempty"`
	BadgeAnimated bool   `json:"badgeAnimated,omitempty"`
	BadgeFrames   int    `json:"badgeFrames,omitempty"`
}

// isSiteSelfURL 判断内容是不是「本站地址」。
// 首页加载时默认预览的就是当前域名生成的码，它不该扣额度也不该进记录表 ——
// 否则每个访客打开首页都会白扣登录用户一次额度、把记录表刷满。
// 按请求的 Host 现算（与页面里 {{BASE_URL}} 同源），带不带末尾斜杠都算。
func isSiteSelfURL(r *http.Request, text string) bool {
	base := strings.TrimRight(httpx.RequestBaseURL(r), "/")
	t := strings.TrimRight(strings.TrimSpace(text), "/")
	return t != "" && strings.EqualFold(t, base)
}

// buildCode 按请求生成葵花码（不含渲染）。
func buildCode(req *encodeReq) (*qrcode.EncodeInfo, error) {
	return qrcode.Encode(req.Text, qrcode.EncodeConfig{
		Version:  qrcode.ParseVersion(req.Version),
		MinLevel: qrcode.ParseLevel(req.Level),
		Mask:     req.Mask,
	})
}

// metaOf 组装摘要。
func metaOf(info *qrcode.EncodeInfo, format string) runtimeMeta {
	m := runtimeMeta{
		Text:        info.Payload,
		Format:      format,
		Version:     info.Version,
		Level:       string(qrcode.LevelNames[info.Level]),
		LevelIndex:  info.Level,
		Mask:        info.Mask,
		Typebits:    info.Typebits,
		Mode:        qrcode.ModeName(info.Mode),
		Codewords:   len(info.Codewords),
		ECCodewords: info.ECPB,
	}
	if info.Mode == qrcode.ModeAlnum {
		m.Transformed = info.Transformed
	}
	return m
}

// renderOptions 把请求参数转成位图渲染选项。
func renderOptions(req *encodeReq, logo, badge *imagex.ImageRef) qrcode.RenderOptions {
	fg, bg := req.colors()
	return qrcode.RenderOptions{
		Size:        req.Size,
		Margin:      req.Margin,
		Fg:          fg,
		Bg:          bg,
		Logo:        imagex.ImageOf(logo),
		LogoRatio:   req.LogoRatio,
		LogoFrames:  imagex.FramesOf(logo),
		LogoDelays:  imagex.DelaysOf(logo),
		Badge:       imagex.ImageOf(badge),
		BadgeFrames: imagex.FramesOf(badge),
		BadgeDelays: imagex.DelaysOf(badge),
		FillBadge:   req.FillBadge,
		Style:       req.Style,
		ColorMode:   req.Color,
		GhostLevel:  req.Ghost,
		Watermark:   req.wm,
	}
}

// svgOptions 把请求参数转成 SVG 渲染选项。
func svgOptions(req *encodeReq, logo *imagex.ImageRef, badgeHref string) qrcode.SVGOptions {
	return qrcode.SVGOptions{
		CenterHref: imagex.DataURIOf(logo),
		BadgeHref:  badgeHref,
		FillBadge:  req.FillBadge,
		Style:      req.Style,
		ColorMode:  req.Color,
		GhostLevel: req.Ghost,
		Watermark:  req.wm,
	}
}

// setCodeHeaders 把编码参数写进响应头，前端据此展示规格（二进制响应没有 JSON 体）。
func setCodeHeaders(w http.ResponseWriter, info *qrcode.EncodeInfo) {
	h := w.Header()
	h.Set("X-WXCode-Version", strconv.Itoa(info.Version))
	h.Set("X-WXCode-Level", string(qrcode.LevelNames[info.Level]))
	h.Set("X-WXCode-Mask", strconv.Itoa(info.Mask))
	h.Set("X-WXCode-Typebits", strconv.Itoa(info.Typebits))
	h.Set("X-WXCode-Mode", qrcode.ModeName(info.Mode))
	h.Set("X-WXCode-Codewords", strconv.Itoa(len(info.Codewords)))
	h.Set("X-WXCode-ECCodewords", strconv.Itoa(info.ECPB))
	h.Set("X-WXCode-DataCodewords", strconv.Itoa(len(info.Codewords)-info.ECPB))
	// 前端需要跨域读头时（同源不需要），把常用头暴露出去
	h.Set("Access-Control-Expose-Headers",
		"X-WXCode-Version,X-WXCode-Level,X-WXCode-Mask,X-WXCode-Typebits,"+
			"X-WXCode-Mode,X-WXCode-Codewords,X-WXCode-ECCodewords,X-WXCode-DataCodewords")
}

// publishCodeImage 把本次生成的图片登记成临时地址，并在响应头里告知前端。
//
// 前端拿 X-WXCode-Image 当 <img src>，手机端才有点下载 / 长按保存；
// 缓存满了或单张过大时不给头，前端自然退回 blob。
func publishCodeImage(w http.ResponseWriter, info *qrcode.EncodeInfo, body []byte, ctype, ext string) {
	id := publishImage(body, ctype, ext, "wxcode-"+itext(info)+"."+ext)
	if id == "" {
		return
	}
	w.Header().Set("X-WXCode-Id", id)
	w.Header().Set("X-WXCode-Image", "/api/code/"+id+"."+ext)
	w.Header().Set("Access-Control-Expose-Headers",
		"X-WXCode-Version,X-WXCode-Level,X-WXCode-Mask,X-WXCode-Typebits,"+
			"X-WXCode-Mode,X-WXCode-Codewords,X-WXCode-ECCodewords,X-WXCode-DataCodewords,"+
			"X-WXCode-Id,X-WXCode-Image")
}

// mediaInfo 返回该 format 对应的 Content-Type 与文件后缀。
func mediaInfo(format string) (ctype, ext string) {
	switch format {
	case "svg":
		return "image/svg+xml; charset=utf-8", "svg"
	case "jpg", "jpeg":
		return "image/jpeg", "jpg"
	case "gif":
		return "image/gif", "gif"
	case "json":
		return "application/json; charset=utf-8", "json"
	default:
		return "image/png", "png"
	}
}

// serveCode 生成并按 format 输出（png / svg / json）。
//
// 返回值 ok=false 表示已经写过错误响应（调用方据此退还预扣的额度）。
// 成功时一并返回编码结果与输出字节数，供上层写记录。
func serveCode(w http.ResponseWriter, r *http.Request, req *encodeReq) (info *qrcode.EncodeInfo, size int, ok bool) {
	if req.Text == "" {
		httpx.WriteErr(w, http.StatusBadRequest, errTextRequired)
		return nil, 0, false
	}
	if len(req.Text) > netutil.MaxTextLen {
		httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("内容过长（上限 %d 字节，葵花码本身也只能装几十字节）", netutil.MaxTextLen))
		return nil, 0, false
	}
	info, err := buildCode(req)
	if err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return nil, 0, false
	}
	logo, err := req.logoImage()
	if err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return nil, 0, false
	}
	badgeImg, badgeHref, err := imagex.ResolveBadgeRef(req.Badge)
	if err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return nil, 0, false
	}

	switch req.Format {
	case "svg":
		svg := qrcode.RenderSVG(info, svgOptions(req, logo, badgeHref))
		setCodeHeaders(w, info)
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		// SVG 是可主动执行的文档。即使未来新增图片来源，也不允许它在直接打开时运行脚本。
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data: https:; style-src 'unsafe-inline'; sandbox")
		w.Header().Set("Cache-Control", "no-store")
		publishCodeImage(w, info, []byte(svg), "image/svg+xml; charset=utf-8", "svg")
		io.WriteString(w, svg)
		return info, len(svg), true

	case "json":
		pngBytes, err := qrcode.RenderPNG(info, renderOptions(req, logo, badgeImg))
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return nil, 0, false
		}
		meta := metaOf(info, "json")
		meta.Size = req.Size
		meta.Style = req.Style
		meta.Color = req.Color
		meta.Ghost = req.Ghost
		if animated, frames := imagex.AnimInfo(logo); animated {
			meta.LogoAnimated = true
			meta.LogoFrames = frames
		}
		if animated, frames := imagex.AnimInfo(badgeImg); animated {
			meta.BadgeAnimated = true
			meta.BadgeFrames = frames
		}
		meta.SVG = qrcode.RenderSVG(info, svgOptions(req, logo, badgeHref))
		meta.PNGBase64 = base64.StdEncoding.EncodeToString(pngBytes)
		httpx.WriteJSON(w, http.StatusOK, meta)
		return info, len(pngBytes), true

	default: // png / jpg / gif
		body, err := renderBytes(info, req, logo, badgeImg)
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return nil, 0, false
		}
		setCodeHeaders(w, info)
		ctype, ext := mediaInfo(req.Format)
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `inline; filename="wxcode-`+itext(info)+`.`+ext+`"`)
		publishCodeImage(w, info, body, ctype, ext)
		w.Write(body)
		return info, len(body), true
	}
}

// renderBytes 按 format 渲染 png / jpg / gif。
func renderBytes(info *qrcode.EncodeInfo, req *encodeReq, logo, badge *imagex.ImageRef) ([]byte, error) {
	opt := renderOptions(req, logo, badge)
	switch req.Format {
	case "jpg", "jpeg":
		return qrcode.RenderJPEG(info, opt)
	case "gif":
		return qrcode.RenderGIF(info, opt)
	default:
		return qrcode.RenderPNG(info, opt)
	}
}

// itext 生成 v0Q-m14 这类简写，用作下载文件名。
func itext(info *qrcode.EncodeInfo) string {
	return "v" + strconv.Itoa(info.Version) + string(qrcode.LevelNames[info.Level]) + "-m" + strconv.Itoa(info.Mask)
}

// handleEncode 生成接口：GET 走 query，POST 走 JSON / 表单 / multipart。
// GET  /api/encode?text=...&format=png
// POST /api/encode
func handleEncode(w http.ResponseWriter, r *http.Request) {
	req := &encodeReq{Mask: -1}

	switch r.Method {
	case http.MethodGet:
		fillFromValues(req, r.URL.Query())

	case http.MethodPost:
		parsed, err := readEncodeReq(r, uploadLimitBytes(r, true))
		if err != nil {
			httpx.WriteErr(w, StatusOf(err), err)
			return
		}
		req = parsed

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}

	req.normalize()

	// 水印：按后台开关 / 权限模式解析出本次真正生效的配置
	if err := resolveWatermark(req, authOf(r)); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}

	// 黑名单关键词：命中就直接拒绝，不扣额度、不落记录
	if store.BlacklistKeywordHit(req.Text) {
		httpx.WriteErr(w, http.StatusForbidden, errors.New("内容包含禁止发布的词语，请修改后再试"))
		return
	}

	// 计费判定：示例内容、站点自身地址（页面加载时的默认预览）、
	// 或该用户之前生成过同样内容 → 不扣额度、不记新记录。
	// 注意登录要求照旧（gateUsage 里 meter=false 也会拦匿名）。
	ai := authOf(r)
	meter := true
	if isDemoPayload(req.Text) || isSiteSelfURL(r, req.Text) {
		meter = false
	} else if ai.user != nil && store.HasDone(ai.user.ID, "gen", req.Text) {
		meter = false
	}

	ai, consumed, ok := gateUsage(w, r, meter)
	if !ok {
		return
	}
	t0 := time.Now()
	info, size, wrote := serveCode(w, r, req)
	if !wrote {
		if consumed {
			refundUsage(ai) // 没生成成功就把额度还回原桶
		}
		return
	}
	if meter {
		recordGeneration(r, ai, req, info, size, time.Since(t0))
	}
}
