package qrcode

import "strings"

// 笔画风格：同一份码格，换一套「怎么画」。
//
// 风格只影响怎么画（像素/路径），不影响编码结果，所以换风格不会改变
// 版本、纠错等级、掩码；所有装饰性抖动都控制在半个格子内，格心始终落在墨迹里。
//
// 配色是另一个维度（见 color.go）：style 决定「形状」，color 决定「墨色」。
const (
	// StyleClassic 是默认风格：等宽胶囊笔画，与微信原生输出一致。
	StyleClassic = "classic"
	// StyleHand 是手绘风格：笔宽起伏、中线抖动、定位环手绘化。
	StyleHand = "hand"
	// StyleDot 是圆点风格：每格一颗圆珠。
	StyleDot = "dot"
	// StyleBlock 是方块风格：每格一个沿极轴对齐的方块，像素味。
	StyleBlock = "block"
	// StyleLine 是线描风格：每格一个细环 + 中心点，线稿感。
	StyleLine = "line"
)

// StyleNames 是所有可用风格（供文档/枚举提示使用）。
var StyleNames = []string{
	StyleClassic, StyleHand, StyleDot, StyleBlock, StyleLine,
}

// ParseStyle 把请求里的风格名归一化，未识别时回落到 classic。
func ParseStyle(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hand", "hand-drawn", "handdrawn", "handwrite", "handwriting",
		"sketch", "sketchy", "doodle", "ink", "brush", "手绘", "手写", "涂鸦":
		return StyleHand
	case "dot", "dots", "round", "circle", "bead", "圆点", "圆珠", "点阵":
		return StyleDot
	case "block", "blocks", "square", "squares", "pixel", "pixels", "方块", "像素":
		return StyleBlock
	case "line", "lines", "outline", "hollow", "lineart", "线描", "线稿", "空心":
		return StyleLine
	default:
		return StyleClassic
	}
}

// LegacyRandomStyle 报告这个值是不是「以前当风格用的彩虹」。
//
// 彩虹是墨色的事（每条笔画随机取色），已经挪到 color 参数；
// 这里留一个判断，老链接（style=rainbow）仍然按彩虹配色出图。
func LegacyRandomStyle(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "rainbow", "random", "confetti", "colorful", "rainbow-random", "彩虹", "随机色":
		return true
	}
	return false
}
