package qrcode

import (
	"fmt"
	"math"
)

// 极坐标几何：画布常量、格点到画布坐标、行内游程提取。
const wxCanvas = 600.0

const wxCenter = 300.0

const defaultBadge = "http://mmbiz.qpic.cn/mmbiz_png/o6DN7TFEdDfS7Zyq5sAbFjX11rKzq5LvCSrekE94aoAhAFssFYc6WYibdbCS20iaufJRaoL2EdcsMpRINUzfRMrA/0"

var wxSize = float32(float64(float32(600)) / float64(float32(27)) * 0.5)

var wxStroke = float64(wxSize) * 0.975 // 10.833333

var wxCap = wxStroke * 0.5 // 5.416666

var wxAngleStep = float32(0.0872664675116539)

// finderCenters 是 3 组定位图案的中心（半径 250 = (12+10.5)·S，行 9/27/63）。
var finderCenters = [][2]float64{
	{123.223312, 123.223312},
	{123.223312, 476.776672},
	{476.776672, 123.223312},
}

// cellPos 把 (row,col) 换算成画布坐标，逐步按 float32 舍入以对齐原生输出。
func cellPos(row, col int) (float64, float64) {
	radius := float32((float64(col) + 10.0 + 0.5) * float64(wxSize))
	angle := float32(float64(wxAngleStep) * float64(float32(row)))
	s := float32(math.Sin(float64(angle)))
	c := float32(math.Cos(float64(angle)))
	// 注意：c*radius 必须先在 float32 里乘再加，才能与原生 f32(c*radius) 逐位一致
	x := float32(wxCenter - float64(c*radius))
	y := float32(wxCenter - float64(s*radius))
	return float64(x), float64(y)
}

func fmt6(v float64) string { return fmt.Sprintf("%.6f", v) }

// svgRun 表示一行内连续为 1 的一段游程（含起止行列）。
type svgRun struct {
	Row, Col1, Col2 int
}

// gridRuns 按行优先顺序取出所有连续为 1 的游程（不跨行）。
func gridRuns(g [72][16]byte) []svgRun {
	var runs []svgRun
	for r := 0; r < 72; r++ {
		c := 0
		for c < 16 {
			if g[r][c] != 0 {
				start := c
				for c+1 < 16 && g[r][c+1] != 0 {
					c++
				}
				runs = append(runs, svgRun{Row: r, Col1: start, Col2: c})
			}
			c++
		}
	}
	return runs
}
