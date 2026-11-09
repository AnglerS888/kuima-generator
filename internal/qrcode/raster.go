package qrcode

import (
	"math"
)

// 位图光栅化图元：圆、圆头线段与超采样二值化。

type raster struct {
	w, h int
	mask []byte // 1 = 暗色
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
				r.mask[row+x] = 1
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
				r.mask[row+x] = 1
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
