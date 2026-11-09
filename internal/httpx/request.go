package httpx

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"wxcode/internal/config"
)

// apiKeyFrom 依次看三个位置：X-API-Key 头、Authorization: Bearer、?key=
func APIKeyFrom(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if v, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("key"))
}

// ---------- Cookie ----------
func CookieSecure(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(config.GetenvDefault("COOKIE_SECURE", "")), "1") {
		return true
	}
	if r.TLS != nil {
		return true
	}
	return FirstHeaderValue(r.Header.Get("X-Forwarded-Proto")) == "https"
}

// ============================================================
// 站点根地址
// ============================================================

// requestBaseURL 还原"用户浏览器地址栏里那把地址"（协议 + 域名），
// 首页示例代码、导出的 md 文档都用它，避免写死 localhost:8080。
//
// 三种部署形态都要照顾：
//   - 直连：Host 头就是用户访问的域名（含端口）
//   - 反向代理：Host 常被改写成内网地址，真实域名在 X-Forwarded-Host，协议在 X-Forwarded-Proto
//   - 本机调试：Host 是 127.0.0.1:8080，如实回显即可
//
// 这些头只决定展示文案、不参与鉴权，被伪造最多是"文档里显示别人的域名"；即便如此仍过一遍字符白名单，
// 免得把奇怪的 Host 拼进 HTML。
//
// 注：反代挂在子路径（如 /wxcode/）时拿不到前缀 —— handleIndex 只认 "/"，应用侧无从得知，
// 那种部署得自己补前缀。这里刻意不猜 X-Forwarded-Prefix。
func RequestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := FirstHeaderValue(r.Header.Get("X-Forwarded-Proto")); p == "https" || p == "http" {
		scheme = p
	}
	host := r.Host
	if h := FirstHeaderValue(r.Header.Get("X-Forwarded-Host")); h != "" {
		host = h
	}
	if !ValidBaseHost(host) {
		// Host 缺失或含非法字符时退回占位地址，总比在文档里甩出半截链接好
		host = "localhost:8080"
	}
	return scheme + "://" + host
}

// firstHeaderValue 取逗号分隔转发头的第一个值（"https, http" → "https"）。
func FirstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// validBaseHost 只放行域名 / IPv4 / IPv6 字面量里可能出现的字符，
// 顺带挡掉把引号、尖括号塞进 Host 头做 HTML 注入的路子。
func ValidBaseHost(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == ':', c == '[', c == ']', c == '_':
		default:
			return false
		}
	}
	return true
}

// jsonUnmarshal 是 json.Unmarshal 的薄包装，便于统一替换实现。
func UnmarshalJSON(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

// readBody 读取请求体并限制大小，防止超大上传打爆内存。
// 多读 1 字节用于判断是否超限：截断后静默处理会让 JSON 报出误导性的解析错误。
func ReadBody(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrBodyTooLarge
	}
	return b, nil
}

// capRequestBody 限制整个 HTTP 请求体。ParseMultipartForm 的参数只是内存阈值，
// 超出部分会写临时文件，必须另设硬上限才能防止无限上传耗尽磁盘。
func CapRequestBody(r *http.Request, limit int64) error {
	if r.ContentLength > limit {
		return ErrBodyTooLarge
	}
	r.Body = http.MaxBytesReader(nil, r.Body, limit)
	return nil
}

// isJSON 判断 Content-Type 是否为 JSON。
func IsJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/json")
}

// isMultipart 判断 Content-Type 是否为 multipart 表单。
func IsMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}
