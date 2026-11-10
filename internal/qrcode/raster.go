package qrcode

import (
	"image/color"
	"math"
)

// 位图光栅化图元：圆、圆头线段、方块与超采样二值化。

type raster struct {
	w, h int
	mask []byte // 1 = 暗色
	// 彩色风格（色环 / 彩虹）才用：每个像素属于哪一笔的颜色。
	// pal 为 nil 时 idx 不分配，渲染走原来的"统一前景色"路径。
	pal *inkPal
	idx []byte
	cur uint8 // 当前这一笔的调色板下标
}

// inkPal 是彩色风格用的小调色板，下标 0 固定是前景色。
type inkPal struct{ cols []color.RGBA }

func newInkPal(fg color.RGBA) *inkPal { return &inkPal{cols: []color.RGBA{fg}} }

// index 返回颜色在调色板里的下标，没有就追加。
func (p *inkPal) index(c color.RGBA) uint8 {
	for i, v := range p.cols {
		if v == c {
			return uint8(i)
		}
	}
	if len(p.cols) >= 255 {
		return 0
	}
	p.cols = append(p.cols, c)
	return uint8(len(p.cols) - 1)
}

// mark 把一个像素标成暗色，并记下它属于哪一笔的颜色。
func (r *raster) mark(i int) {
	r.mask[i] = 1
	if r.idx != nil {
		r.idx[i] = r.cur
	}
}

func (r *raster) fillDisc(cx, cy, rad float64) {
	if rad <= 0 {
		return
	}
	x0 := int(math.Floor(cx - rad))
	x1 := int(math.Ceil(cx + rad))
	y0 := int(math.Floor(cy - rad))
	y1 := int(math.Ceil(cy + rad))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > r.w-1 {
		x1 = r.w - 1
	}
	if y1 > r.h-1 {
		y1 = r.h - 1
	}
	rr := rad * rad
	for y := y0; y <= y1; y++ {
		dy := float64(y) + 0.5 - cy
		row := y * r.w
		for x := x0; x <= x1; x++ {
			dx := float64(x) + 0.5 - cx
			if dx*dx+dy*dy <= rr {
				r.mark(row + x)
			}
		}
	}
}

func (r *raster) clearDisc(cx, cy, rad float64) {
	if rad <= 0 {
		return
	}
	x0 := int(math.Floor(cx - rad))
	x1 := int(math.Ceil(cx + rad))
	y0 := int(math.Floor(cy - rad))
	y1 := int(math.Ceil(cy + rad))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > r.w-1 {
		x1 = r.w - 1
	}
	if y1 > r.h-1 {
		y1 = r.h - 1
	}
	rr := rad * rad
	for y := y0; y <= y1; y++ {
		dy := float64(y) + 0.5 - cy
		row := y * r.w
		for x := x0; x <= x1; x++ {
			dx := float64(x) + 0.5 - cx
			if dx*dx+dy*dy <= rr {
				r.mask[row+x] = 0
			}
		}
	}
}

// fillCapsule 画一条圆头线段（对应 SVG 的 line + 两端 circle）。
func (r *raster) fillCapsule(x1, y1, x2, y2, width float64) {
	rad := width / 2
	if rad <= 0 {
		return
	}
	r.fillDisc(x1, y1, rad)
	r.fillDisc(x2, y2, rad)
	dx := x2 - x1
	dy := y2 - y1
	len2 := dx*dx + dy*dy
	if len2 == 0 {
		return
	}
	minx := math.Min(x1, x2) - rad
	maxx := math.Max(x1, x2) + rad
	miny := math.Min(y1, y2) - rad
	maxy := math.Max(y1, y2) + rad
	x0 := int(math.Floor(minx))
	xe := int(math.Ceil(maxx))
	y0 := int(math.Floor(miny))
	ye := int(math.Ceil(maxy))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if xe > r.w-1 {
		xe = r.w - 1
	}
	if ye > r.h-1 {
		ye = r.h - 1
	}
	rr := rad * rad
	for y := y0; y <= ye; y++ {
		py := float64(y) + 0.5
		row := y * r.w
		for x := x0; x <= xe; x++ {
			px := float64(x) + 0.5
			t := ((px-x1)*dx + (py-y1)*dy) / len2
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			qx := x1 + t*dx
			qy := y1 + t*dy
			ddx := px - qx
			ddy := py - qy
			if ddx*ddx+ddy*ddy <= rr {
				r.mark(row + x)
			}
		}
	}
}

// inkStroke 沿点列铺一条笔宽可变的笔画（相邻两点用一个胶囊连接）。
//
// 点的 W 是这一点的笔宽，连接处取两端均值，于是笔画能自然地从粗到细收笔。
func (r *raster) inkStroke(pts []inkPt) {
	switch len(pts) {
	case 0:
		return
	case 1:
		r.fillDisc(pts[0].X, pts[0].Y, pts[0].W/2)
		return
	}
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		r.fillCapsule(a.X, a.Y, b.X, b.Y, (a.W+b.W)/2)
	}
}

// fillBox 画一个以 (cx,cy) 为中心、边长 side、绕中心旋转 ang 弧度的方块。
// 方块风格用：格子本身沿极轴排布，方块跟着转过来才排得整齐。
func (r *raster) fillBox(cx, cy, side, ang float64) {
	h := side / 2
	if h <= 0 {
		return
	}
	c, s := math.Cos(ang), math.Sin(ang)
	rng := h * (math.Abs(c) + math.Abs(s))
	x0 := int(math.Floor(cx - rng))
	x1 := int(math.Ceil(cx + rng))
	y0 := int(math.Floor(cy - rng))
	y1 := int(math.Ceil(cy + rng))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > r.w-1 {
		x1 = r.w - 1
	}
	if y1 > r.h-1 {
		y1 = r.h - 1
	}
	for y := y0; y <= y1; y++ {
		dy := float64(y) + 0.5 - cy
		row := y * r.w
		for x := x0; x <= x1; x++ {
			dx := float64(x) + 0.5 - cx
			if math.Abs(dx*c+dy*s) <= h && math.Abs(-dx*s+dy*c) <= h {
				r.mark(row + x)
			}
		}
	}
}

// Rasterize 把网格光栅化成二值位图（1 = 暗），600 坐标系，带超采样抗锯齿。
func Rasterize(g [72][16]byte) (mask []byte, size int) {
	const ss = 2
	W := 600 * ss
	r := &raster{w: W, h: W, mask: make([]byte, W*W)}
	for _, run := range gridRuns(g) {
		x1, y1 := cellPos(run.Row, run.Col1)
		x2, y2 := cellPos(run.Row, run.Col2)
		r.fillCapsule(x1*ss, y1*ss, x2*ss, y2*ss, wxStroke*ss)
	}
	for _, c := range finderCenters {
		r.fillDisc(c[0]*ss, c[1]*ss, 2.5*float64(wxSize)*ss)
		r.clearDisc(c[0]*ss, c[1]*ss, 1.7*float64(wxSize)*ss)
		r.fillDisc(c[0]*ss, c[1]*ss, 0.5*float64(wxSize)*ss)
	}
	out := make([]byte, 600*600)
	for y := 0; y < 600; y++ {
		for x := 0; x < 600; x++ {
			s := 0
			for dy := 0; dy < ss; dy++ {
				for dx := 0; dx < ss; dx++ {
					s += int(r.mask[(y*ss+dy)*W+(x*ss+dx)])
				}
			}
			if s*2 >= ss*ss {
				out[y*600+x] = 1
			}
		}
	}
	return out, 600
}
