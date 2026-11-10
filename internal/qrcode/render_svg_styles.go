package qrcode

import (
	"fmt"
	"image/color"
	"math"
	"strings"
)

// SVG 端的风格画法。尺寸与位图端一一对应，两种输出看起来才是一套东西。

// svgHex 把颜色转成 #rrggbb。
func svgHex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// cellFill 返回某一格的填充色；ink 为 nil 时用黑色（与原生输出一致）。
func cellFill(ink inkAt, row, col1, col2 int) string {
	if ink == nil {
		return "black"
	}
	return svgHex(ink(row, col1, col2))
}

// cellPainter 画一格：给出格心坐标、该行的旋转角与该格的颜色。
type cellPainter func(b *strings.Builder, x, y, ang float64, fill string)

// writeSVGCells 逐格画（圆点 / 方块 / 线描共用）。
func writeSVGCells(b *strings.Builder, g [72][16]byte, ink inkAt, paint cellPainter) {
	for _, rn := range gridRuns(g) {
		ang := float64(wxAngleStep) * float64(rn.Row)
		for c := rn.Col1; c <= rn.Col2; c++ {
			x, y := cellPos(rn.Row, c)
			paint(b, x, y, ang, cellFill(ink, rn.Row, c, c))
		}
	}
}

// svgCircle 圆点：直径 10.3，比格距略小。
func svgCircle(b *strings.Builder, x, y, _ float64, fill string) {
	fmt.Fprintf(b, "<circle cx=\"%s\" cy=\"%s\" r=\"5.15\" fill=\"%s\"/>\n", fmt6(x), fmt6(y), fill)
}

// svgBox 方块：边长 10.3，跟着行角转，排成一圈才整齐。
func svgBox(b *strings.Builder, x, y, ang float64, fill string) {
	deg := fmt6(ang * 180 / math.Pi)
	fmt.Fprintf(b, "<rect x=\"%s\" y=\"%s\" width=\"10.3\" height=\"10.3\" fill=\"%s\" "+
		"transform=\"translate(%s %s) rotate(%s)\"/>\n",
		fmt6(-5.15), fmt6(-5.15), fill, fmt6(x), fmt6(y), deg)
}

// svgHollow 线描：细环 + 中心点。中心点不能省 —— 解码是采样格心的。
func svgHollow(b *strings.Builder, x, y, _ float64, fill string) {
	fmt.Fprintf(b, "<circle cx=\"%s\" cy=\"%s\" r=\"5.2\" fill=\"none\" stroke=\"%s\" stroke-width=\"2.35\"/>\n",
		fmt6(x), fmt6(y), fill)
	fmt.Fprintf(b, "<circle cx=\"%s\" cy=\"%s\" r=\"1.55\" fill=\"%s\"/>\n", fmt6(x), fmt6(y), fill)
}

// writeSVGRings 画三组定位图案；rings 为 nil 时用黑色。
func writeSVGRings(b *strings.Builder, rings func(i int) color.RGBA) {
	for i, c := range finderCenters {
		main := "black"
		if rings != nil {
			main = svgHex(rings(i))
		}
		for _, ring := range []struct {
			k     float64
			color string
		}{{2.5, main}, {1.7, "white"}, {0.5, main}} {
			fmt.Fprintf(b, "<circle cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"%s\"/>\n",
				fmt6(c[0]), fmt6(c[1]), fmt6(ring.k*float64(wxSize)), ring.color)
		}
	}
}

// writeSVGRunsInk 用经典胶囊形状画游程，但每一段单独上色（色环 / 彩虹）。
func writeSVGRunsInk(b *strings.Builder, g [72][16]byte, ink inkAt) {
	for _, rn := range gridRuns(g) {
		x1, y1 := cellPos(rn.Row, rn.Col1)
		x2, y2 := cellPos(rn.Row, rn.Col2)
		fill := cellFill(ink, rn.Row, rn.Col1, rn.Col2)
		fmt.Fprintf(b, "<circle cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"%s\"/>\n", fmt6(x1), fmt6(y1), fmt6(wxCap), fill)
		fmt.Fprintf(b, "<line x1=\"%s\" y1=\"%s\" x2=\"%s\" y2=\"%s\" style=\"stroke:%s; stroke-width:%s\" "+
			"stroke-linecap=\"round\"/>\n", fmt6(x1), fmt6(y1), fmt6(x2), fmt6(y2), fill, fmt6(wxStroke))
		fmt.Fprintf(b, "<circle cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"%s\"/>\n", fmt6(x2), fmt6(y2), fmt6(wxCap), fill)
	}
}
