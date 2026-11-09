package imagex

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"wxcode/internal/httpx"
	"wxcode/internal/netutil"
	"wxcode/internal/qrcode"
	webassets "wxcode/web"
)

// 图片加载：logo / badge 支持 data URL、http(s) URL、裸 base64 与本地路径。

// ============================================================
// 图片加载
// ============================================================

// imageRef 是解析好的图片：既给位图渲染用，也能给 SVG 用 data URI 引用。
// 动图 GIF 会额外带 frames/delays，输出 GIF 时逐帧合成。
type ImageRef struct {
	img      image.Image
	frames   []image.Image // 动图帧（已按 disposal 合成），静态图为空
	delays   []int         // 每帧延时（1/100 秒）
	animated bool
	dataURI  string
	raw      []byte
	mime     string
}

const MaxImageBytes = 8 << 20 // 单张图片上限 8MB

const MaxJSONBody = 16 << 20 // JSON 请求体上限

const MaxUploadBody = 16 << 20 // multipart 上限

const AllowRemoteImage = true // 远程图片可用，但只能访问公网地址（见 safe.go）

// loadImageRef 支持 data URL、http(s) URL、裸 base64 与本地文件路径。
func LoadImageRef(spec string, allowRemote bool, max int64) (*ImageRef, error) {
	return LoadImageRefContext(context.Background(), spec, allowRemote, max)
}

// loadImageRefContext 是 loadImageRef 的可取消版本；远程下载会跟随请求一起取消。
func LoadImageRefContext(ctx context.Context, spec string, allowRemote bool, max int64) (*ImageRef, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "none" || spec == "off" {
		return nil, nil
	}

	var (
		raw     []byte
		mimeStr string
	)
	switch {
	case strings.HasPrefix(spec, "data:"):
		comma := strings.Index(spec, ",")
		if comma < 0 {
			return nil, fmt.Errorf("data URL 格式错误")
		}
		meta := spec[5:comma]
		mimeStr = meta
		if i := strings.Index(meta, ";"); i >= 0 {
			mimeStr = meta[:i]
		}
		if strings.Contains(meta, "base64") {
			b, err := base64.StdEncoding.DecodeString(spec[comma+1:])
			if err != nil {
				return nil, fmt.Errorf("data URL base64 解码失败: %w", err)
			}
			raw = b
		} else {
			u, err := url.QueryUnescape(spec[comma+1:])
			if err != nil {
				return nil, err
			}
			raw = []byte(u)
		}

	case strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://"):
		if !allowRemote {
			return nil, fmt.Errorf("已禁用远程图片")
		}
		if err := netutil.ValidateRemoteURL(spec); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec, nil)
		if err != nil {
			return nil, fmt.Errorf("下载图片失败: %w", err)
		}
		resp, err := netutil.RemoteHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("下载图片失败: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
		}
		if resp.ContentLength > max {
			return nil, httpx.ErrBodyTooLarge
		}
		raw, err = httpx.ReadBody(resp.Body, max)
		if err != nil {
			return nil, err
		}
		mimeStr = resp.Header.Get("Content-Type")

	default:
		// 本地文件（默认关闭，防任意文件读取）或裸 base64。
		// 本地文件也走带硬上限的读取，不能用一个超大文件耗尽内存或磁盘 IO。
		read := false
		if allowLocalFile() && !strings.Contains(spec, "\n") && len(spec) < 4096 {
			if f, err := os.Open(spec); err == nil {
				raw, err = httpx.ReadBody(f, max)
				_ = f.Close()
				if err != nil {
					return nil, err
				}
				read = true
			}
		}
		if !read {
			b, derr := base64.StdEncoding.DecodeString(strings.TrimPrefix(spec, "base64,"))
			if derr != nil {
				return nil, fmt.Errorf("无法识别图片参数（既不是可访问的远程地址，也不是 base64）")
			}
			raw = b
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("图片内容为空")
	}
	if int64(len(raw)) > max {
		return nil, httpx.ErrBodyTooLarge
	}
	return newImageRef(raw, mimeStr)
}

// loadImageRefFromMultipart 读取上传的 logo 文件。
func LoadImageRefFromMultipart(fh *multipart.FileHeader, max int64) (*ImageRef, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := httpx.ReadBody(f, max)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return newImageRef(raw, "")
}

// newImageRef 解码图片并生成 data URI。GIF 若有多帧，会额外解析出动画帧。
func newImageRef(raw []byte, _ string) (*ImageRef, error) {
	// 先用 DecodeConfig 看尺寸，避免「几 KB 文件 → 几 GB 内存」的解压炸弹
	cfg, _, cerr := image.DecodeConfig(bytes.NewReader(raw))
	if cerr == nil {
		if err := checkImageConfig(cfg); err != nil {
			return nil, err
		}
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("无法解析图片（支持 PNG/JPG/GIF）: %w", err)
	}
	// data URI 的 MIME 只能取实际解码格式，绝不沿用客户端或远程服务器给的
	// Content-Type。后者会原样进入 SVG 的 xlink:href 属性，带引号的 MIME 可造成注入。
	var mimeStr string
	switch format {
	case "png", "gif":
		mimeStr = "image/" + format
	case "jpeg":
		mimeStr = "image/jpeg"
	default:
		return nil, fmt.Errorf("不支持的图片格式: %s", format)
	}
	ref := &ImageRef{
		img:     img,
		raw:     raw,
		mime:    mimeStr,
		dataURI: "data:" + mimeStr + ";base64," + base64.StdEncoding.EncodeToString(raw),
	}
	if format == "gif" {
		if frames, delays, aerr := decodeGIFAnimation(raw); aerr == nil && len(frames) > 1 {
			ref.frames = frames
			ref.delays = delays
			ref.animated = true
		}
	}
	return ref, nil
}

// gifAnimationAffordable 在 DecodeAll 之前解析 GIF 块结构并核算帧数/像素预算。
// gif.DecodeAll 会一次性解出所有帧；事后再裁剪帧数已经太晚，必须先挡住超多帧输入。
func gifAnimationAffordable(raw []byte, width, height int) bool {
	if len(raw) < 14 || width <= 0 || height <= 0 {
		return false
	}
	pos := 13
	if raw[10]&0x80 != 0 { // global color table
		pos += 3 * (1 << ((raw[10] & 7) + 1))
	}
	frames := 0
	skipSubBlocks := func(at int) (int, bool) {
		for {
			if at >= len(raw) {
				return at, false
			}
			n := int(raw[at])
			at++
			if n == 0 {
				return at, true
			}
			if n > len(raw)-at {
				return at, false
			}
			at += n
		}
	}
	for pos < len(raw) {
		switch raw[pos] {
		case 0x3b: // trailer
			return frames > 0 && frames <= netutil.MaxGIFFrames && frames*width*height <= netutil.MaxAnimPixels
		case 0x21: // extension: label + data sub-blocks
			if pos+2 > len(raw) {
				return false
			}
			next, ok := skipSubBlocks(pos + 2)
			if !ok {
				return false
			}
			pos = next
		case 0x2c: // image descriptor
			if pos+10 > len(raw) {
				return false
			}
			frames++
			if frames > netutil.MaxGIFFrames || frames > netutil.MaxAnimPixels/(width*height) {
				return false
			}
			packed := raw[pos+9]
			pos += 10
			if packed&0x80 != 0 { // local color table
				pos += 3 * (1 << ((packed & 7) + 1))
			}
			if pos >= len(raw) {
				return false
			}
			pos++ // LZW minimum code size
			next, ok := skipSubBlocks(pos)
			if !ok {
				return false
			}
			pos = next
		default:
			return false
		}
	}
	return false
}

// decodeGIFAnimation 把多帧 GIF 合成为完整画面序列（按 Disposal 规则处理帧间差异）。
func decodeGIFAnimation(raw []byte) ([]image.Image, []int, error) {
	cfg, err := gif.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	if err := checkImageConfig(image.Config{Width: cfg.Width, Height: cfg.Height}); err != nil {
		return nil, nil, err
	}
	if !gifAnimationAffordable(raw, cfg.Width, cfg.Height) {
		return nil, nil, fmt.Errorf("GIF 帧数或总像素超过安全限制")
	}
	g, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	if len(g.Image) <= 1 {
		return nil, nil, nil
	}
	w, h := g.Config.Width, g.Config.Height
	if w <= 0 || h <= 0 {
		return nil, nil, fmt.Errorf("GIF 尺寸异常")
	}
	// 帧数 × 尺寸预算：防止「一帧很小但几千帧」把内存吃光
	n := len(g.Image)
	if n > netutil.MaxGIFFrames {
		n = netutil.MaxGIFFrames
	}
	if budget := netutil.MaxAnimPixels / (w * h); budget < n {
		n = budget
		if n < 1 {
			n = 1
		}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	frames := make([]image.Image, 0, n)
	delays := make([]int, 0, len(g.Image))
	transparent := image.NewUniform(color.Transparent)

	for i, frame := range g.Image[:n] {
		bounds := frame.Bounds()
		saved := qrcode.CloneRGBA(canvas)

		draw.Draw(canvas, bounds, frame, bounds.Min, draw.Over)
		frames = append(frames, qrcode.CloneRGBA(canvas))

		delay := 10
		if i < len(g.Delay) {
			delay = g.Delay[i]
		}
		if delay < 2 {
			delay = 2
		}
		delays = append(delays, delay)

		disposal := byte(gif.DisposalNone)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, bounds, transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = saved
		}
	}
	return frames, delays, nil
}

func ImageOf(r *ImageRef) image.Image {
	if r == nil {
		return nil
	}
	return r.img
}

func FramesOf(r *ImageRef) []image.Image {
	if r == nil {
		return nil
	}
	return r.frames
}

func DelaysOf(r *ImageRef) []int {
	if r == nil {
		return nil
	}
	return r.delays
}

// ============================================================
// 角落徽标（微信小程序图标）
//
// 原生 SVG 里是一个指向 mmbiz.qpic.cn 的 <image>，渲染成位图时没有这一步，
// 所以 PNG/JPG/GIF 的楔形缺口一直是空的。这里把同一张图内嵌进二进制，
// 位图默认画在 (419.59,419.59) 122.22×122.22 的位置，与 SVG 完全对齐。
// ============================================================

var (
	badgeOnce  sync.Once
	badgeCache image.Image
	badgeErr   error
)

// embeddedBadge 懒加载内嵌徽标（只解码一次）。
func embeddedBadge() (image.Image, error) {
	badgeOnce.Do(func() {
		img, _, err := image.Decode(bytes.NewReader(webassets.BadgePNG))
		if err != nil {
			badgeErr = fmt.Errorf("内置徽标解码失败: %w", err)
			return
		}
		badgeCache = img
	})
	return badgeCache, badgeErr
}

// badgeDataURI 把内嵌徽标转成 data URI（只算一次）。
// 用它替换原来的 http://mmbiz.qpic.cn/... 后，导出的 SVG 不联网也能显示徽标。
var (
	badgeURITime  sync.Once
	badgeURICache string
)

func badgeDataURI() string {
	badgeURITime.Do(func() {
		badgeURICache = "data:image/png;base64," + base64.StdEncoding.EncodeToString(webassets.BadgePNG)
	})
	return badgeURICache
}

// resolveBadge 解析 badge 参数：
//   - ""/default/on/true/1 → 用内置徽标，SVG 里写 data URI（离线可用）
//   - native               → 用内置徽标，SVG 里写原生 mmbiz URL（需要与原生逐字节一致时用）
//   - none/off/false/0     → 不画
//   - 其它                 → 当作图片源（URL / data URL / base64）
//
// 返回：位图要画的图（nil 表示不画）、SVG 里的 href（"none" = 不画，其余原样写入）。
func ResolveBadge(spec string) (image.Image, string, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "none", "off", "false", "0":
		return nil, "none", nil
	case "native", "remote", "origin":
		img, err := embeddedBadge()
		return img, "", err
	case "", "default", "on", "true", "1":
		img, err := embeddedBadge()
		return img, badgeDataURI(), err
	default:
		ref, err := LoadImageRef(spec, AllowRemoteImage, MaxImageBytes)
		if err != nil {
			return nil, "", err
		}
		if ref == nil {
			return nil, "none", nil
		}
		return ref.img, ref.dataURI, nil
	}
}

// animInfo 返回动图描述，给前端展示用。
func AnimInfo(r *ImageRef) (animated bool, frames int) {
	if r == nil {
		return false, 0
	}
	return r.animated, len(r.frames)
}

func DataURIOf(r *ImageRef) string {
	if r == nil {
		return ""
	}
	return r.dataURI
}
