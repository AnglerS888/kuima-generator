package qrcode

import (
	"image"
	"image/color"
	"image/draw"
)

// 二值图：灰度化、Otsu 阈值与缩放。

// BinImage 是二值图（1 = 暗）。解码链路的输入，由 ToBinary 生成。
type BinImage struct {
	Bin  []byte // 长度 W*H，行优先，1 表示暗像素
	W, H int
}

// ToBinary 把任意图片转成二值图。透明像素按白处理，阈值用 Otsu 全局自适应。
func ToBinary(img image.Image) *BinImage {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return &BinImage{W: 0, H: 0}
	}
	// 先铺白再叠加，顺带把 alpha 压平成 RGB
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Over)

	gray := make([]byte, w*h)
	for y := 0; y < h; y++ {
		row := rgba.Pix[y*rgba.Stride:]
		for x := 0; x < w; x++ {
			p := row[x*4:]
			gray[y*w+x] = byte((int(p[0])*299 + int(p[1])*587 + int(p[2])*114) / 1000)
		}
	}
	return binFromGray(gray, w, h)
}

// ToBinaryScaled 在二值化之前先按需降采样。
//
// 解码大图时，先对 4000 万像素的原图分配 RGBA + 灰度 + 二值图，峰值接近 240MB；
// 而后续本来就会降到 1200 再解。这里直接按目标尺寸做区域采样，只保留目标灰度图，
// 把第一阶段的内存压到 1 字节/目标像素。
func ToBinaryScaled(img image.Image, maxSide int) *BinImage {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return &BinImage{W: 0, H: 0}
	}
	if maxSide <= 0 || max(w, h) <= maxSide {
		return ToBinary(img)
	}
	f := float64(maxSide) / float64(max(w, h))
	nw := max(1, int(float64(w)*f))
	nh := max(1, int(float64(h)*f))
	gray := make([]byte, nw*nh)
	lum := luminanceFunc(img)
	for y := 0; y < nh; y++ {
		y0 := y * h / nh
		y1 := (y + 1) * h / nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		// 每个目标像素最多取 4×4 个源像素做平均，既能保住细线，又不会因
		// 极端缩放大小时遍历整块源区域。
		stepY := max(1, (y1-y0+3)/4)
		for x := 0; x < nw; x++ {
			x0 := x * w / nw
			x1 := (x + 1) * w / nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			stepX := max(1, (x1-x0+3)/4)
			var sum, n int
			for sy := y0; sy < y1; sy += stepY {
				for sx := x0; sx < x1; sx += stepX {
					sum += int(lum(b.Min.X+sx, b.Min.Y+sy))
					n++
				}
			}
			if n == 0 {
				continue
			}
			gray[y*nw+x] = byte(sum / n)
		}
	}
	return binFromGray(gray, nw, nh)
}

// luminanceFunc 为常见图片类型提供直接读 Pix 的快路径。
// 大图采样会调用几百万到上千万次，image.At + color.Convert 的接口开销很可观。
func luminanceFunc(img image.Image) func(x, y int) uint8 {
	switch im := img.(type) {
	case *image.Gray:
		return func(x, y int) uint8 { return im.GrayAt(x, y).Y }
	case *image.NRGBA:
		return func(x, y int) uint8 {
			i := im.PixOffset(x, y)
			a := int(im.Pix[i+3])
			r, g, b := int(im.Pix[i]), int(im.Pix[i+1]), int(im.Pix[i+2])
			if a < 255 {
				r = (r*a + 255*(255-a)) / 255
				g = (g*a + 255*(255-a)) / 255
				b = (b*a + 255*(255-a)) / 255
			}
			return uint8((r*299 + g*587 + b*114) / 1000)
		}
	case *image.RGBA:
		return func(x, y int) uint8 {
			i := im.PixOffset(x, y)
			a := int(im.Pix[i+3])
			// RGBA 的 RGB 是预乘值，叠在白底上直接补上未乘的白色部分。
			r, g, b := int(im.Pix[i])+255-a, int(im.Pix[i+1])+255-a, int(im.Pix[i+2])+255-a
			return uint8((r*299 + g*587 + b*114) / 1000)
		}
	case *image.YCbCr:
		return func(x, y int) uint8 {
			yi := im.YOffset(x, y)
			ci := im.COffset(x, y)
			r, g, b := color.YCbCrToRGB(im.Y[yi], im.Cb[ci], im.Cr[ci])
			return uint8((int(r)*299 + int(g)*587 + int(b)*114) / 1000)
		}
	default:
		return func(x, y int) uint8 {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			r, g, b := int(c.R), int(c.G), int(c.B)
			if c.A < 255 {
				a := int(c.A)
				r = (r*a + 255*(255-a)) / 255
				g = (g*a + 255*(255-a)) / 255
				b = (b*a + 255*(255-a)) / 255
			}
			return uint8((r*299 + g*587 + b*114) / 1000)
		}
	}
}

// binFromGray 用 Otsu 阈值把灰度图转成二值图。
func binFromGray(gray []byte, w, h int) *BinImage {
	thr := otsu(gray)
	bin := make([]byte, len(gray))
	for i, g := range gray {
		if int(g) <= thr {
			bin[i] = 1
		}
	}
	return &BinImage{Bin: bin, W: w, H: h}
}

// otsu 求类间方差最大的全局阈值。
func otsu(gray []byte) int {
	var hist [256]int
	for _, g := range gray {
		hist[g]++
	}
	total := len(gray)
	sumAll := 0
	for i := 0; i < 256; i++ {
		sumAll += i * hist[i]
	}
	wB, sumB := 0, 0
	best := -1.0
	thr := 128
	for t := 0; t < 256; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF := total - wB
		if wF == 0 {
			break
		}
		sumB += t * hist[t]
		mB := float64(sumB) / float64(wB)
		mF := float64(sumAll-sumB) / float64(wF)
		v := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if v > best {
			best = v
			thr = t
		}
	}
	return thr
}

// Downscale 用盒式平均把图缩小到 maxSide 以内（大图解码前先降采样，控制耗时）。
func Downscale(img *BinImage, maxSide int) *BinImage {
	if img.W <= maxSide && img.H <= maxSide {
		return img
	}
	f := float64(maxSide) / float64(max(img.W, img.H))
	nw := max(1, int(float64(img.W)*f))
	nh := max(1, int(float64(img.H)*f))
	out := make([]byte, nw*nh)
	for y := 0; y < nh; y++ {
		y0 := y * img.H / nh
		y1 := (y + 1) * img.H / nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0 := x * img.W / nw
			x1 := (x + 1) * img.W / nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			n, sum := 0, 0
			for yy := y0; yy < y1; yy++ {
				row := yy * img.W
				for xx := x0; xx < x1; xx++ {
					n++
					sum += int(img.Bin[row+xx])
				}
			}
			if sum*2 >= n { // 多数为暗
				out[y*nw+x] = 1
			}
		}
	}
	return &BinImage{Bin: out, W: nw, H: nh}
}
