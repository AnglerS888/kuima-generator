package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/netutil"
	"wxcode/internal/qrcode"
	"wxcode/internal/store"
	"wxcode/internal/watermark"
)

const batchMaxItems = 50

const batchMaxBody = 4 << 20

// batchRequest 是批量生成的入参。
//
// 顶层是公共参数（格式、尺寸、颜色…），items 里每条只给 text；
// 单条也可以覆盖顶层的 text（用 body 里的 items[].text）。
type batchRequest struct {
	// Items 是要生成的内容列表。
	Items []batchItem `json:"items"`
	// 以下是公共参数，作用于所有条目。
	Format    string  `json:"format"`
	Version   string  `json:"version"`
	Level     string  `json:"level"`
	Mask      *int    `json:"mask"`
	Size      int     `json:"size"`
	Margin    int     `json:"margin"`
	FG        string  `json:"fg"`
	BG        string  `json:"bg"`
	Logo      string  `json:"logo"`
	LogoRatio float64 `json:"logoRatio"`
	Badge     string  `json:"badge"`
	FillBadge bool    `json:"fillBadge"`
	Style     string  `json:"style"`
	Color     string  `json:"color"`
	Ghost     int     `json:"ghost"`

	// 水印（公共参数，作用于所有条目；规则同单条接口）
	Watermark         *bool  `json:"watermark"`
	WatermarkText     string `json:"watermarkText"`
	WatermarkSize     int    `json:"watermarkSize"`
	WatermarkColor    string `json:"watermarkColor"`
	WatermarkOpacity  *int   `json:"watermarkOpacity"`
	WatermarkPosition string `json:"watermarkPosition"`

	wm *watermark.Config // resolveWatermark 填充
}

// batchItem 是批量里的单条。
type batchItem struct {
	Text string `json:"text"`
	// 可选的逐条覆盖：只覆盖非空字段。
	Format    string  `json:"format"`
	Size      int     `json:"size"`
	Margin    int     `json:"margin"`
	FG        string  `json:"fg"`
	BG        string  `json:"bg"`
	Logo      string  `json:"logo"`
	LogoRatio float64 `json:"logoRatio"`
	Badge     string  `json:"badge"`
	FillBadge *bool   `json:"fillBadge"` // nil 继承公共参数，false 可显式关闭
	Style     string  `json:"style"`
	Color     string  `json:"color"`
	Ghost     int     `json:"ghost"`
}

// batchResult 是单条的结果。
type batchResult struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	// 成功时给出的编码规格。
	Version int    `json:"version,omitempty"`
	Level   string `json:"level,omitempty"`
	Mask    int    `json:"mask,omitempty"`
	Mode    string `json:"mode,omitempty"`

	// 成功时的图片数据（format=json 时同时给出 svg 与 pngBase64）。
	SVG       string `json:"svg,omitempty"`
	PNGBase64 string `json:"pngBase64,omitempty"`
	// Bytes 是图片字节数（png/jpg/gif 才有）。
	Bytes int `json:"bytes,omitempty"`
	// MS 是这一条的耗时（毫秒）。
	MS int `json:"ms,omitempty"`
}

// batchResponse 是整个批量请求的响应。
type batchResponse struct {
	OK      int           `json:"ok"`
	Failed  int           `json:"failed"`
	Total   int           `json:"total"`
	Results []batchResult `json:"results"`
	// Charged 是本次实际扣掉的额度条数（成功且非示例/重复内容的条数）。
	Charged int `json:"charged"`
}

// handleEncodeBatch 批量生成。
//
// POST /api/encode/batch
func handleEncodeBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}

	// 批量请求体有自己的上限
	if err := httpx.CapRequestBody(r, batchMaxBody); err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	raw, err := httpx.ReadBody(r.Body, batchMaxBody)
	if err != nil {
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("请求体为空"))
		return
	}

	var body batchRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("JSON 解析失败: %w", err))
		return
	}
	if len(body.Items) == 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("items 不能为空"))
		return
	}
	if len(body.Items) > batchMaxItems {
		httpx.WriteErr(w, http.StatusBadRequest,
			fmt.Errorf("单次最多 %d 条，当前 %d 条", batchMaxItems, len(body.Items)))
		return
	}

	// 登录要求与计费入口跟单条接口保持一致：这里只做"能不能用"的判定，
	// 真正的扣额度在下面逐条进行（按条扣，失败的退掉）。
	ai, _, ok := gateUsage(w, r, false)
	if !ok {
		return
	}

	// 水印：整批共用一份解析结果（权限与默认值对同一请求是一致的）
	wmReq := &encodeReq{
		Watermark:         body.Watermark,
		WatermarkText:     body.WatermarkText,
		WatermarkSize:     body.WatermarkSize,
		WatermarkColor:    body.WatermarkColor,
		WatermarkOpacity:  body.WatermarkOpacity,
		WatermarkPosition: body.WatermarkPosition,
	}
	if err := resolveWatermark(wmReq, ai); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	body.wm = wmReq.wm

	resp := batchResponse{
		Total:   len(body.Items),
		Results: make([]batchResult, 0, len(body.Items)),
	}

	for i, item := range body.Items {
		res, charged := encodeOneBatchItem(r, ai, &body, item, i)
		if res.OK {
			resp.OK++
		} else {
			resp.Failed++
		}
		if charged {
			resp.Charged++
		}
		resp.Results = append(resp.Results, res)
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

// encodeOneBatchItem 生成单条并按条目规则计费、记录。
//
// 返回值第二个是「这条是否真的扣了额度」——示例内容 / 重复内容 / 免扣场景都返回 false。
//
// 计费口径与单条 /api/encode 一致：
//   - 示例内容 / 本站地址 / 该用户之前生成过同样内容 → 不扣额度、不写记录；
//   - 否则扣 1 次额度并写一条记录。
func encodeOneBatchItem(r *http.Request, ai *authInfo, body *batchRequest, item batchItem, index int) (batchResult, bool) {
	out := batchResult{Index: index, Text: item.Text}
	t0 := time.Now()

	// 把公共参数与逐条覆盖合并成一条 encodeReq
	req := &encodeReq{
		Text:      item.Text,
		Format:    body.Format,
		Version:   body.Version,
		Level:     body.Level,
		Mask:      -1,
		Size:      body.Size,
		Margin:    body.Margin,
		FG:        body.FG,
		BG:        body.BG,
		Logo:      body.Logo,
		LogoRatio: body.LogoRatio,
		Badge:     body.Badge,
		FillBadge: body.FillBadge,
		Style:     body.Style,
		Color:     body.Color,
		Ghost:     body.Ghost,
	}
	if body.Mask != nil {
		req.Mask = *body.Mask
	}
	// 逐条覆盖（只覆盖显式给了值的字段）
	if item.Format != "" {
		req.Format = item.Format
	}
	if item.Size > 0 {
		req.Size = item.Size
	}
	if item.Margin > 0 {
		req.Margin = item.Margin
	}
	if item.FG != "" {
		req.FG = item.FG
	}
	if item.BG != "" {
		req.BG = item.BG
	}
	if item.Logo != "" {
		req.Logo = item.Logo
	}
	if item.LogoRatio > 0 {
		req.LogoRatio = item.LogoRatio
	}
	if item.Badge != "" {
		req.Badge = item.Badge
	}
	if item.FillBadge != nil {
		req.FillBadge = *item.FillBadge
	}
	if item.Style != "" {
		req.Style = item.Style
	}
	if item.Color != "" {
		req.Color = item.Color
	}
	if item.Ghost > 0 {
		req.Ghost = item.Ghost
	}
	req.wm = body.wm
	req.normalize()

	if req.Text == "" {
		out.Error = "text 不能为空"
		return out, false
	}
	if len(req.Text) > netutil.MaxTextLen {
		out.Error = fmt.Sprintf("内容过长（上限 %d 字节）", netutil.MaxTextLen)
		return out, false
	}
	// 黑名单关键词：命中就跳过这条，不扣额度、不落记录
	if store.BlacklistKeywordHit(req.Text) {
		out.Error = "内容包含禁止发布的词语"
		return out, false
	}

	// 计费判定（与单条接口同样的三个例外）
	meter := true
	if isDemoPayload(req.Text) || isSiteSelfURL(r, req.Text) {
		meter = false
	} else if ai.user != nil && store.HasDone(ai.user.ID, "gen", req.Text) {
		meter = false
	}

	consumed := false
	if meter {
		// 用 Key 调用一定要扣；网页要不要扣看 require_login
		shouldCharge := ai.viaAPI || store.LoadSettings().RequireLogin
		if shouldCharge {
			if ai.user != nil {
				if !store.ConsumeQuota(ai.user.ID) {
					out.Error = errQuotaNoLeft.Error()
					return out, false
				}
				consumed = true
			}
		}
	}

	info, err := buildCode(req)
	if err != nil {
		if consumed {
			store.RefundQuota(ai.user.ID)
		}
		out.Error = err.Error()
		return out, false
	}

	logo, err := req.logoImage()
	if err != nil {
		if consumed {
			store.RefundQuota(ai.user.ID)
		}
		out.Error = err.Error()
		return out, false
	}
	badgeImg, badgeHref, err := imagex.ResolveBadge(req.Badge)
	if err != nil {
		if consumed {
			store.RefundQuota(ai.user.ID)
		}
		out.Error = err.Error()
		return out, false
	}

	// 批量接口统一按 JSON 出结果（图片转 base64），这样一条失败不影响其它条。
	// format 只影响渲染目标格式，不影响响应结构。
	switch req.Format {
	case "svg":
		out.SVG = qrcode.RenderSVG(info, svgOptions(req, logo, badgeHref))
	case "json":
		png, err := qrcode.RenderPNG(info, renderOptions(req, logo, badgeImg))
		if err != nil {
			if consumed {
				store.RefundQuota(ai.user.ID)
			}
			out.Error = err.Error()
			return out, false
		}
		out.SVG = qrcode.RenderSVG(info, svgOptions(req, logo, badgeHref))
		out.PNGBase64 = base64.StdEncoding.EncodeToString(png)
		out.Bytes = len(png)
	default:
		body, err := renderBytes(info, req, logo, badgeImg)
		if err != nil {
			if consumed {
				store.RefundQuota(ai.user.ID)
			}
			out.Error = err.Error()
			return out, false
		}
		out.PNGBase64 = base64.StdEncoding.EncodeToString(body)
		out.Bytes = len(body)
	}

	out.OK = true
	out.Version = info.Version
	out.Level = string(qrcode.LevelNames[info.Level])
	out.Mask = info.Mask
	out.Mode = qrcode.ModeName(info.Mode)
	out.MS = int(time.Since(t0).Milliseconds())

	// 记一条（示例/重复内容不记；记录关闭或内容脱敏由 recordStorePayload 统一处理）
	storedPayload, recordEnabled := store.RecordStorePayload(info.Payload)
	if meter && recordEnabled && !isDemoPayload(info.Payload) {
		rec := store.NewRecord(ai.user, "gen", batchSource(ai), storedPayload, req.Format,
			map[string]any{
				"version":  info.Version,
				"level":    string(qrcode.LevelNames[info.Level]),
				"mask":     info.Mask,
				"typebits": info.Typebits,
				"mode":     qrcode.ModeName(info.Mode),
				"size":     req.Size,
				"format":   req.Format,
				"batch":    true,
			},
			out.Bytes, out.MS, netutil.ClientIP(r), r.UserAgent())
		_ = store.InsertRecord(rec)
	}
	return out, consumed
}

// batchSource 返回记录里的来源标记：批量统一记 api（它是程序调用）。
func batchSource(ai *authInfo) string {
	if ai != nil && ai.viaAPI {
		return "api"
	}
	return "web"
}
