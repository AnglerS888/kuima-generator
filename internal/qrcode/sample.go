package qrcode

import (
	"math"
)

// 径向采样：格点采样、格式环匹配、码字还原。

// sampleDisc 以 (x,y) 为圆心在半径 rad 内取暗像素比例。
func sampleDisc(img *BinImage, x, y, rad float64) float64 {
	xi0 := int(math.Round(x))
	yi0 := int(math.Round(y))
	r := int(math.Ceil(rad))
	n, d := 0, 0
	rr := rad*rad + 0.5
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if float64(dx*dx+dy*dy) > rr {
				continue
			}
			xi, yi := xi0+dx, yi0+dy
			if xi >= 0 && xi < img.W && yi >= 0 && yi < img.H {
				n++
				d += int(img.Bin[yi*img.W+xi])
			}
		}
	}
	if n == 0 {
		return 0
	}
	return float64(d) / float64(n)
}

type cellFn func(row, col int) byte

// minRingScore 是同步环的最低得分（满分 36）。
// 纯渲染图上正确几何通常是 34~36，但真实截图缩放后细线会被重采样糊掉，
// 实测一张 900px 的官方码最佳也只能到 29 —— 门槛定在 31 会直接把图判成"没有码"。
// 放宽到 26 只增加候选数量，最终仍由 RS 校验仲裁。
const minRingScore = 26

// ringScore 统计同步环（第 3 圈，36 个采样点）的得分。
func ringScore(cell cellFn) int {
	n := 0
	for r := 1; r < 72; r += 2 {
		n += int(cell(r, 3))
	}
	return n
}

// cornerScore 统计 4 个角标的得分（每个角标 2 格 × 4 个角）。
func cornerScore(cell cellFn) int {
	n := 0
	for _, r := range []int{9, 11, 27, 29, 45, 47, 63, 65} {
		n += int(cell(r, 4)) + int(cell(r, 5))
	}
	return n
}

// makeCell 构造把 (row,col) 映射到图像像素的采样函数。
func makeCell(img *BinImage, cx, cy, K, a, th float64) cellFn {
	c := math.Cos(th)
	s := math.Sin(th)
	sampleR := 1.2
	if K > 0 {
		v := 1.2 * K / 11.111111
		if v < 1.0 {
			v = 1.0
		}
		if v > 4.0 {
			v = 4.0
		}
		sampleR = v
	}
	return func(row, col int) byte {
		rr := (float64(col) + a) * K
		ang := float64(wxAngleStep) * float64(row)
		du := -math.Cos(ang) * rr
		dv := -math.Sin(ang) * rr
		x := cx + c*du - s*dv
		y := cy + s*du + c*dv
		if sampleDisc(img, x, y, sampleR) > 0.5 {
			return 1
		}
		return 0
	}
}

// bestFormat 用格式环 36 格匹配 512 项 typebits 表。
func bestFormat(cell cellFn) (matches int, idx int) {
	bits := make([]byte, 0, 36)
	for r := 1; r < 72; r += 2 {
		bits = append(bits, cell(r, 6))
	}
	bestM, bestIdx := -1, 0
	for i, tb := range typebitsTable {
		m := 0
		for j := 0; j < 36 && j < len(bits); j++ {
			if int(bits[j]) == (tb>>uint(17-(j%18)))&1 {
				m++
			}
		}
		if m > bestM {
			bestM = m
			bestIdx = i
		}
	}
	return bestM, bestIdx
}

// wxCodewords 从网格采样还原交织后的码字。
func wxCodewords(cell cellFn, version, mask int) []byte {
	var bits []byte
	for r := 0; r < 72; r++ {
		prow := placeTable[version][r]
		mrow := maskTable[mask][r]
		for c := 0; c < 16; c++ {
			if (prow>>uint(c))&1 != 0 {
				bits = append(bits, cell(r, c)^byte((mrow>>uint(c))&1))
			}
		}
	}
	cws := make([]byte, 0, len(bits)/8)
	for i := 0; i+7 < len(bits); i += 8 {
		var v byte
		for j := 0; j < 8; j++ {
			v = v<<1 | bits[i+j]
		}
		cws = append(cws, v)
	}
	return cws
}
