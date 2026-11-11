package api

import (
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"strconv"
	"strings"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/netutil"
	"wxcode/internal/qrcode"
	"wxcode/internal/watermark"
)

// ============================================================
// 生成请求参数
// ============================================================

// encodeReq 是 /api/encode 的入参（JSON / 表单 / query 三种来源共用）。
type encodeReq struct {
	Text      string  `json:"text"`
	Format    string  `json:"format"`  // png | svg | json
	Version   string  `json:"version"` // auto | 0 | 1 | 2
	Level     string  `json:"level"`   // L | M | Q | H（= 允许的最低纠错等级）
	Mask      int     `json:"mask"`    // -1 = 自动
	Size      int     `json:"size"`    // PNG 边长
	Margin    int     `json:"margin"`  // PNG 白边
	FG        string  `json:"fg"`
	BG        string  `json:"bg"`
	Logo      string  `json:"logo"` // data URL / http(s) URL / 裸 base64 / 本地路径
	LogoRatio float64 `json:"logoRatio"`
	Badge     string  `json:"badge"`     // default | none | URL
	FillBadge bool    `json:"fillBadge"` // 隐藏徽标时填补空位
	Style     string  `json:"style"`     // classic | hand | dot | block | line | rainbow（笔画风格）
	Color     string  `json:"color"`     // solid | rainbow | ghost（配色）
	Ghost     int     `json:"ghost"`     // 隐形配色的深度：墨色比底色深几级（2~40）

	// 水印（可选）。是否生效由后台开关 + 权限模式决定，优先级见 resolveWatermark。
	Watermark         *bool  `json:"watermark"`
	WatermarkText     string `json:"watermarkText"`
	WatermarkSize     int    `json:"watermarkSize"`
	WatermarkColor    string `json:"watermarkColor"`
	WatermarkOpacity  *int   `json:"watermarkOpacity"` // 0~100
	WatermarkPosition string `json:"watermarkPosition"`

	logoRef   *imagex.ImageRef // multipart 上传时已解码的 logo
	logoCache *imagex.ImageRef
	logoMax   int64 // 本次请求允许的 logo 字节上限（按登录态配置）

	wm *watermark.Config // 解析后的水印配置（resolveWatermark 填充，不参与 JSON）
}

// hasWatermarkParams 报告请求里是否显式带了水印参数。
func (r *encodeReq) hasWatermarkParams() bool {
	return strings.TrimSpace(r.WatermarkText) != "" || r.WatermarkSize > 0 ||
		strings.TrimSpace(r.WatermarkColor) != "" || r.WatermarkOpacity != nil ||
		strings.TrimSpace(r.WatermarkPosition) != ""
}

// normalize 做默认值与边界收敛，避免把非法值传进渲染层。
func (r *encodeReq) normalize() {
	r.Format = strings.ToLower(strings.TrimSpace(r.Format))
	switch r.Format {
	case "png", "jpg", "jpeg", "svg", "gif", "json":
	default:
		r.Format = "png"
	}
	if r.Size <= 0 {
		r.Size = defaultSize
	}
	if r.Size > maxSize {
		r.Size = maxSize
	}
	if r.Size < minSize {
		r.Size = minSize
	}
	if r.Margin < 0 {
		r.Margin = 0
	}
	if r.Margin > maxMargin {
		r.Margin = maxMargin
	}
	if r.Mask < -1 || r.Mask > 31 {
		r.Mask = -1
	}
	if r.LogoRatio <= 0 {
		r.LogoRatio = 1
	}
	// 上限防止有人传 logoRatio=1e9 让渲染层去画一个巨大的圆（CPU/内存放大）
	if r.LogoRatio > netutil.MaxLogoRatio {
		r.LogoRatio = netutil.MaxLogoRatio
	}
	if r.LogoRatio < netutil.MinLogoRatio {
		r.LogoRatio = netutil.MinLogoRatio
	}
	// 笔画风格 / 配色：认大小写和常见别名，认不出的收敛成默认值。
	// 彩虹原本是风格，后来归到配色；这里兼容老链接 style=rainbow。
	legacyRainbow := qrcode.LegacyRandomStyle(r.Style)
	r.Style = qrcode.ParseStyle(r.Style)
	r.Color = qrcode.ParseColorMode(r.Color)
	if legacyRainbow && r.Color == qrcode.ColorSolid {
		r.Color = qrcode.ColorRandom
	}
	// 隐形深度：0 用默认值，其余夹到 2~40（太小扫不出、太大就看得见了）
	if r.Ghost == 0 {
		r.Ghost = qrcode.GhostDefault
	}
	if r.Ghost < 2 {
		r.Ghost = 2
	}
	if r.Ghost > 40 {
		r.Ghost = 40
	}
}

const defaultSize = 600

const minSize = 120

const maxSize = 2000

const maxMargin = 200

// readEncodeReq 按 Content-Type 读取参数：JSON / multipart（可带 logo 文件）/ 表单。
// logoMax 是本次请求允许的 logo 字节上限（未登录 / 已登录由调用方按配置决定）。
func readEncodeReq(r *http.Request, logoMax int64) (*encodeReq, error) {
	req := &encodeReq{Mask: -1, logoMax: logoMax}
	if logoMax <= 0 {
		logoMax = imagex.MaxImageBytes
		req.logoMax = logoMax
	}
	// 请求体要比图片本身宽裕：base64 会膨胀 4/3，multipart 还有边界与字段开销
	bodyLimit := int64(imagex.MaxUploadBody)
	if v := logoMax*2 + (1 << 20); v > bodyLimit {
		bodyLimit = v
	}
	// 中心 logo 与徽标可以同时上传，JSON 内两张图的 base64 都会膨胀。
	imagesLimit := (logoMax+imagex.MaxImageBytes)*4/3 + (1 << 20)
	if imagesLimit > bodyLimit {
		bodyLimit = imagesLimit
	}
	if err := httpx.CapRequestBody(r, bodyLimit); err != nil {
		return nil, err
	}

	switch {
	case httpx.IsJSON(r):
		jsonLimit := int64(imagex.MaxJSONBody)
		if v := logoMax*2 + (1 << 20); v > jsonLimit {
			jsonLimit = v
		}
		if imagesLimit > jsonLimit {
			jsonLimit = imagesLimit
		}
		body, err := httpx.ReadBody(r.Body, jsonLimit)
		if err != nil {
			return nil, err
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, req); err != nil {
				return nil, fmt.Errorf("JSON 解析失败: %w", err)
			}
		}
		return req, nil

	case httpx.IsMultipart(r):
		if err := r.ParseMultipartForm(imagex.MaxUploadBody); err != nil {
			return nil, err
		}
		defer r.MultipartForm.RemoveAll()
		fillFromValues(req, r.MultipartForm.Value)
		if fhs := r.MultipartForm.File["logo"]; len(fhs) > 0 {
			ref, err := imagex.LoadImageRefFromMultipart(fhs[0], logoMax)
			if err != nil {
				return nil, err
			}
			req.logoRef = ref
		}
		return req, nil

	default:
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		fillFromValues(req, r.Form)
		return req, nil
	}
}

// fillFromValues 把 query / 表单值搬进结构体。
func fillFromValues(req *encodeReq, v map[string][]string) {
	get := func(keys ...string) string {
		for _, k := range keys {
			if vs, ok := v[k]; ok && len(vs) > 0 && vs[0] != "" {
				return vs[0]
			}
		}
		return ""
	}
	req.Text = get("text", "payload", "url", "content")
	if s := get("format", "type"); s != "" {
		req.Format = s
	}
	if s := get("version"); s != "" {
		req.Version = s
	}
	if s := get("level", "eclevel", "ec"); s != "" {
		req.Level = s
	}
	req.Mask = atoiOr(get("mask"), -1)
	req.Size = atoiOr(get("size", "w", "width"), 0)
	req.Margin = atoiOr(get("margin", "pad"), 0)
	if s := get("fg", "fgColor"); s != "" {
		req.FG = s
	}
	if s := get("bg", "bgColor"); s != "" {
		req.BG = s
	}
	req.Logo = get("logo")
	req.LogoRatio = atofOr(get("logoRatio", "logoRatio"), 0)
	if s := get("badge"); s != "" {
		req.Badge = s
	}
	if b, ok := parseBoolLoose(get("fillBadge")); ok {
		req.FillBadge = b
	}
	if s := get("style", "renderStyle"); s != "" {
		req.Style = s
	}
	if s := get("color", "colorMode", "palette"); s != "" {
		req.Color = s
	}
	req.Ghost = atoiOr(get("ghost", "ghostLevel"), 0)
	if s := get("watermark", "wm"); s != "" {
		if b, ok := parseBoolLoose(s); ok {
			req.Watermark = &b
		}
	}
	req.WatermarkText = get("watermarkText", "wmText")
	req.WatermarkSize = atoiOr(get("watermarkSize", "wmSize"), 0)
	if s := get("watermarkColor", "wmColor"); s != "" {
		req.WatermarkColor = s
	}
	if s := get("watermarkOpacity", "wmOpacity"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			req.WatermarkOpacity = &n
		}
	}
	req.WatermarkPosition = get("watermarkPosition", "wmPosition")
}

// parseBoolLoose 接受 1/0、true/false、on/off、yes/no。
func parseBoolLoose(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "on", "yes", "y":
		return true, true
	case "0", "false", "off", "no", "n":
		return false, true
	}
	return false, false
}

// logoImage 惰性加载 logo，避免同一请求里重复下载/解码。
func (r *encodeReq) logoImage() (*imagex.ImageRef, error) {
	if r.logoRef != nil {
		return r.logoRef, nil
	}
	if r.logoCache != nil {
		return r.logoCache, nil
	}
	if r.Logo == "" {
		return nil, nil
	}
	max := r.logoMax
	if max <= 0 {
		max = imagex.MaxImageBytes
	}
	ref, err := imagex.LoadImageRef(r.Logo, imagex.AllowRemoteImage, max)
	if err != nil {
		return nil, err
	}
	r.logoCache = ref
	return ref, nil
}

// colors 解析前景/背景色（缺省黑/白）。
func (r *encodeReq) colors() (fg, bg color.RGBA) {
	return qrcode.ParseColor(r.FG, color.RGBA{0, 0, 0, 255}),
		qrcode.ParseColor(r.BG, color.RGBA{255, 255, 255, 255})
}

// ============================================================
// 小工具
// ============================================================

// atoiOr 解析整数，空串或非法值返回 def。
func atoiOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// atofOr 解析浮点数，空串或非法值返回 def。
func atofOr(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return f
}
