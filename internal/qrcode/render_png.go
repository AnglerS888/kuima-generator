package qrcode

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"wxcode/internal/watermark"
)

// PNG 渲染入口：按渲染选项拼出 RGBA 位图并编码。

// renderBase 渲染底色 + 码点 + 楔形区小图标（不含中心 logo）。
// 动图 GIF 需要逐帧复用同一张底图，所以把 logo 单独叠加。
func renderBase(info *EncodeInfo, opt RenderOptions) *image.RGBA {
	size := opt.Size
	if size <= 0 {
		size = 600
	}
	margin := opt.Margin
	if margin < 0 {
		margin = 0
	}
	total := size + margin*2
	scale := float64(size) / 600.0
	ss := 4
	if size > 800 {
		ss = 2
	}
	for total*ss > 8000 {
		ss--
	}
	if ss < 1 {
		ss = 1
	}
	unit := scale * float64(ss)

	fg, bg := opt.Fg, opt.Bg
	if fg.A == 0 {
		fg = color.RGBA{0, 0, 0, 255}
	}
	if bg.A == 0 {
		bg = color.RGBA{255, 255, 255, 255}
	}
	style := ParseStyle(opt.Style)
	colorMode := ParseColorMode(opt.ColorMode)
	if ghostActive(colorMode) {
		// 隐形配色：墨色由底色推出来（只压几个色阶），前景色不参与
		fg = ghostInk(bg, opt.GhostLevel)
	}
	ink := runInk(colorMode)    // 每笔画一色：彩虹 / 色环
	rings := ringInk(colorMode) // 定位环的取色
	colored := ink != nil || rings != nil

	W := total * ss
	r := &raster{w: W, h: W, mask: make([]byte, W*W)}
	if colored {
		// 只有彩色风格才分配调色板与像素色号，黑白风格走原来的单色合成
		r.pal = newInkPal(fg)
		r.idx = make([]byte, W*W)
	}
	toDev := func(x, y float64) (float64, float64) {
		return (x*scale + float64(margin)) * float64(ss), (y*scale + float64(margin)) * float64(ss)
	}

	switch style {
	case StyleHand:
		// 手绘：整枚码按「墨线」重画（含手绘定位环），坐标逐点换算到设备像素。
		for i, pass := range handStrokes(info.Grid) {
			pts := make([]inkPt, len(pass.Pts))
			for k, p := range pass.Pts {
				x, y := toDev(p.X, p.Y)
				pts[k] = inkPt{X: x, Y: y, W: p.W * unit}
			}
			if colored {
				takeInk(r, handInk(colorMode, i, pass))
			}
			r.inkStroke(pts)
		}
	case StyleDot:
		drawDotStyle(r, info.Grid, toDev, unit, ink)
		drawRings(r, toDev, unit, rings)
	case StyleBlock:
		drawBlockStyle(r, info.Grid, toDev, unit, ink)
		drawRings(r, toDev, unit, rings)
	case StyleLine:
		drawLineStyle(r, info.Grid, toDev, unit, float64(ss), ink)
		drawRings(r, toDev, unit, rings)
	case StyleClassic:
		if colored {
			drawRunsInk(r, info.Grid, toDev, unit, ink)
			drawRings(r, toDev, unit, rings)
			break
		}
		// 经典：等宽胶囊 + 三组定位环（与微信原生输出逐字节一致）
		for _, run := range gridRuns(info.Grid) {
			x1, y1 := cellPos(run.Row, run.Col1)
			x2, y2 := cellPos(run.Row, run.Col2)
			dx1, dy1 := toDev(x1, y1)
			dx2, dy2 := toDev(x2, y2)
			r.fillCapsule(dx1, dy1, dx2, dy2, wxStroke*unit)
		}
		for _, c := range finderCenters {
			cx, cy := toDev(c[0], c[1])
			r.fillDisc(cx, cy, 2.5*float64(wxSize)*unit)
			r.clearDisc(cx, cy, 1.7*float64(wxSize)*unit)
			r.fillDisc(cx, cy, 0.5*float64(wxSize)*unit)
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, total, total))
	if r.idx == nil {
		// 单色：按覆盖率把前景色和背景色混起来
		inv := 1.0 / float64(ss*ss)
		for y := 0; y < total; y++ {
			for x := 0; x < total; x++ {
				s := 0
				for dy := 0; dy < ss; dy++ {
					row := (y*ss + dy) * W
					for dx := 0; dx < ss; dx++ {
						s += int(r.mask[row+x*ss+dx])
					}
				}
				a := float64(s) * inv
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(math.Round(float64(bg.R)*(1-a) + float64(fg.R)*a)),
					G: uint8(math.Round(float64(bg.G)*(1-a) + float64(fg.G)*a)),
					B: uint8(math.Round(float64(bg.B)*(1-a) + float64(fg.B)*a)),
					A: 255,
				})
			}
		}
	} else {
		compositeColor(img, r, total, ss, bg)
	}

	// 隐形模式下不画徽标：一个彩色小图标会把"这是码"直接说出来
	if opt.Badge != nil && !ghostActive(colorMode) {
		bx := (419.593964*scale + float64(margin))
		bs := (float64(wxSize) + float64(wxSize)*10.0) * scale
		if style == StyleHand {
			// 手绘风格下徽标也要"手贴"上去：略微歪、边缘不齐，否则一块干净的
			// 数字图标贴在满纸手绘笔迹里会很扎眼。
			drawImageInk(img, opt.Badge, bx, bx, bs, bs)
		} else {
			drawImageBox(img, opt.Badge, bx, bx, bs, bs)
		}
	}
	return img
}

// compositeColor 是彩色风格的合成：覆盖度照旧，颜色取这一小片像素的平均墨色。
func compositeColor(img *image.RGBA, r *raster, total, ss int, bg color.RGBA) {
	inv := 1.0 / float64(ss*ss)
	W := r.w
	for y := 0; y < total; y++ {
		for x := 0; x < total; x++ {
			var n, sr, sg, sb int
			for dy := 0; dy < ss; dy++ {
				row := (y*ss + dy) * W
				for dx := 0; dx < ss; dx++ {
					i := row + x*ss + dx
					if r.mask[i] == 0 {
						continue
					}
					c := r.pal.cols[r.idx[i]]
					sr += int(c.R)
					sg += int(c.G)
					sb += int(c.B)
					n++
				}
			}
			if n == 0 {
				img.SetRGBA(x, y, color.RGBA{bg.R, bg.G, bg.B, 255})
				continue
			}
			a := float64(n) * inv
			ir := float64(sr) / float64(n)
			ig := float64(sg) / float64(n)
			ib := float64(sb) / float64(n)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Round(float64(bg.R)*(1-a) + ir*a)),
				G: uint8(math.Round(float64(bg.G)*(1-a) + ig*a)),
				B: uint8(math.Round(float64(bg.B)*(1-a) + ib*a)),
				A: 255,
			})
		}
	}
}

// logoGeometry 计算中心 logo 的圆心与半径（输出像素）。
func logoGeometry(opt RenderOptions) (cx, cy, r float64) {
	size := opt.Size
	if size <= 0 {
		size = 600
	}
	margin := opt.Margin
	if margin < 0 {
		margin = 0
	}
	ratio := opt.LogoRatio
	if ratio <= 0 {
		ratio = 1
	}
	cx = float64(margin) + 300*float64(size)/600.0
	return cx, cx, logoBaseR * ratio * float64(size) / 600.0
}

// RenderImage 把葵花码渲染成 RGBA 位图（含中心 logo 与文字水印）。
func RenderImage(info *EncodeInfo, opt RenderOptions) *image.RGBA {
	img := renderBase(info, opt)
	if opt.Logo != nil {
		cx, cy, rad := logoGeometry(opt)
		fillWhiteDisc(img, cx, cy, rad+3)
		drawImageCircle(img, opt.Logo, cx, cy, rad)
	}
	applyWatermark(img, opt)
	return img
}

// applyWatermark 在最终图像上叠字。放在 logo 之后，保证水印压在最上层。
func applyWatermark(img *image.RGBA, opt RenderOptions) {
	if opt.Watermark == nil || opt.Watermark.Text == "" {
		return
	}
	watermark.Draw(img, *opt.Watermark)
}

// RenderPNG 直接返回 PNG 字节。
func RenderPNG(info *EncodeInfo, opt RenderOptions) ([]byte, error) {
	img := RenderImage(info, opt)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
