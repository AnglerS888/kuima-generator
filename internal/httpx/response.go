package httpx

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
)

// ============================================================
// 响应小工具
// ============================================================

// writeJSON 输出 JSON（统一 UTF-8，不转义中文）。
func WriteJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// /api/me 等响应含 API Key，任何情况下都不能进入共享或浏览器缓存。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeErr 输出 {"error": "..."}。
func WriteErr(w http.ResponseWriter, code int, err error) {
	WriteJSON(w, code, map[string]string{"error": err.Error()})
}

// errBodyTooLarge 表示请求体超过上限（调用方据此返回 413）。
var ErrBodyTooLarge = errors.New("请求体超过大小上限")
