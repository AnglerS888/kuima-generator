package qrcode

import (
	"image/color"
	"math"
	"strings"
)

// 配色模式：和笔画风格正交的第二个维度 —— 风格决定「形状」，配色决定「墨色」。
const (
	// ColorSolid 是默认配色：用请求里的前景色（fg）画所有墨点。
	ColorSolid = "solid"
	// ColorRainbow 是色环配色：按半径取色（内圈青 → 外圈品红），一圈一个颜色。
	ColorRainbow = "rainbow"
	// ColorGhost 是隐形配色：墨色只比底色深几个色阶，肉眼几乎看不出，
	// 但亮度差还在，软件（以及凑近的手机）仍能解出来。
	ColorGhost = "ghost"
	// ColorRandom 是彩虹配色：每一笔（一个圆点 / 一条线段）随机取一个颜色。
	ColorRandom = "random"
)

// ColorNames 是所有可用配色（供文档/枚举提示使用）。
var ColorNames = []string{ColorSolid, ColorRainbow, ColorRandom, ColorGhost}

// GhostDefault 是隐形配色的默认深度（色阶，0~40）。越小越隐形、越难扫。
const GhostDefault = 6

// ParseColorMode 把请求里的配色名归一化，未识别时回落到 solid。
func ParseColorMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "rainbow", "gradient", "grad", "hue", "color-ring", "ring", "色环", "渐变":
		return ColorRainbow
	case "random", "rainbow-random", "confetti", "colorful", "candy",
		"彩虹", "随机色", "彩色":
		return ColorRandom
	case "ghost", "hidden", "invisible", "faint", "stealth", "watermark",
		"隐形", "隐蔽", "隐藏", "透明":
		return ColorGhost
	default:
		return ColorSolid
	}
}

// gradientActive 报告是否按位置（半径）取色 —— 色环配色。
func gradientActive(mode string) bool { return mode == ColorRainbow }

// randomActive 报告是否每笔画随机取色 —— 彩虹配色。
func randomActive(mode string) bool { return mode == ColorRandom }

// colorModeActive 报告是否要按位置临时算墨色（色环）。
func colorModeActive(mode string) bool { return gradientActive(mode) }

// ghostActive 报告是否是隐形配色。
func ghostActive(mode string) bool { return mode == ColorGhost }

// ghostInk 返回隐形配色的墨色：背景色三个通道同时减 level，色相不变、只压亮度。
// level 越大越看得见，也越好扫。
func ghostInk(bg color.RGBA, level int) color.RGBA {
	if level < 1 {
		level = GhostDefault
	}
	sub := func(v uint8) uint8 {
		if int(v) <= level {
			return 0
		}
		return uint8(int(v) - level)
	}
	return color.RGBA{sub(bg.R), sub(bg.G), sub(bg.B), 255}
}

// rainbowRingInk 是色环配色：按半径取色。色相只走青 → 蓝紫 → 品红这一段，
// 避开黄、绿这些亮度高的色相 —— 它们在白底上太浅，缩放后就扫不出来了。
func rainbowRingInk(t float64) color.RGBA {
	return hslToRGBA(196+142*clamp01(t), 0.74, 0.42)
}

// inkTForRadius 把「到圆心的像素距离（600 坐标系）」映射成 0..1 的配色进度。
func inkTForRadius(r float64) float64 {
	rIn := 10.5 * float64(wxSize)
	rOut := 25.5 * float64(wxSize)
	if rOut <= rIn {
		return 0
	}
	return clamp01((r - rIn) / (rOut - rIn))
}

// randomInks 是彩虹配色的取色盘：每条笔画随机一色。
//
// 关键约束：所有颜色的「亮度」必须一致（这里固定 luma=0.34）。解码端是拿整张图
// 做全局阈值二值化的，只要有一档颜色偏亮，它的抗锯齿边缘就会从"黑"翻到"白"，
// 格子被读错 —— 实测早期色盘里的橙 #f26522 在 1200px 上就会让 RS 校验失败。
// 所以这里按"目标亮度 + 变色相"来生成：饱和度拉满，亮度对齐。
var randomInks = buildRandomInks()

func buildRandomInks() []color.RGBA {
	hues := []float64{340, 355, 15, 30, 200, 220, 245, 270, 295, 315}
	out := make([]color.RGBA, len(hues))
	for i, h := range hues {
		out[i] = inkAtLuma(h, 0.34)
	}
	return out
}

// luma 是感知亮度（跟解码端灰度化用的系数一致：0.299R+0.587G+0.114B）。
func luma(c color.RGBA) float64 {
	return (0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)) / 255
}

// inkAtLuma 按目标亮度生成一个饱和度尽量高的墨色：
// 先取同色相的高饱和色，再等比压暗到指定亮度。
func inkAtLuma(hue, target float64) color.RGBA {
	base := hslToRGBA(hue, 0.9, 0.5)
	lo, hi := 0.0, 1.0
	for i := 0; i < 20; i++ {
		k := (lo + hi) / 2
		c := color.RGBA{
			R: uint8(math.Round(float64(base.R) * k)),
			G: uint8(math.Round(float64(base.G) * k)),
			B: uint8(math.Round(float64(base.B) * k)),
			A: 255,
		}
		if luma(c) > target {
			hi = k
		} else {
			lo = k
		}
	}
	return color.RGBA{
		R: uint8(math.Round(float64(base.R) * lo)),
		G: uint8(math.Round(float64(base.G) * lo)),
		B: uint8(math.Round(float64(base.B) * lo)),
		A: 255,
	}
}

// randomInk 按笔画坐标散列取色：同一个码永远同一批颜色。
func randomInk(seed uint32) color.RGBA {
	return randomInks[int(seed%uint32(len(randomInks)))]
}

// inkSeed 由 (行, 起始列, 结束列) 散列出一个稳定种子。
func inkSeed(row, col1, col2 int) uint32 {
	s := uint32(row)*2654435761 ^ uint32(col1)*40503 ^ uint32(col2)*2246822519
	s ^= s >> 15
	s *= 2246822519
	s ^= s >> 13
	return s
}

// hslToRGBA 把 HSL（h∈度，s/l∈0..1）转成 RGBA。
func hslToRGBA(h, s, l float64) color.RGBA {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	if s <= 0 {
		v := uint8(math.Round(clamp01(l) * 255))
		return color.RGBA{v, v, v, 255}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hue := func(t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	f := func(v float64) uint8 { return uint8(math.Round(clamp01(v) * 255)) }
	return color.RGBA{f(hue(h + 1.0/3)), f(hue(h)), f(hue(h - 1.0/3)), 255}
}

// clamp01 把值夹到 0..1。
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
