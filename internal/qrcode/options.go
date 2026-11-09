package qrcode

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"wxcode/internal/watermark"
)

// 对外选项与参数解析（HTTP 层与命令行共用）。

// LevelNames 下标 0..3 = L/M/Q/H。
const LevelNames = "LMQH"

// levelBits 采用 ZXing 的 ErrorCorrectionLevel.bits：L=1, M=0, Q=3, H=2。
var levelBits = [4]int{1, 0, 3, 2}

// levelFromBits 是 levelBits 的反查表。
var levelFromBits = map[int]int{1: 0, 0: 1, 3: 2, 2: 3}

// EncodeConfig 控制版本、纠错等级与掩码。
type EncodeConfig struct {
	Version  int // -1 = 自动（v0..v2 选最小），0..2 = 钉死
	MinLevel int // 允许的最低纠错等级 0=L 1=M 2=Q 3=H
	Mask     int // -1 = 自动
}

// DefaultEncodeConfig 返回与仓库 app/ 一致的默认参数（format=18, level=L, 自动版本）。
func DefaultEncodeConfig() EncodeConfig {
	return EncodeConfig{Version: -1, MinLevel: 0, Mask: -1}
}

// SVGOptions 控制 SVG 里的两张图片。
type SVGOptions struct {
	// BadgeHref 是角落小图标：""/"default" 用原生默认 URL，"none"/"off" 则不输出该 <image>。
	BadgeHref string
	// CenterHref 是中心 logo（data URI 或 URL），非空则追加圆形裁剪的 <image>。
	CenterHref string
	// Watermark 非空时在 SVG 末尾追加 <text> 水印（由浏览器用系统字体渲染）。
	Watermark *watermark.Config
}

// RenderOptions 控制 PNG 输出。
type RenderOptions struct {
	Size      int         // 图像边长（不含白边），默认 600
	Margin    int         // 四周留白像素，默认 0
	Fg        color.RGBA  // 前景色（码点），默认纯黑
	Bg        color.RGBA  // 背景色，默认纯白
	Logo      image.Image // 中心 logo，可为 nil
	LogoRatio float64     // logo 半径相对默认 108/600 的缩放，默认 1
	Badge     image.Image // 345° 楔形区的小图标，可为 nil

	// LogoFrames/LogoDelays 仅在输出 GIF 时使用：动图 logo 的逐帧画面与延时（1/100 秒）。
	// 为空时按静态 Logo 处理。
	LogoFrames []image.Image
	LogoDelays []int

	// Watermark 非空时在最终图像上叠加文字水印（PNG/JPEG/GIF 位图与 SVG 均支持）。
	Watermark *watermark.Config
}

// ============================================================
// 参数解析：把 HTTP/命令行里的字符串转成结构化选项
// ============================================================

// ParseLevel 把 "L"/"M"/"Q"/"H" 或 "0".."3" 转成等级下标，无法识别时返回 0（L）。
func ParseLevel(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "M", "1":
		return 1
	case "Q", "2":
		return 2
	case "H", "3":
		return 3
	default:
		return 0
	}
}

// ParseVersion 把 "auto"/"" 转成 -1，"0".."2" 转成对应版本。
func ParseVersion(s string) int {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "0", "1", "2":
		v, _ := strconv.Atoi(strings.TrimSpace(s))
		return v
	default:
		return -1
	}
}

// ParseColor 解析 "#rrggbb" / "rrggbb" / "#rgb"，失败时返回 def。
func ParseColor(s string, def color.RGBA) color.RGBA {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if s == "" {
		return def
	}
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return def
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return def
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}
