package api

import (
	"encoding/base64"
	"errors"
	"net/http"

	"wxcode/internal/httpx"
	"wxcode/internal/netutil"
	"wxcode/internal/service"
	"wxcode/internal/store"
)

// handleCaptcha 下发一个验证码（PNG data URL），前端直接放进 img。
// GET /api/captcha
func handleCaptcha(w http.ResponseWriter, r *http.Request) {
	if !store.LoadSettings().CaptchaEnabled {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("验证码未开启"))
		return
	}
	if !netutil.CaptchaLimiter.Allow(netutil.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		httpx.WriteErr(w, http.StatusTooManyRequests, errors.New("验证码请求过于频繁，请稍后再试"))
		return
	}
	id, code := service.NewCaptcha()
	pngBytes := service.CaptchaPNG(code)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"id":        id,
		"image":     dataURL,
		"expiresIn": int(service.CaptchaTTL.Seconds()),
	})
}
