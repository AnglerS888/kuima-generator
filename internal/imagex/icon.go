package imagex

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
)

// 站点图标：把上传的图片整理成统一尺寸的 PNG，存进 settings。

// iconMaxSide 是图标落库的边长上限。
// 256 足够覆盖 180px 的 iOS 主屏图标和浏览器页签，体积也就几十 KB。
const IconMaxSide = 256

// iconFromImage 把任意图片做成站点图标：居中裁成正方形 → 缩到 max×max → PNG。
//
// 统一裁方 + 转 PNG 是有必要的：管理员可能传 jpg、非正方形截图，浏览器
// 对 favicon 的格式和比例都比较挑剔，这里先收拾干净，运行时就不必再判断。
func IconFromImage(src image.Image, max int) ([]byte, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("图片为空")
	}
	if max <= 0 {
		max = IconMaxSide
	}

	side := min(w, h)
	ox := b.Min.X + (w-side)/2
	oy := b.Min.Y + (h-side)/2
	crop := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(crop, crop.Bounds(), src, image.Pt(ox, oy), draw.Src)

	n := max
	if side < max {
		n = side // 小图不放大，否则糊
	}
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		sy := y * side / n
		for x := 0; x < n; x++ {
			out.Set(x, y, crop.At(x*side/n, sy))
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
