package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"wxcode/internal/httpx"
)

// ============================================================
// 生成结果短链（临时图片地址）
//
// 为什么需要它：手机端必须拿到「真正的 http(s) 图片地址」才能保存。
//   - 微信内置浏览器、iOS Safari 会直接忽略 <a download href="blob:...">，
//     点了毫无反应（甚至只把 blob 当页面打开）；
//   - blob: / data: 图片在长按时没有「保存图片 / 存储到照片」菜单。
// 所以每次生成成功后，把图片字节在内存里留一小会儿，用一个不可猜的随机
// id 暴露成 GET /api/code/{id}.{ext}，前端把 <img src> 指向这里，
// 下载与长按保存就都走普通图片那条路。
//
// 取舍：不落库、不落盘，进程重启即失效；id 是 128 bit 随机数，
// 猜不到就等于「只有拿到响应头的人能访问」，因此不再挂登录校验
// （图片本身已按请求时的身份渲染、水印也已经在字节里了）。
// ============================================================

const (
	assetTTL      = 30 * time.Minute // 地址有效期：够用户打开、长按、复制分享
	assetMaxBytes = 64 << 20         // 所有图片加起来的内存上限
	assetMaxItem  = 8 << 20          // 单张上限，过大的图不进缓存（前端会退回 blob）
)

type assetEntry struct {
	body  []byte
	ctype string
	ext   string
	name  string
	at    time.Time
}

var (
	assetMu    sync.Mutex
	assetItems = make(map[string]*assetEntry)
	assetTotal int
)

// publishImage 把一次生成结果放进内存，返回短 id（不进缓存时返回空串）。
func publishImage(body []byte, ctype, ext, name string) string {
	if len(body) == 0 || len(body) > assetMaxItem {
		return ""
	}
	id := newImageID()
	now := time.Now()

	assetMu.Lock()
	defer assetMu.Unlock()
	evictAssetsLocked(now, len(body))
	assetItems[id] = &assetEntry{body: body, ctype: ctype, ext: ext, name: name, at: now}
	assetTotal += len(body)
	return id
}

// lookupImage 按 id 取图；过期的顺手删掉。
func lookupImage(id string) *assetEntry {
	assetMu.Lock()
	defer assetMu.Unlock()
	e := assetItems[id]
	if e == nil {
		return nil
	}
	if time.Since(e.at) > assetTTL {
		delete(assetItems, id)
		assetTotal -= len(e.body)
		return nil
	}
	return e
}

// evictAssetsLocked 先清过期，再按时间从旧到新删，直到放得下新图。
func evictAssetsLocked(now time.Time, incoming int) {
	for k, e := range assetItems {
		if now.Sub(e.at) > assetTTL {
			delete(assetItems, k)
			assetTotal -= len(e.body)
		}
	}
	for assetTotal+incoming > assetMaxBytes && len(assetItems) > 0 {
		var oldestKey string
		var oldest time.Time
		for k, e := range assetItems {
			if oldestKey == "" || e.at.Before(oldest) {
				oldestKey, oldest = k, e.at
			}
		}
		assetTotal -= len(assetItems[oldestKey].body)
		delete(assetItems, oldestKey)
	}
}

// newImageID 生成 128 bit 随机 id 的十六进制串（32 字符）。
func newImageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 不可用属于系统级故障，退化成时间戳也只能保证不撞
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// handleCodeAsset 返回刚生成的图片。
//
// GET /api/code/{name}   name = 32 位十六进制 id + 可选 ".png/.jpg/.gif/.svg"
// 后缀只是为了长按保存时文件名好看，不参与查找。
func handleCodeAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	id := name
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		id = name[:i]
	}
	e := lookupImage(id)
	if e == nil {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("图片已过期，请重新生成"))
		return
	}

	h := w.Header()
	h.Set("Content-Type", e.ctype)
	h.Set("Content-Length", strconv.Itoa(len(e.body)))
	// inline + 文件名：安卓微信/浏览器长按保存时用它命名
	h.Set("Content-Disposition", `inline; filename="`+e.name+`"`)
	// 内容不会变，但地址是临时的：短缓存，长按和重复打开都不必再打服务端
	h.Set("Cache-Control", "private, max-age=900")
	h.Set("ETag", `"`+id+`"`)
	w.Write(e.body)
}
