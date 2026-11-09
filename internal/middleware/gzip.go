package middleware

import (
	"compress/gzip"
	"net/http"
	"strings"
)

type gzipResponseWriter struct {
	http.ResponseWriter
	request     *http.Request
	writer      *gzip.Writer
	wroteHeader bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	ct := strings.ToLower(strings.TrimSpace(w.Header().Get("Content-Type")))
	if code != http.StatusNoContent && code != http.StatusNotModified && acceptsGzip(w.request) && compressibleType(ct) {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.writer = gzip.NewWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.writer != nil {
		return w.writer.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *gzipResponseWriter) Close() {
	if w.writer != nil {
		_ = w.writer.Close()
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		part = strings.TrimSpace(part)
		if part == "gzip" || strings.HasPrefix(part, "gzip;") {
			return !strings.Contains(part, "q=0")
		}
	}
	return false
}

func compressibleType(ct string) bool {
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/javascript", "application/xml",
		"image/svg+xml", "application/xhtml+xml":
		return true
	}
	return false
}

// withGzip 只包装接受 gzip 的客户端，并交给响应类型决定是否真正压缩。
func WithGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w, request: r}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}
