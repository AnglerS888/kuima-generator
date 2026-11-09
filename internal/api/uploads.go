package api

import (
	"net/http"

	"wxcode/internal/store"
)

// 上传大小限制：按「未登录 / 已登录」分别配置，单位 KB。
const minUploadKB = 128

const maxUploadKB = 64 * 1024 // 64MB

// uploadLimitBytes 返回本次请求允许的单张图片字节数。
// logo=true 表示生成时上传的中心图；false 表示解析接口上传的待识别图片。
func uploadLimitBytes(r *http.Request, logo bool) int64 {
	st := store.LoadSettings()
	kb := st.AnonImageKB
	if logo {
		kb = st.AnonLogoKB
	}
	if u := currentUser(r); u != nil {
		if logo {
			kb = st.UserLogoKB
		} else {
			kb = st.UserImageKB
		}
	}
	kb = clampUploadKB(kb)
	return int64(kb) << 10
}

func clampUploadKB(kb int) int {
	if kb < minUploadKB {
		return minUploadKB
	}
	if kb > maxUploadKB {
		return maxUploadKB
	}
	return kb
}
