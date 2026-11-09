package api

import (
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/model"
	"wxcode/internal/qrcode"
	"wxcode/internal/store"
	"wxcode/internal/watermark"
)

// 水印全局配置（对应 settings.watermark_mode），四档互斥：
//
//	none  → 无水印，前端不展示水印设置
//	all   → 所有人可配置，前端展示水印设置
//	user  → 登录用户可配置；未登录用户开关禁用且关闭，前端展示但不可改
//	admin → 管理员全局配置，前端不展示；所有输出自动套用后台水印
//
// defaultWatermarkColor 是后台没配颜色（或配错）时的兜底。
var defaultWatermarkColor = color.RGBA{0x1f, 0x29, 0x37, 255}

// canConfigWatermark 判断当前身份能否自定义水印（只影响「谁传的参数算数」）。
//
// 无水印 / 管理员全局配置下谁都不能改；登录用户模式下未登录不能改。
func canConfigWatermark(st model.Settings, ai *authInfo) bool {
	switch st.WatermarkMode {
	case model.WatermarkAll:
		return true
	case model.WatermarkUser:
		return ai != nil && ai.user != nil
	default: // none / admin
		return false
	}
}

// settingsWatermark 用后台配置生成默认水印；无全局水印或没配文字时返回 nil。
func settingsWatermark(st model.Settings) *watermark.Config {
	if st.WatermarkMode == model.WatermarkNone || strings.TrimSpace(st.WatermarkText) == "" {
		return nil
	}
	cfg := &watermark.Config{
		Text:     st.WatermarkText,
		Size:     st.WatermarkSize,
		Color:    qrcode.ParseColor(st.WatermarkColor, defaultWatermarkColor),
		Opacity:  float64(st.WatermarkOpacity) / 100,
		Position: watermark.ParsePosition(st.WatermarkPosition),
	}
	// 兜住历史库里可能存在的越界值，避免渲染层拿到 0 或 1000
	if cfg.Size < watermark.MinSize || cfg.Size > watermark.MaxSize {
		cfg.Size = 20
	}
	if cfg.Opacity < 0 || cfg.Opacity > 1 {
		cfg.Opacity = 0.25
	}
	return cfg
}

// resolveWatermark 计算本次请求最终生效的水印，写入 req.wm。
//
// 按「水印全局配置」分档处理：
//
//	none  → 一律不加水印（请求参数也忽略）
//	admin → 一律套用后台水印（请求参数也忽略，保证前端输出的所有图都有水印）
//	user  → 未登录：不加水印；登录：可自定义
//	all   → 所有人都可自定义
//
// 可自定义时只认请求里的参数：用户没配置就不加水印（不再回退到后台默认水印），
// 传 watermark=false 显式关闭；只传部分字段时其余样式用内置默认值补齐。
// 后台默认水印只在 admin 模式下使用。
func resolveWatermark(req *encodeReq, ai *authInfo) error {
	st := store.LoadSettings()

	switch st.WatermarkMode {
	case model.WatermarkNone:
		req.wm = nil
		return nil
	case model.WatermarkAdmin:
		req.wm = settingsWatermark(st)
		return nil
	case model.WatermarkUser:
		if ai == nil || ai.user == nil {
			// 未登录：开关禁用且默认关闭，输出不带水印
			req.wm = nil
			return nil
		}
	}

	if req.Watermark != nil && !*req.Watermark {
		req.wm = nil
		return nil
	}
	if !req.hasWatermarkParams() {
		// all / user 模式下用户没配置任何水印参数 → 不加水印
		req.wm = nil
		return nil
	}

	cfg := watermark.Config{Size: 20, Color: defaultWatermarkColor, Opacity: 0.25, Position: watermark.PositionCenter}
	if s := strings.TrimSpace(req.WatermarkText); s != "" {
		cfg.Text = s
	}
	if req.WatermarkSize > 0 {
		cfg.Size = req.WatermarkSize
	}
	if req.WatermarkColor != "" {
		cfg.Color = qrcode.ParseColor(req.WatermarkColor, cfg.Color)
	}
	if req.WatermarkOpacity != nil {
		cfg.Opacity = float64(*req.WatermarkOpacity) / 100
	}
	if req.WatermarkPosition != "" {
		cfg.Position = watermark.ParsePosition(req.WatermarkPosition)
	}
	if strings.TrimSpace(cfg.Text) == "" {
		// 只有样式参数、没有任何文字：静默不加水印
		req.wm = nil
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	req.wm = &cfg
	return nil
}

// validateWatermarkSettings 校验后台提交的水印配置，返回归一化后的值。
//
// changed 为 false 表示这次请求没带任何水印字段（部分更新语义）。
func validateWatermarkSettings(
	mode, text *string, size, opacity *int, colorHex, position *string,
	cur model.Settings,
) (model.Settings, error) {
	out := cur
	if mode != nil {
		m, ok := parseWatermarkMode(*mode)
		if !ok {
			return out, errors.New("watermarkMode 只能是 none / all / user / admin")
		}
		out.WatermarkMode = m
	}
	if text != nil {
		out.WatermarkText = strings.TrimSpace(*text)
	}
	if size != nil {
		if *size < watermark.MinSize || *size > watermark.MaxSize {
			return out, fmt.Errorf("水印字号需在 %d~%d 像素之间", watermark.MinSize, watermark.MaxSize)
		}
		out.WatermarkSize = *size
	}
	if opacity != nil {
		if *opacity < watermark.MinOpacity || *opacity > watermark.MaxOpacity {
			return out, fmt.Errorf("水印透明度需在 %d~%d 之间", watermark.MinOpacity, watermark.MaxOpacity)
		}
		out.WatermarkOpacity = *opacity
	}
	if colorHex != nil {
		if !validHexColor(*colorHex) {
			return out, errors.New("水印颜色需要是 #RRGGBB 或 #RGB")
		}
		out.WatermarkColor = strings.TrimSpace(*colorHex)
	}
	if position != nil {
		p, ok := watermark.NormalizePosition(*position)
		if !ok {
			return out, errors.New("水印位置只能是 center / top / bottom / top-left / top-right / bottom-left / bottom-right / tile / tile-diagonal")
		}
		out.WatermarkPosition = p
	}

	// 「管理员全局配置」下前端不展示水印设置，必须由管理员提供一份可渲染的默认水印；
	// 其它模式由前端用户自己填文字，默认水印可以留空（仅作为合并参数的兜底）。
	if out.WatermarkMode == model.WatermarkAdmin && strings.TrimSpace(out.WatermarkText) == "" {
		return out, errors.New("管理员全局配置需要填写默认水印文字")
	}
	if strings.TrimSpace(out.WatermarkText) != "" {
		cfg := watermark.Config{
			Text:     out.WatermarkText,
			Size:     out.WatermarkSize,
			Color:    qrcode.ParseColor(out.WatermarkColor, defaultWatermarkColor),
			Opacity:  float64(out.WatermarkOpacity) / 100,
			Position: watermark.ParsePosition(out.WatermarkPosition),
		}
		if err := cfg.Validate(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// parseWatermarkMode 严格解析四档全局配置（保存时用；未知值直接报错）。
func parseWatermarkMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case model.WatermarkNone, "off", "disabled":
		return model.WatermarkNone, true
	case model.WatermarkAll, "anon", "anonymous", "everyone":
		return model.WatermarkAll, true
	case model.WatermarkUser, "login", "logged":
		return model.WatermarkUser, true
	case model.WatermarkAdmin, "global":
		return model.WatermarkAdmin, true
	}
	return "", false
}

// handleWatermarkPreview 渲染一张示例码 + 当前水印参数，供前台/后台实时预览。
//
// GET /api/watermark/preview?text=&size=&color=&opacity=&position=
//
// 返回 PNG（与真实生成同一套渲染管线：内置字形 + 默认角标），参数缺省时套用后台默认水印。
//
// 仅供管理后台调效果：只有管理员能访问（非管理员一律 404，不暴露该接口存在）。
func handleWatermarkPreview(w http.ResponseWriter, r *http.Request) {
	ai := authOf(r)
	if ai == nil || ai.user == nil || !ai.user.IsAdmin() {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("水印预览不可用"))
		return
	}
	st := store.LoadSettings()

	q := r.URL.Query()
	cfg := watermark.Config{Size: 20, Color: defaultWatermarkColor, Opacity: 0.25, Position: watermark.PositionCenter}
	if base := settingsWatermark(st); base != nil {
		cfg = *base
	}
	if s := strings.TrimSpace(q.Get("text")); s != "" {
		cfg.Text = s
	}
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("size"))); err == nil && n > 0 {
		cfg.Size = n
	}
	if s := q.Get("color"); s != "" {
		cfg.Color = qrcode.ParseColor(s, cfg.Color)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("opacity"))); err == nil {
		cfg.Opacity = float64(n) / 100
	}
	if s := q.Get("position"); s != "" {
		cfg.Position = watermark.ParsePosition(s)
	}
	if strings.TrimSpace(cfg.Text) == "" {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("请先填写水印文字"))
		return
	}
	if err := cfg.Validate(); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}

	// 固定示例内容：预览只关心水印效果，不希望每次请求都做一次完整编码放大。
	info, err := qrcode.Encode("https://example.com/watermark", qrcode.DefaultEncodeConfig())
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	// 默认角标（右下角小图标）一起画上，保证预览和真实生成长得一模一样。
	// embeddedBadge 内部有 sync.Once 缓存，不会每次请求都解码。
	badge, _, err := imagex.ResolveBadge("")
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	img := qrcode.RenderImage(info, qrcode.RenderOptions{Size: 600, Badge: badge, Watermark: &cfg})
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_ = png.Encode(w, img)
}

// validHexColor 校验 #RGB / #RRGGBB。
func validHexColor(s string) bool {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) != 3 && len(s) != 6 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
