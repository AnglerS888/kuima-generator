package qrcode

import (
	"image/color"
	"math"
)

// 位图风格的画法：圆点 / 方块 / 线描，以及「每笔画什么颜色」。
//
// 这几种风格都是整格画的（不像手绘要按余量算摆动），所以格心一定落墨；
// 它们在解码端的表现和经典风格基本一致。

// devFn 把 600 坐标系的点换算到设备像素（含白边与超采样倍率）。
type devFn func(x, y float64) (float64, float64)

// inkAt 返回某一段墨该用什么颜色；返回零值表示"用前景色"。
type inkAt func(row, col1, col2 int) color.RGBA

// takeInk 把这一段的颜色登记进调色板，并设为当前笔色。
func takeInk(r *raster, c color.RGBA) {
	if r.pal == nil || c.A == 0 {
		r.cur = 0
		return
	}
	r.cur = r.pal.index(c)
}

// quantT 把配色进度量化成 1/40 步：调色板不会因为每个游程半径略有差别就爆掉。
func quantT(t float64) float64 { return math.Round(clamp01(t)*40) / 40 }

// runInk 给出「按游程取色」的函数：彩虹配色按散列，色环配色按半径。
// 两者都不生效时返回 nil，渲染就走统一前景色。
func runInk(colorMode string) inkAt {
	switch {
	case ghostActive(colorMode):
		// 隐形配色下所有墨点同色，不参与彩色分色
		return nil
	case randomActive(colorMode):
		return func(row, col1, col2 int) color.RGBA {
			return randomInk(inkSeed(row, col1, col2))
		}
	case gradientActive(colorMode):
		return func(row, col1, col2 int) color.RGBA {
			r := (float64(col1+col2)/2 + 10.5) * float64(wxSize)
			return rainbowRingInk(quantT(inkTForRadius(r)))
		}
	}
	return nil
}

// ringInk 给出定位环的取色函数（环在半径 250 那一圈上）。
func ringInk(colorMode string) func(i int) color.RGBA {
	switch {
	case ghostActive(colorMode):
		return nil
	case randomActive(colorMode):
		return func(i int) color.RGBA { return randomInk(inkSeed(97, i, 3)) }
	case gradientActive(colorMode):
		return func(i int) color.RGBA { return rainbowRingInk(quantT(inkTForRadius(finderR))) }
	}
	return nil
}

// passInk 给一条墨线笔画（手绘 / 笔画风）取色：色环按它所在的半径，
// 彩虹按笔画序号散列。
func passInk(colorMode string, i int, pass inkPass) color.RGBA {
	if len(pass.Pts) == 0 {
		return color.RGBA{}
	}
	switch {
	case randomActive(colorMode):
		p := pass.Pts[0]
		return randomInk(inkSeed(i, int(p.X*4), int(p.Y*4)))
	case gradientActive(colorMode):
		mid := pass.Pts[len(pass.Pts)/2]
		return rainbowRingInk(quantT(inkTForRadius(math.Hypot(mid.X-wxCenter, mid.Y-wxCenter))))
	}
	return color.RGBA{}
}

// finderR 是定位环圆心到画布中心的距离。
const finderR = 250.0

// drawRings 画三组定位图案（实心圆 + 挖空 + 中心点）。
func drawRings(r *raster, toDev devFn, unit float64, ink func(i int) color.RGBA) {
	for i, c := range finderCenters {
		if ink != nil {
			takeInk(r, ink(i))
		} else {
			r.cur = 0
		}
		cx, cy := toDev(c[0], c[1])
		r.fillDisc(cx, cy, 2.5*float64(wxSize)*unit)
		r.clearDisc(cx, cy, 1.7*float64(wxSize)*unit)
		r.fillDisc(cx, cy, 0.5*float64(wxSize)*unit)
	}
}

// drawDotStyle 圆点：每格一颗圆珠。珠径 10.3，比格距 11.1 略小，珠间留一条缝。
func drawDotStyle(r *raster, g [72][16]byte, toDev devFn, unit float64, ink inkAt) {
	rad := 5.15 * unit
	for _, rn := range gridRuns(g) {
		for c := rn.Col1; c <= rn.Col2; c++ {
			x, y := cellPos(rn.Row, c)
			dx, dy := toDev(x, y)
			if ink != nil {
				takeInk(r, ink(rn.Row, c, c))
			} else {
				r.cur = 0
			}
			r.fillDisc(dx, dy, rad)
		}
	}
}

// drawBlockStyle 方块：每格一个沿极轴对齐的方块。
func drawBlockStyle(r *raster, g [72][16]byte, toDev devFn, unit float64, ink inkAt) {
	side := 10.3 * unit
	for _, rn := range gridRuns(g) {
		ang := float64(wxAngleStep) * float64(rn.Row)
		for c := rn.Col1; c <= rn.Col2; c++ {
			x, y := cellPos(rn.Row, c)
			dx, dy := toDev(x, y)
			if ink != nil {
				takeInk(r, ink(rn.Row, c, c))
			} else {
				r.cur = 0
			}
			r.fillBox(dx, dy, side, ang)
		}
	}
}

// drawLineStyle 线描：每格一个细环 + 中心点。
//
// 中心点不能省 —— 解码是采样格心的，环中间空着就等于没画。
// 输出很小时（比如 200px）点会缩到半个像素，这里给它一个最小直径兜底。
func drawLineStyle(r *raster, g [72][16]byte, toDev devFn, unit, ss float64, ink inkAt) {
	dotR := 1.55 * unit
	if minR := 0.95 * ss; dotR < minR {
		dotR = minR
	}
	for _, rn := range gridRuns(g) {
		for c := rn.Col1; c <= rn.Col2; c++ {
			x, y := cellPos(rn.Row, c)
			dx, dy := toDev(x, y)
			if ink != nil {
				takeInk(r, ink(rn.Row, c, c))
			} else {
				r.cur = 0
			}
			r.fillDisc(dx, dy, 5.2*unit)
			r.clearDisc(dx, dy, 2.85*unit)
			r.fillDisc(dx, dy, dotR)
		}
	}
}

// drawRunsInk 按经典胶囊画法画所有游程，但每一段按 ink 取色（色环 / 彩虹）。
func drawRunsInk(r *raster, g [72][16]byte, toDev devFn, unit float64, ink inkAt) {
	for _, rn := range gridRuns(g) {
		x1, y1 := cellPos(rn.Row, rn.Col1)
		x2, y2 := cellPos(rn.Row, rn.Col2)
		dx1, dy1 := toDev(x1, y1)
		dx2, dy2 := toDev(x2, y2)
		if ink != nil {
			takeInk(r, ink(rn.Row, rn.Col1, rn.Col2))
		} else {
			r.cur = 0
		}
		r.fillCapsule(dx1, dy1, dx2, dy2, wxStroke*unit)
	}
}
