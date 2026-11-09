package imagex

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// 预设 LOGO：管理员在后台传一批，用户在生成页一键选用。
//
// 这里只做「能不能存、存多大」的整理，不裁方也不改比例 ——
// 中心 logo 是按外接方形映射进圆形的（见 qrcode.drawImageCircle），
// 比例由图片自己决定，服务端不该偷偷裁掉一圈。

// PresetLogoMaxSide 是预设 LOGO 落库的边长上限。
//
// 码图里 logo 直径约 108/600：size 取到上限 2000 时也就 360px，
// 512 已经足够清晰，再大只是白占库体积和前端带宽。
const PresetLogoMaxSide = 512

// PresetLogo 把上传的图片整理成可长期保存的字节，返回 (字节, MIME, error)。
//
//   - GIF 原样保留：动图 logo 逐帧替换是现有能力，重新编码会把动画丢掉
//   - 其它格式超过 maxSide 时等比缩进框内并转 PNG（保留透明通道）
//   - 尺寸已达标则原样保存：避免无意义的重新编码，JPG 也不会被转成 PNG
func PresetLogo(raw []byte, maxSide int) ([]byte, string, error) {
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("图片内容为空")
	}
	// 先只看头部：几 KB 的「30000×30000」PNG 足以把进程内存打爆
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("无法识别图片（支持 PNG / JPG / GIF）: %w", err)
	}
	if err := checkImageConfig(cfg); err != nil {
		return nil, "", err
	}
	switch format {
	case "gif":
		return raw, "image/gif", nil
	case "png", "jpeg":
	default:
		return nil, "", fmt.Errorf("不支持的图片格式: %s（支持 PNG / JPG / GIF）", format)
	}
	if maxSide <= 0 {
		maxSide = PresetLogoMaxSide
	}
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
	}
	if cfg.Width <= maxSide && cfg.Height <= maxSide {
		return raw, mime, nil
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("无法解析图片: %w", err)
	}
	nw, nh := fitSize(cfg.Width, cfg.Height, maxSide)
	var buf bytes.Buffer
	if err := png.Encode(&buf, resizeArea(src, nw, nh)); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

// fitSize 把 w×h 等比缩进 max×max 的框里（只缩不放）。
func fitSize(w, h, max int) (int, int) {
	if w <= 0 || h <= 0 {
		return max, max
	}
	if w >= h {
		nw := max
		nh := h * max / w
		if nh < 1 {
			nh = 1
		}
		return nw, nh
	}
	nh := max
	nw := w * max / h
	if nw < 1 {
		nw = 1
	}
	return nw, nh
}

// resizeArea 用面积平均缩图：比最近邻干净得多，半透明边缘也不会发黑。
//
// 源像素的 RGBA() 返回的是预乘 alpha 的 16 位值，平均值直接写回
// image.RGBA（同样是预乘）即可，不需要再反预乘。
func resizeArea(src image.Image, nw, nh int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*sh/nh, (y+1)*sh/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > sh {
			y1 = sh
		}
		for x := 0; x < nw; x++ {
			x0, x1 := x*sw/nw, (x+1)*sw/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > sw {
				x1 = sw
			}
			var sr, sg, sb, sa, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					sr += uint64(cr)
					sg += uint64(cg)
					sb += uint64(cb)
					sa += uint64(ca)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((sr / n) >> 8),
				G: uint8((sg / n) >> 8),
				B: uint8((sb / n) >> 8),
				A: uint8((sa / n) >> 8),
			})
		}
	}
	return dst
}
