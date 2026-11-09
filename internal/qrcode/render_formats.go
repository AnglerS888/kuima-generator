package qrcode

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
)

// 其它输出格式：JPEG 与 GIF（动图 logo 逐帧合成）。

// gifMaxFrames 一帧动图最多合成多少张，防止上传 200 帧的 GIF 把 CPU 打满。
const gifMaxFrames = 20

// RenderJPEG 输出 JPEG（无 alpha，白底；质量 92）。
func RenderJPEG(info *EncodeInfo, opt RenderOptions) ([]byte, error) {
	img := RenderImage(info, opt)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderGIF 输出 GIF：
//   - logo 是动图（LogoFrames 非空）时，逐帧合成，输出循环动图；
//   - 否则输出单帧 GIF（尺寸更小，适合当普通图片用）。
func RenderGIF(info *EncodeInfo, opt RenderOptions) ([]byte, error) {
	base := renderBase(info, opt)

	frames := opt.LogoFrames
	delays := opt.LogoDelays
	if len(frames) == 0 && opt.Logo != nil {
		frames = []image.Image{opt.Logo}
	}
	if len(frames) > gifMaxFrames {
		frames = frames[:gifMaxFrames]
		delays = delays[:min(len(delays), gifMaxFrames)]
	}

	// 静态 GIF：没有动图 logo，直接编码底图
	if len(frames) <= 1 {
		canvas := base
		if len(frames) == 1 && frames[0] != nil {
			cx, cy, rad := logoGeometry(opt)
			fillWhiteDisc(canvas, cx, cy, rad+3)
			drawImageCircle(canvas, frames[0], cx, cy, rad)
		}
		applyWatermark(canvas, opt)
		var buf bytes.Buffer
		if err := gif.Encode(&buf, toPaletted(canvas), nil); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	cx, cy, rad := logoGeometry(opt)
	fillWhiteDisc(base, cx, cy, rad+3)
	out := &gif.GIF{LoopCount: 0} // 0 = 无限循环
	cache := palCache{}
	for i, f := range frames {
		canvas := base
		// 第一帧直接画在底图上，后面每帧都从底图复制，避免 logo 累积。
		// 有水印时必须每帧都复制：水印画在 copy 上才不会叠到下一帧。
		if i > 0 || opt.Watermark != nil {
			canvas = CloneRGBA(base)
		}
		if f != nil {
			drawImageCircle(canvas, f, cx, cy, rad)
		}
		applyWatermark(canvas, opt)
		out.Image = append(out.Image, toPalettedShared(canvas, cache))
		out.Delay = append(out.Delay, gifDelay(delays, i))
		out.Disposal = append(out.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gifDelay 取第 i 帧延时（1/100 秒），缺省 10，且不小于 2 以防部分解码器加速。
func gifDelay(delays []int, i int) int {
	d := 10
	if i < len(delays) && delays[i] > 0 {
		d = delays[i]
	}
	if d < 2 {
		d = 2
	}
	return d
}

// palCache 缓存 RGB → 调色板下标。码图里绝大多数像素只有少数几种颜色，
// 缓存后 600×600 的量化从 ~600ms 降到十几毫秒。
type palCache map[uint32]uint8

func (c palCache) index(p color.Palette, r, g, b uint8) uint8 {
	key := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	if idx, ok := c[key]; ok {
		return idx
	}
	idx := uint8(p.Index(color.RGBA{R: r, G: g, B: b, A: 255}))
	c[key] = idx
	return idx
}

// toPaletted 转成 256 色调色板图。用最近色而不是抖动：
// 码点是纯黑白块，抖动会引入噪点、拉低扫描容错。
func toPaletted(src *image.RGBA) *image.Paletted {
	return toPalettedShared(src, palCache{})
}

// toPalettedShared 复用颜色缓存（动图多帧共用一份，越往后越快）。
func toPalettedShared(src *image.RGBA, cache palCache) *image.Paletted {
	p := image.NewPaletted(src.Bounds(), palette.Plan9)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	for y := 0; y < h; y++ {
		srow := src.Pix[y*src.Stride:]
		drow := p.Pix[y*p.Stride:]
		for x := 0; x < w; x++ {
			s := srow[x*4:]
			drow[x] = cache.index(p.Palette, s[0], s[1], s[2])
		}
	}
	return p
}

// cloneRGBA 深拷贝位图（GIF 逐帧合成用）。
func CloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}
