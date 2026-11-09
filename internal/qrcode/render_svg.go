package qrcode

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"

	"wxcode/internal/watermark"
)

// SVG 渲染（坐标按 float32 逐步舍入，与原生库逐字节一致）。

// RenderSVG 输出与原生库逐字节一致的 SVG（默认徽标 URL 时）。
func RenderSVG(info *EncodeInfo, opt SVGOptions) string {
	badge := opt.BadgeHref
	if badge == "" || badge == "default" {
		badge = defaultBadge
	}
	var b strings.Builder
	b.Grow(40000)
	b.WriteString("<svg width=\"600\" height=\"600\" version=\"1.1\"                     " +
		"xmlns=\"http://www.w3.org/2000/svg\"  xmlns:xlink=\"http://www.w3.org/1999/xlink\">\n")
	side := float32(float64(wxSize) + float64(wxSize)*10.0)
	if badge != "none" && badge != "off" {
		fmt.Fprintf(&b, "<image xlink:href=\"%s\"  x=\"419.593964\" y=\"419.593964\"  width=\"%s\" height=\"%s\"/>\n",
			html.EscapeString(badge), fmt6(float64(side)), fmt6(float64(side)))
	}
	for _, c := range finderCenters {
		for _, ring := range []struct {
			k     float64
			color string
		}{{2.5, "black"}, {1.7, "white"}, {0.5, "black"}} {
			fmt.Fprintf(&b, "<circle cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"%s\"/>\n",
				fmt6(c[0]), fmt6(c[1]), fmt6(ring.k*float64(wxSize)), ring.color)
		}
	}
	for _, run := range gridRuns(info.Grid) {
		x1, y1 := cellPos(run.Row, run.Col1)
		x2, y2 := cellPos(run.Row, run.Col2)
		fmt.Fprintf(&b, "<circle cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"black\"/>\n", fmt6(x1), fmt6(y1), fmt6(wxCap))
		fmt.Fprintf(&b, "<line x1=\"%s\" y1=\"%s\" x2=\"%s\" y2=\"%s\"                                 "+
			"style=\"stroke:rgb(0, 0, 0); stroke-width:%s\"/>\n", fmt6(x1), fmt6(y1), fmt6(x2), fmt6(y2), fmt6(wxStroke))
		fmt.Fprintf(&b, "<circle cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"black\"/>\n", fmt6(x2), fmt6(y2), fmt6(wxCap))
	}
	if opt.CenterHref != "" {
		b.WriteString("<clipPath id=\"wxcenter\"><circle cx=\"300\" cy=\"300\" r=\"130\"/></clipPath>\n")
		b.WriteString("<circle cx=\"300\" cy=\"300\" r=\"132\" fill=\"white\"/>\n")
		fmt.Fprintf(&b, "<image xlink:href=\"%s\" x=\"170\" y=\"170\" width=\"260\" height=\"260\" clip-path=\"url(#wxcenter)\"/>\n", html.EscapeString(opt.CenterHref))
	}
	writeSVGWatermark(&b, opt.Watermark)
	b.WriteString("</svg>\n")
	return b.String()
}

// writeSVGWatermark 在 SVG 末尾追加文字水印。
//
// SVG 的坐标系固定 600×600（与码本身一致），字号直接用用户配置的像素值；
// 文字交给浏览器用系统字体渲染，所以中文/emoji 都能正常显示。
func writeSVGWatermark(b *strings.Builder, c *watermark.Config) {
	if c == nil || strings.TrimSpace(c.Text) == "" {
		return
	}
	lines := strings.Split(strings.ReplaceAll(c.Text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return
	}
	size := float64(c.Size)
	lineH := size * 1.25
	pad := math.Max(4, size/2)
	fill := fmt.Sprintf("#%02x%02x%02x", c.Color.R, c.Color.G, c.Color.B)
	style := fmt.Sprintf(`font-family="Noto Sans SC,PingFang SC,Microsoft YaHei,sans-serif" font-size="%s" fill="%s" fill-opacity="%s"`,
		fmt6(size), fill, trimFloat(c.Opacity))

	blockH := lineH * float64(len(lines))
	tileText := html.EscapeString(strings.Join(lines, " "))

	switch c.Position {
	case watermark.PositionTile:
		stepX, stepY := math.Max(size*8, size*4), math.Max(size*4, size*2)
		for ty := size * 2; ty < 600; ty += stepY {
			for tx := size; tx < 600; tx += stepX {
				fmt.Fprintf(b, "<text x=\"%s\" y=\"%s\" %s text-anchor=\"start\">%s</text>\n",
					fmt6(tx), fmt6(ty), style, tileText)
			}
		}
		return
	case watermark.PositionTileDiagonal:
		stepX, stepY := math.Max(size*10, size*5), math.Max(size*6.5, size*4)
		for ty := -stepY; ty < 600+stepY; ty += stepY {
			for tx := -stepX; tx < 600+stepX; tx += stepX {
				fmt.Fprintf(b, "<text x=\"%s\" y=\"%s\" %s text-anchor=\"start\" transform=\"rotate(-25 %s %s)\">%s</text>\n",
					fmt6(tx), fmt6(ty), style, fmt6(tx), fmt6(ty), tileText)
			}
		}
		return
	}

	var x, y float64
	anchor := "middle"
	switch c.Position {
	case watermark.PositionTopLeft:
		x, y, anchor = pad, pad+size*0.8, "start"
	case watermark.PositionTop:
		x, y = 300, pad+size*0.8
	case watermark.PositionTopRight:
		x, y, anchor = 600-pad, pad+size*0.8, "end"
	case watermark.PositionBottomLeft:
		x, y, anchor = pad, 600-pad, "start"
	case watermark.PositionBottom:
		x, y = 300, 600-pad
	case watermark.PositionBottomRight:
		x, y, anchor = 600-pad, 600-pad, "end"
	default: // center：多行整体垂直居中
		x = 300
		y = 300 - blockH/2 + lineH*0.75
	}
	for i, ln := range lines {
		fmt.Fprintf(b, "<text x=\"%s\" y=\"%s\" %s text-anchor=\"%s\">%s</text>\n",
			fmt6(x), fmt6(y+float64(i)*lineH), style, anchor, html.EscapeString(ln))
	}
}

// trimFloat 把 0.35 这类值格式化成最短形式（0.35 / 0.3 / 1）。
func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}
