package api

import (
	"errors"
	"net/http"

	"wxcode/internal/httpx"
	"wxcode/internal/netutil"
)

var (
	errTextRequired     = errors.New("text 不能为空")
	errMethodNotAllowed = errors.New("只支持 GET / POST")
	errEmptyBody        = errors.New("请求体为空，请上传图片或使用 multipart 字段 file")
	errNoImageField     = errors.New("缺少文件字段 file")
	errNeedImageOrURL   = errors.New("需要 image 或 url 字段")
)

// statusOf 把内部错误映射成合适的 HTTP 状态码。
func StatusOf(err error) int {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, httpx.ErrBodyTooLarge), errors.As(err, &maxErr):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, netutil.ErrPrivateAddr):
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}
