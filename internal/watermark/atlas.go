// Package watermark 在生成的码图上叠加文字水印。
//
// 为了不引入第三方字体依赖、也不往仓库里塞几 MB 的字体文件，这里内置了一份
// 32px 的位图字形表（ASCII + GB2312 一级汉字 + 常用中文标点，共 4122 字），
// 运行时按需缩放绘制。字形来自 Noto Sans SC（SIL Open Font License 1.1），
// 仅提取了位图轮廓，见同目录 NOTICE.md。
package watermark

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
)

//go:embed atlas.bin.gz
var atlasGz []byte

// 源字形参数（与生成器保持一致）。
const (
	sourceEm      = 32 // 生成字形时使用的字号
	atlasMagic    = "WMK1"
	atlasHeaderSZ = 4 + 2 + 2 + 2 + 4
)

type glyph struct {
	offset int
	width  int // 步进宽度（像素，源尺寸下）
}

var (
	atlasW   int
	atlasH   int
	atlasAsc int
	atlasRow int // 每行字节数
	glyphs   map[rune]glyph
	bitmaps  []byte
)

func init() {
	if err := loadAtlas(); err != nil {
		panic("水印字形表初始化失败: " + err.Error())
	}
}

func loadAtlas() error {
	zr, err := gzip.NewReader(bytes.NewReader(atlasGz))
	if err != nil {
		return err
	}
	defer zr.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(zr); err != nil {
		return err
	}
	b := raw.Bytes()
	if len(b) < atlasHeaderSZ || string(b[:4]) != atlasMagic {
		return errors.New("字形表格式不正确")
	}
	atlasW = int(binary.LittleEndian.Uint16(b[4:]))
	atlasH = int(binary.LittleEndian.Uint16(b[6:]))
	atlasAsc = int(binary.LittleEndian.Uint16(b[8:]))
	count := int(binary.LittleEndian.Uint32(b[10:]))
	atlasRow = (atlasW + 7) / 8

	idx := atlasHeaderSZ
	need := idx + count*5
	if len(b) < need {
		return errors.New("字形表索引被截断")
	}
	glyphs = make(map[rune]glyph, count)
	for i := 0; i < count; i++ {
		p := idx + i*5
		r := rune(binary.LittleEndian.Uint32(b[p:]))
		w := int(b[p+4])
		glyphs[r] = glyph{offset: i * atlasRow * atlasH, width: w}
	}
	bitmaps = b[need:]
	want := count * atlasRow * atlasH
	if len(bitmaps) < want {
		return errors.New("字形表位图被截断")
	}
	return nil
}

// Supports 报告字符是否有字形。换行与空格总是支持。
func Supports(r rune) bool {
	if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
		return true
	}
	_, ok := glyphs[r]
	return ok
}

// Unsupported 返回文本里没有字形的字符（去重，保持出现顺序）。
func Unsupported(text string) []rune {
	seen := map[rune]bool{}
	var out []rune
	for _, r := range text {
		if Supports(r) || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// Draw 把水印画到 dst 上（就地修改）。
//
// 所有参数已由调用方校验过；这里只做几何布局与像素混合，
// 源字形是 1bpp，按目标字号做面积采样，缩小时不会出现锯齿断裂。
func Draw(dst *image.RGBA, c Config) {
	if dst == nil || c.Text == "" || c.Opacity <= 0 || c.Size <= 0 {
		return
	}
	scale := float64(c.Size) / sourceEm
	lines := splitLines(c.Text)
	if len(lines) == 0 {
		return
	}
	lineH := float64(atlasH) * scale
	blockW := 0.0
	widths := make([]float64, len(lines))
	for i, ln := range lines {
		w := measure(ln) * scale
		widths[i] = w
		if w > blockW {
			blockW = w
		}
	}
	blockH := lineH * float64(len(lines))

	b := dst.Bounds()
	W, H := float64(b.Dx()), float64(b.Dy())
	pad := math.Max(4, float64(c.Size)/2)

	var startX, startY float64
	align := "center" // 多行时的水平对齐：left / center / right
	switch c.Position {
	case PositionTopLeft:
		startX, startY, align = pad, pad, "left"
	case PositionTop:
		startX, startY = (W-blockW)/2, pad
	case PositionTopRight:
		startX, startY, align = W-blockW-pad, pad, "right"
	case PositionBottomLeft:
		startX, startY, align = pad, H-blockH-pad, "left"
	case PositionBottomRight:
		startX, startY, align = W-blockW-pad, H-blockH-pad, "right"
	case PositionBottom:
		startX, startY = (W-blockW)/2, H-blockH-pad
	case PositionTile:
		drawTile(dst, c, lines, widths, blockW, blockH, scale)
		return
	case PositionTileDiagonal:
		drawTileDiagonal(dst, c, lines, widths, blockW, blockH, scale)
		return
	default: // center
		startX, startY = (W-blockW)/2, (H-blockH)/2
	}
	startX = math.Round(startX)
	startY = math.Round(startY)

	col := c.Color
	for i, ln := range lines {
		var x float64
		switch align {
		case "left":
			x = startX
		case "right":
			x = startX + blockW - widths[i]
		default:
			x = startX + (blockW-widths[i])/2 // 多行时逐行居中
		}
		y := startY + float64(i)*lineH
		drawLine(dst, ln, x, y, scale, col, c.Opacity)
	}
}

// drawTile 平铺：以「文字块 + 间距」为一个格子，铺满整张图。
func drawTile(dst *image.RGBA, c Config, lines []string, widths []float64, blockW, blockH, scale float64) {
	gapX := math.Max(blockW*0.8, float64(c.Size)*3)
	gapY := math.Max(blockH*0.8, float64(c.Size)*2.5)
	stepX := blockW + gapX
	stepY := blockH + gapY
	b := dst.Bounds()
	W, H := float64(b.Dx()), float64(b.Dy())
	// 从负半格开始，保证边缘也被覆盖，看起来是连续平铺
	for y := -stepY / 2; y < H; y += stepY {
		for x := -stepX / 2; x < W; x += stepX {
			for i, ln := range lines {
				drawLine(dst, ln, x+(blockW-widths[i])/2, y+float64(i)*(blockH/float64(len(lines))), scale, c.Color, c.Opacity)
			}
		}
	}
}

// drawLine 画一行文字。
func drawLine(dst *image.RGBA, line string, x0, y0, scale float64, col color.RGBA, opacity float64) {
	x := x0
	for _, r := range line {
		g, ok := glyphs[r]
		if !ok {
			if r == ' ' || r == '\t' {
				x += float64(sourceEm) * 0.5 * scale
			}
			continue
		}
		drawGlyph(dst, g, x, y0, scale, col, opacity)
		x += float64(g.width) * scale
	}
}

// drawGlyph 把一个字形按 scale 缩放后混入目标图。
func drawGlyph(dst *image.RGBA, g glyph, x0, y0, scale float64, col color.RGBA, opacity float64) {
	bounds := dst.Bounds()
	// 目标矩形（可能超出画布，逐像素裁剪）
	tw := int(math.Ceil(float64(atlasW) * scale))
	th := int(math.Ceil(float64(atlasH) * scale))
	left := int(math.Floor(x0))
	top := int(math.Floor(y0))
	for ty := 0; ty < th; ty++ {
		py := top + ty
		if py < bounds.Min.Y || py >= bounds.Max.Y {
			continue
		}
		sy := float64(ty) / scale
		for tx := 0; tx < tw; tx++ {
			px := left + tx
			if px < bounds.Min.X || px >= bounds.Max.X {
				continue
			}
			sx := float64(tx) / scale
			cov := sampleBit(g, sx, sy, scale)
			if cov <= 0 {
				continue
			}
			a := opacity * cov
			if a <= 0 {
				continue
			}
			i := dst.PixOffset(px, py)
			dst.Pix[i+0] = blend(dst.Pix[i+0], col.R, a)
			dst.Pix[i+1] = blend(dst.Pix[i+1], col.G, a)
			dst.Pix[i+2] = blend(dst.Pix[i+2], col.B, a)
			dst.Pix[i+3] = 255
		}
	}
}

// sampleBit 在源字形坐标 (sx, sy) 处取覆盖率：
// 放大时取最近像素；缩小时对源像素做 2×2 超采样，避免笔画断裂。
func sampleBit(g glyph, sx, sy, scale float64) float64 {
	if sx < 0 || sy < 0 || sx >= float64(atlasW) || sy >= float64(atlasH) {
		return 0
	}
	bit := func(px, py int) float64 {
		if px < 0 || py < 0 || px >= atlasW || py >= atlasH {
			return 0
		}
		off := g.offset + py*atlasRow + px/8
		if off >= len(bitmaps) {
			return 0
		}
		if bitmaps[off]&(1<<(7-uint(px%8))) != 0 {
			return 1
		}
		return 0
	}
	if scale >= 1 {
		return bit(int(sx), int(sy))
	}
	// 缩小：源像素覆盖 [sx, sx+1/scale)，取 2×2 采样平均
	step := 0.5 / scale
	sum := bit(int(sx), int(sy)) +
		bit(int(sx+step), int(sy)) +
		bit(int(sx), int(sy+step)) +
		bit(int(sx+step), int(sy+step))
	return sum / 4
}

func measure(line string) float64 {
	w := 0.0
	for _, r := range line {
		if g, ok := glyphs[r]; ok {
			w += float64(g.width)
			continue
		}
		if r == ' ' || r == '\t' {
			w += float64(sourceEm) * 0.5
		}
	}
	return w
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" && len(out) > 0 {
			// 允许空行（保留排版），但末尾空行忽略
			out = append(out, ln)
			continue
		}
		out = append(out, ln)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func blend(dst, src uint8, a float64) uint8 {
	if a >= 1 {
		return src
	}
	v := float64(dst)*(1-a) + float64(src)*a
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return uint8(math.Round(v))
}

// SourceSize 返回内置字形的源字号（供文档/测试使用）。
func SourceSize() int { return sourceEm }

// GlyphCount 返回内置字形数量（供文档/测试使用）。
func GlyphCount() int { return len(glyphs) }

// String 便于日志里打印水印配置。
func (c Config) String() string {
	return fmt.Sprintf("watermark{text=%q size=%d color=%s opacity=%.2f position=%s}",
		c.Text, c.Size, colorHex(c.Color), c.Opacity, c.Position)
}

// drawTileDiagonal 斜向平铺：整块文字倾斜 -25° 后按网格重复。
func drawTileDiagonal(dst *image.RGBA, c Config, lines []string, widths []float64, blockW, blockH, scale float64) {
	const angle = -25 * math.Pi / 180
	lineH := float64(atlasH) * scale
	// 斜铺时文字之间要留更大间距，否则旋转后容易糊成一片
	gapX := math.Max(blockW*0.9, float64(c.Size)*3.2)
	gapY := math.Max(blockH*1.15, float64(c.Size)*3.0)
	stepX := blockW + gapX
	stepY := blockH + gapY
	b := dst.Bounds()
	W, H := float64(b.Dx()), float64(b.Dy())
	// 从负一格开始铺，保证四角也被覆盖
	for y := -stepY; y < H+stepY; y += stepY {
		for x := -stepX; x < W+stepX; x += stepX {
			bcx, bcy := x+blockW/2, y+blockH/2
			for i, ln := range lines {
				lx := x + (blockW-widths[i])/2
				ly := y + float64(i)*lineH
				drawLineRotated(dst, ln, lx, ly, bcx, bcy, scale, angle, c.Color, c.Opacity)
			}
		}
	}
}

// drawLineRotated 沿倾斜基线画一行文字：逐字按字符中心绕 (bcx,bcy) 旋转。
func drawLineRotated(dst *image.RGBA, line string, x0, y0, bcx, bcy, scale, angle float64, col color.RGBA, opacity float64) {
	x := x0
	cosA, sinA := math.Cos(angle), math.Sin(angle)
	gh := float64(atlasH) * scale
	for _, r := range line {
		g, ok := glyphs[r]
		if !ok {
			if r == ' ' || r == '\t' {
				x += float64(sourceEm) * 0.5 * scale
			}
			continue
		}
		gw := float64(g.width) * scale
		dx := (x + gw/2) - bcx
		dy := (y0 + gh/2) - bcy
		drawGlyphRotated(dst, g, bcx+dx*cosA-dy*sinA, bcy+dx*sinA+dy*cosA, scale, angle, col, opacity)
		x += gw
	}
}

// drawGlyphRotated 把一个字形绕自身中心旋转后混入目标图（逆向采样，避免锯齿空洞）。
func drawGlyphRotated(dst *image.RGBA, g glyph, cx, cy, scale, angle float64, col color.RGBA, opacity float64) {
	gw := float64(atlasW) * scale
	gh := float64(atlasH) * scale
	r := math.Hypot(gw, gh)/2 + 1
	bounds := dst.Bounds()
	minX, maxX := int(math.Floor(cx-r)), int(math.Ceil(cx+r))
	minY, maxY := int(math.Floor(cy-r)), int(math.Ceil(cy+r))
	cosA, sinA := math.Cos(angle), math.Sin(angle)
	for py := minY; py <= maxY; py++ {
		if py < bounds.Min.Y || py >= bounds.Max.Y {
			continue
		}
		for px := minX; px <= maxX; px++ {
			if px < bounds.Min.X || px >= bounds.Max.X {
				continue
			}
			dx := float64(px) + 0.5 - cx
			dy := float64(py) + 0.5 - cy
			// 反向旋转回字形本地坐标
			lx := dx*cosA + dy*sinA + gw/2
			ly := -dx*sinA + dy*cosA + gh/2
			if lx < 0 || ly < 0 || lx >= gw || ly >= gh {
				continue
			}
			cov := sampleBit(g, lx/scale, ly/scale, scale)
			if cov <= 0 {
				continue
			}
			a := opacity * cov
			if a <= 0 {
				continue
			}
			i := dst.PixOffset(px, py)
			dst.Pix[i+0] = blend(dst.Pix[i+0], col.R, a)
			dst.Pix[i+1] = blend(dst.Pix[i+1], col.G, a)
			dst.Pix[i+2] = blend(dst.Pix[i+2], col.B, a)
			dst.Pix[i+3] = 255
		}
	}
}
