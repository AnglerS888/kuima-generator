package api

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/qrcode"
	"wxcode/internal/service"
	"wxcode/internal/store"
	"wxcode/internal/util"
)

// handleAutoDecode 处理 /api/decode?family=auto。
// 支持与 /api/decode 相同的三种投喂方式，并可用 prefer=qr 让二维码先试。
func handleAutoDecode(w http.ResponseWriter, r *http.Request) {
	ai, consumed, ok := gateUsage(w, r, true)
	if !ok {
		return
	}
	t0 := time.Now()
	img, err := imageFromRequestContext(r.Context(), r, uploadLimitBytes(r, false))
	if err != nil {
		if consumed {
			refundUsage(ai)
		}
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	res, err := service.DecodeAutoImage(r.Context(), img,
		strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("prefer")), "qr"))
	if err != nil {
		if consumed {
			refundUsage(ai)
		}
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	if consumed && (isDemoPayload(res.Payload) || (ai.user != nil && store.HasDone(ai.user.ID, "decode", res.Payload))) {
		refundUsage(ai)
	} else {
		recordDecode(r, ai, res.DecodeResult(), time.Since(t0))
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ============================================================
// 解析
// ============================================================

const maxDecodeBytes = 24 << 20

// handleDecode 解析接口。三种投喂方式：
//   - 原始图片：Content-Type: image/*，body 就是图片字节
//   - multipart：字段名 file（或 image / logo）
//   - JSON：{"image":"data:image/png;base64,..."} 或 {"url":"https://..."}
//
// POST /api/decode
// GET  /api/decode?url=...
func handleDecode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	// family=auto：一次上传、一次取图，服务端互补尝试太阳码与普通二维码。
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("family")), "auto") {
		handleAutoDecode(w, r)
		return
	}
	ai, consumed, ok := gateUsage(w, r, true)
	if !ok {
		return
	}
	t0 := time.Now()
	res, err := decodeRequestContext(r.Context(), r, uploadLimitBytes(r, false))
	if err != nil {
		if consumed {
			refundUsage(ai)
		}
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	// 解出来是示例地址 / 之前解过同样内容 → 退回额度、不重复记录
	if consumed && (isDemoPayload(res.Payload) || (ai.user != nil && store.HasDone(ai.user.ID, "decode", res.Payload))) {
		refundUsage(ai)
	} else {
		recordDecode(r, ai, res, time.Since(t0))
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// imageFromRequest 从请求里取出一张图片（三个解析接口共用）。
// 支持：multipart 的 file 字段、原始图片字节、JSON 的 image/url、?url= 远程地址。
func imageFromRequest(r *http.Request, max int64) (image.Image, error) {
	return imageFromRequestContext(r.Context(), r, max)
}

// imageFromRequestContext 让远程取图跟随入站请求的取消信号。
func imageFromRequestContext(ctx context.Context, r *http.Request, max int64) (image.Image, error) {
	if max <= 0 {
		max = maxDecodeBytes
	}
	if err := httpx.CapRequestBody(r, max); err != nil {
		return nil, err
	}
	switch {
	case httpx.IsMultipart(r):
		if err := r.ParseMultipartForm(imagex.MaxUploadBody); err != nil {
			return nil, err
		}
		defer r.MultipartForm.RemoveAll()
		for _, key := range []string{"file", "image", "logo", "icon"} {
			if fhs := r.MultipartForm.File[key]; len(fhs) > 0 {
				f, err := fhs[0].Open()
				if err != nil {
					return nil, err
				}
				defer f.Close()
				return imageFromReader(f, max)
			}
		}
		return nil, errNoImageField

	case httpx.IsJSON(r):
		var body struct {
			Image string `json:"image"`
			URL   string `json:"url"`
			Text  string `json:"text"`
		}
		raw, err := httpx.ReadBody(r.Body, max)
		if err != nil {
			return nil, err
		}
		if err := httpx.UnmarshalJSON(raw, &body); err != nil {
			return nil, fmt.Errorf("JSON 解析失败: %w", err)
		}
		spec := util.FirstNonEmpty(body.Image, body.URL, body.Text)
		if spec == "" {
			return nil, errNeedImageOrURL
		}
		return imageFromSpecContext(ctx, spec, max)

	default:
		if u := r.URL.Query().Get("url"); u != "" {
			return imageFromSpecContext(ctx, u, max)
		}
		return imageFromReader(r.Body, max)
	}
}

// imageFromSpec 按 data URL / http(s) URL / base64 / 本地路径加载图片。
func imageFromSpec(spec string, max int64) (image.Image, error) {
	return imageFromSpecContext(context.Background(), spec, max)
}

func imageFromSpecContext(ctx context.Context, spec string, max int64) (image.Image, error) {
	ref, err := imagex.LoadImageRefContext(ctx, spec, imagex.AllowRemoteImage, max)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, errNeedImageOrURL
	}
	return imagex.ImageOf(ref), nil
}

// decodeRequest 取图并做葵花码解析。
func decodeRequest(r *http.Request, max int64) (*qrcode.DecodeResult, error) {
	return decodeRequestContext(r.Context(), r, max)
}

func decodeRequestContext(ctx context.Context, r *http.Request, max int64) (*qrcode.DecodeResult, error) {
	img, err := imageFromRequestContext(ctx, r, max)
	if err != nil {
		return nil, err
	}
	return qrcode.DecodeImageContext(ctx, img)
}

// handleQR 解析普通二维码（微信「加好友」二维码就是 QR，内容是 https://u.wechat.com/...）。
// POST /api/qr
func handleQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	ai, consumed, ok := gateUsage(w, r, true)
	if !ok {
		return
	}
	t0 := time.Now()
	img, err := imageFromRequestContext(r.Context(), r, uploadLimitBytes(r, false))
	if err != nil {
		if consumed {
			refundUsage(ai)
		}
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	res, err := qrcode.DecodeQRImageContext(r.Context(), img)
	if err != nil {
		if consumed {
			refundUsage(ai)
		}
		httpx.WriteErr(w, StatusOf(err), err)
		return
	}
	payload := res.URL
	if payload == "" {
		payload = res.Text
	}
	if consumed && (isDemoPayload(payload) || (ai.user != nil && store.HasDone(ai.user.ID, "decode", payload))) {
		refundUsage(ai)
	} else {
		recordQRDecode(r, ai, res, time.Since(t0))
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// imageFromReader 读图片字节并解码。
func imageFromReader(rd io.Reader, max int64) (image.Image, error) {
	raw, err := httpx.ReadBody(rd, max)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errEmptyBody
	}
	img, err := imagex.DecodeImageSafe(raw)
	if err != nil {
		return nil, err
	}
	return img, nil
}
