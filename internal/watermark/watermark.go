package watermark

import (
	"errors"
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"
)

// 水印位置。
const (
	PositionCenter       = "center"        // 居中
	PositionTop          = "top"           // 顶部居中
	PositionBottom       = "bottom"        // 底部居中
	PositionTopLeft      = "top-left"      // 左上角
	PositionTopRight     = "top-right"     // 右上角
	PositionBottomLeft   = "bottom-left"   // 左下角
	PositionBottomRight  = "bottom-right"  // 右下角
	PositionTile         = "tile"          // 重复平铺
	PositionTileDiagonal = "tile-diagonal" // 重复斜铺（-25° 倾斜）
)

// 取值范围。API 层与后台表单都用这组常量做校验，避免两边写两份。
const (
	MinSize      = 8   // 最小字号（像素）
	MaxSize      = 240 // 最大字号（像素）
	MaxTextRunes = 64  // 水印文字最大字符数
	MinOpacity   = 0   // 最小透明度（百分比）
	MaxOpacity   = 100 // 最大透明度（百分比）
)

// Config 是一次渲染使用的水印配置（已归一化：Opacity 为 0~1）。
type Config struct {
	Text     string
	Size     int
	Color    color.RGBA
	Opacity  float64 // 0~1（接口传 0~100 的百分比，进来前换算）
	Position string
}

// Positions 返回支持的位置取值（后台下拉框用）。
func Positions() []string {
	return []string{
		PositionTopLeft, PositionTop, PositionTopRight,
		PositionCenter,
		PositionBottomLeft, PositionBottom, PositionBottomRight,
		PositionTile, PositionTileDiagonal,
	}
}

// PositionLabels 是位置的中文名，前端下拉框与文档共用。
var PositionLabels = map[string]string{
	PositionTopLeft:      "左上角",
	PositionTop:          "顶部居中",
	PositionTopRight:     "右上角",
	PositionCenter:       "居中",
	PositionBottomLeft:   "左下角",
	PositionBottom:       "底部居中",
	PositionBottomRight:  "右下角",
	PositionTile:         "重复平铺",
	PositionTileDiagonal: "重复斜铺",
}

// NormalizePosition 归一化位置：返回规范值 + 是否可识别。
//
// 兼容几种历史/别名写法（bottomcenter、topleft、repeat、diagonal 等），
// 未知值返回 ("", false)，由调用方决定是回落还是报错。
func NormalizePosition(s string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.NewReplacer("_", "-", " ", "-").Replace(v)
	switch v {
	case PositionTopLeft, "left-top", "topleft", "lt":
		return PositionTopLeft, true
	case PositionTop, "top-center", "topcenter", "tc":
		return PositionTop, true
	case PositionTopRight, "right-top", "topright", "rt":
		return PositionTopRight, true
	case PositionCenter, "middle", "centre":
		return PositionCenter, true
	case PositionBottomLeft, "left-bottom", "bottomleft", "lb":
		return PositionBottomLeft, true
	case PositionBottom, "bottom-center", "bottomcenter", "bc":
		return PositionBottom, true
	case PositionBottomRight, "right-bottom", "bottomright", "br", "right":
		return PositionBottomRight, true
	case PositionTile, "repeat", "tiled":
		return PositionTile, true
	case PositionTileDiagonal, "tile-diag", "diagonal", "diag", "repeat-diagonal":
		return PositionTileDiagonal, true
	}
	return "", false
}

// ParsePosition 归一化位置：空值或未知值回落到居中。
func ParsePosition(s string) string {
	if v, ok := NormalizePosition(s); ok {
		return v
	}
	return PositionCenter
}

// Validate 校验配置；返回的错误是中文，可直接给接口调用方看。
func (c Config) Validate() error {
	text := strings.TrimSpace(c.Text)
	if text == "" {
		return errors.New("水印文字不能为空")
	}
	if utf8.RuneCountInString(text) > MaxTextRunes {
		return fmt.Errorf("水印文字最多 %d 个字符", MaxTextRunes)
	}
	if c.Size < MinSize || c.Size > MaxSize {
		return fmt.Errorf("水印字号需在 %d~%d 像素之间", MinSize, MaxSize)
	}
	if c.Opacity < 0 || c.Opacity > 1 {
		return errors.New("水印透明度需在 0~100 之间")
	}
	switch c.Position {
	case PositionCenter, PositionTop, PositionBottom,
		PositionTopLeft, PositionTopRight, PositionBottomLeft, PositionBottomRight,
		PositionTile, PositionTileDiagonal:
	default:
		return errors.New("水印位置只能是 center / top / bottom / top-left / top-right / bottom-left / bottom-right / tile / tile-diagonal")
	}
	if bad := Unsupported(text); len(bad) > 0 {
		// 只提示前 3 个，避免长文本把错误信息撑爆
		shown := bad
		if len(shown) > 3 {
			shown = shown[:3]
		}
		parts := make([]string, 0, len(shown))
		for _, r := range shown {
			parts = append(parts, string(r))
		}
		return fmt.Errorf("水印文字包含暂不支持的字符：%s（内置字形为常用汉字 + 英文数字符号）",
			strings.Join(parts, " "))
	}
	return nil
}

// colorHex 输出 #RRGGBB，便于日志与接口回显。
func colorHex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}
