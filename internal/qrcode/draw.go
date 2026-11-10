package qrcode

import (
	"image"
	"image/color"
	"math"
)

// 图片合成：圆形裁剪的 logo、楔形区小图标、预乘 alpha 混合。

// 默认中心 logo 半径：落在最内圈数据格（r≈116.7）以内，不破坏数据。
const logoBaseR = 108.0

// drawImageCircle 把 src 缩放成 2r 见方，按圆形裁剪后合成到 dst 中心 (cx,cy)。
// 白色底圈由调用方先画好，动图 GIF 才能逐帧复用同一张底图。
func drawImageCircle(dst *image.RGBA, src image.Image, cx, cy, r float64) {
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	if sw <= 0 || sh <= 0 {
		return
	}
	x0 := int(math.Floor(cx - r))
	x1 := int(math.Ceil(cx + r))
	y0 := int(math.Floor(cy - r))
	y1 := int(math.Ceil(cy + r))
	for y := y0; y <= y1; y++ {
		if y < dst.Rect.Min.Y || y >= dst.Rect.Max.Y {
			continue
		}
		py := float64(y) + 0.5 - cy
		for x := x0; x <= x1; x++ {
			if x < dst.Rect.Min.X || x >= dst.Rect.Max.X {
				continue
			}
			px := float64(x) + 0.5 - cx
			d := math.Hypot(px, py)
			if d > r+1 {
				continue
			}
			cov := 1.0
			if d > r-0.5 {
				cov = r + 0.5 - d
				if cov < 0 {
					cov = 0
				} else if cov > 1 {
					cov = 1
				}
			}
			// 双线性采样（预乘 alpha，避免透明区域把颜色拉黑）
			u := (px/(2*r) + 0.5) * sw
			v := (py/(2*r) + 0.5) * sh
			sr, sg, sb, sa := bilinearPremul(src, float64(b.Min.X)+u, float64(b.Min.Y)+v)
			overPixel(dst, x, y, sr*cov, sg*cov, sb*cov, sa*cov)
		}
	}
}

func drawImageBox(dst *image.RGBA, src image.Image, x, y, w, h float64) {
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	if sw <= 0 || sh <= 0 {
		return
	}
	for py := int(math.Floor(y)); py <= int(math.Ceil(y+h)); py++ {
		if py < 0 || py >= dst.Rect.Max.Y {
			continue
		}
		for px := int(math.Floor(x)); px <= int(math.Ceil(x+w)); px++ {
			if px < 0 || px >= dst.Rect.Max.X {
				continue
			}
			u := ((float64(px)+0.5-x)/w*sw - 0.5)
			v := ((float64(py)+0.5-y)/h*sh - 0.5)
			sr, sg, sb, sa := bilinearPremul(src, float64(b.Min.X)+u+0.5, float64(b.Min.Y)+v+0.5)
			overPixel(dst, px, py, sr, sg, sb, sa)
		}
	}
}

// drawImageInk 以「手绘」的方式把 src 贴进 dst 的 (x,y,w,h) 方框。
//
// 与 drawImageBox 的区别只在采样坐标：
//   - 叠一层低频位移场，整块图像像被手捏着按上去的，边缘不在一个正圆上；
//   - 边界附近再叠一点高频噪声，墨迹边界毛毛的，不是刀切一样齐。
//
// 位移幅度按方框边长取比例，所以 600 和 2000 两种输出的手感一致。
func drawImageInk(dst *image.RGBA, src image.Image, x, y, w, h float64) {
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return
	}
	const amp = 0.018 // 边长的 1.8%，600 坐标系下约 2.2px
	// 采样域比图片本身多留 3% 的透明边：徽标图是满幅的（绿圆贴着图边），
	// 不留这点余量，往外抖的那一下就会被削平，圆边上出现直线段。
	const pad = 0.03
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	x1 := int(math.Ceil(x + w))
	y1 := int(math.Ceil(y + h))
	for py := y0; py <= y1; py++ {
		if py < dst.Rect.Min.Y || py >= dst.Rect.Max.Y {
			continue
		}
		for px := x0; px <= x1; px++ {
			if px < dst.Rect.Min.X || px >= dst.Rect.Max.X {
				continue
			}
			nx := (float64(px) + 0.5 - x) / w
			ny := (float64(py) + 0.5 - y) / h
			// 低频（一笔画歪了）+ 高频（笔尖的抖）两段，才像手画的而不是揉皱的
			du := amp * (0.75*math.Sin(4.1*nx+2.3*ny+1.7) +
				0.45*math.Sin(11.7*nx-8.9*ny+0.3) + 0.22*math.Sin(27.3*nx+19.1*ny+2.1))
			dv := amp * (0.75*math.Sin(3.7*ny-2.1*nx+2.9) +
				0.45*math.Sin(10.9*ny+7.3*nx+1.1) + 0.22*math.Sin(25.1*ny-17.9*nx+0.7))
			u := (nx*(1+2*pad) - pad + du) * sw
			v := (ny*(1+2*pad) - pad + dv) * sh
			// 抖到图外就当作透明：bilinearPremul 会把越界坐标钳到边缘像素上，
			// 那样徽标边缘会被"拉"出一条色带。
			if u < 0 || v < 0 || u >= sw || v >= sh {
				continue
			}
			sr, sg, sb, sa := bilinearPremul(src, float64(b.Min.X)+u, float64(b.Min.Y)+v)
			if sa > 0 && sa < 1 {
				// 只在边界带上动手：把覆盖度按高频噪声推一下，边就毛了。
				// 颜色是预乘的，alpha 变了要同比例缩放，否则边界会发白发黑。
				n := 0.5 * (math.Sin(41.3*nx+27.1*ny+0.7) + 0.5*math.Sin(67.9*nx-53.3*ny+1.9))
				na := sa + n*(1-math.Abs(2*sa-1))*0.55
				if na < 0 {
					na = 0
				} else if na > 1 {
					na = 1
				}
				k := na / sa
				sr, sg, sb, sa = sr*k, sg*k, sb*k, na
				if sr > 255 {
					sr = 255
				}
				if sg > 255 {
					sg = 255
				}
				if sb > 255 {
					sb = 255
				}
			}
			overPixel(dst, px, py, sr, sg, sb, sa)
		}
	}
}

func fillWhiteDisc(dst *image.RGBA, cx, cy, r float64) {
	rr := r * r
	for y := int(math.Floor(cy - r)); y <= int(math.Ceil(cy+r)); y++ {
		if y < 0 || y >= dst.Rect.Max.Y {
			continue
		}
		for x := int(math.Floor(cx - r)); x <= int(math.Ceil(cx+r)); x++ {
			if x < 0 || x >= dst.Rect.Max.X {
				continue
			}
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			if dx*dx+dy*dy <= rr {
				dst.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
}

// bilinearPremul 双线性采样，返回预乘 alpha 的颜色与 alpha（均为 0..1）。
func bilinearPremul(src image.Image, x, y float64) (r, g, b, a float64) {
	bnd := src.Bounds()
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	clampX := func(v int) int {
		if v < bnd.Min.X {
			return bnd.Min.X
		}
		if v > bnd.Max.X-1 {
			return bnd.Max.X - 1
		}
		return v
	}
	clampY := func(v int) int {
		if v < bnd.Min.Y {
			return bnd.Min.Y
		}
		if v > bnd.Max.Y-1 {
			return bnd.Max.Y - 1
		}
		return v
	}
	at := func(px, py int) (float64, float64, float64, float64) {
		rr, gg, bb, aa := src.At(clampX(px), clampY(py)).RGBA() // 预乘、0..65535
		return float64(rr) / 257, float64(gg) / 257, float64(bb) / 257, float64(aa) / 65535
	}
	r00, g00, b00, a00 := at(x0, y0)
	r10, g10, b10, a10 := at(x0+1, y0)
	r01, g01, b01, a01 := at(x0, y0+1)
	r11, g11, b11, a11 := at(x0+1, y0+1)
	w00 := (1 - fx) * (1 - fy)
	w10 := fx * (1 - fy)
	w01 := (1 - fx) * fy
	w11 := fx * fy
	mix := func(v00, v10, v01, v11 float64) float64 {
		return v00*w00 + v10*w10 + v01*w01 + v11*w11
	}
	return mix(r00, r10, r01, r11), mix(g00, g10, g01, g11),
		mix(b00, b10, b01, b11), mix(a00, a10, a01, a11)
}

// overPixel 用预乘值做 source-over 合成。
func overPixel(dst *image.RGBA, x, y int, sr, sg, sb, sa float64) {
	if sa <= 0 {
		return
	}
	if sa > 1 {
		sa = 1
	}
	i := dst.PixOffset(x, y)
	inv := 1 - sa
	dst.Pix[i+0] = clamp8(float64(dst.Pix[i+0])*inv + sr)
	dst.Pix[i+1] = clamp8(float64(dst.Pix[i+1])*inv + sg)
	dst.Pix[i+2] = clamp8(float64(dst.Pix[i+2])*inv + sb)
	dst.Pix[i+3] = 255
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}
