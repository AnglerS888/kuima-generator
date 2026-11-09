package qrcode

import (
	"math"
	"sort"
)

// 三点定位：连通域找 3 组定位图案，推出中心、尺度与参考角。

type finderCand struct {
	score, cx, cy, rad float64
}

// findFinderPose 通过连通域找 3 组定位图案，返回中心、尺度与参考旋转角。
func findFinderPose(img *BinImage) (cx, cy, s, th0 float64, ok bool) {
	w, h := img.W, img.H
	seen := make([]bool, w*h)
	var cands []finderCand
	stack := make([]int, 0, 1024)
	pts := make([]int, 0, 1024)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if img.Bin[idx] == 0 || seen[idx] {
				continue
			}
			seen[idx] = true
			stack = stack[:0]
			stack = append(stack, idx)
			pts = pts[:0]
			minx, maxx := x, x
			miny, maxy := y, y
			sx, sy := 0, 0
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				pts = append(pts, p)
				px, py := p%w, p/w
				sx += px
				sy += py
				if px < minx {
					minx = px
				}
				if px > maxx {
					maxx = px
				}
				if py < miny {
					miny = py
				}
				if py > maxy {
					maxy = py
				}
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := px+dx, py+dy
						if nx < 0 || nx >= w || ny < 0 || ny >= h {
							continue
						}
						ni := ny*w + nx
						if img.Bin[ni] != 0 && !seen[ni] {
							seen[ni] = true
							stack = append(stack, ni)
						}
					}
				}
			}
			bw := maxx - minx + 1
			bh := maxy - miny + 1
			if bw < 12 || bh < 12 || math.Abs(float64(bw-bh)) > 0.18*float64(max(bw, bh)) {
				continue
			}
			ccx := float64(sx) / float64(len(pts))
			ccy := float64(sy) / float64(len(pts))
			rad := float64(max(bw, bh)) / 2
			if float64(len(pts))/float64(bw*bh) > 0.72 {
				continue
			}
			frac := func(lo, hi float64, want byte) float64 {
				n, hit := 0, 0
				for i := 0; i < 16; i++ {
					a := 2 * math.Pi * float64(i) / 16
					for _, rr := range []float64{lo, hi} {
						xi := int(math.Round(ccx + rr*rad*math.Cos(a)))
						yi := int(math.Round(ccy + rr*rad*math.Sin(a)))
						if xi >= 0 && xi < w && yi >= 0 && yi < h {
							n++
							if img.Bin[yi*w+xi] == want {
								hit++
							}
						}
					}
				}
				if n == 0 {
					return 0
				}
				return float64(hit) / float64(n)
			}
			sc := frac(0.0, 0.12, 1) + frac(0.42, 0.52, 0) + frac(0.74, 0.88, 1)
			if sc > 2.55 {
				cands = append(cands, finderCand{sc, ccx, ccy, rad})
			}
		}
	}
	if len(cands) < 3 {
		return 0, 0, 0, 0, false
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > 16 {
		cands = cands[:16]
	}
	type triple struct {
		score float64
		pts   [3][2]float64
	}
	var best *triple
	dist := func(a, b [2]float64) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			for k := j + 1; k < len(cands); k++ {
				pts3 := [3][2]float64{
					{cands[i].cx, cands[i].cy},
					{cands[j].cx, cands[j].cy},
					{cands[k].cx, cands[k].cy},
				}
				d := []float64{dist(pts3[0], pts3[1]), dist(pts3[0], pts3[2]), dist(pts3[1], pts3[2])}
				sort.Float64s(d)
				leg, hyp := d[0], d[2]
				if leg < 20 || math.Abs(d[0]-d[1]) > 0.12*leg || math.Abs(hyp/leg-math.Sqrt2) > 0.12 {
					continue
				}
				rs := []float64{cands[i].rad, cands[j].rad, cands[k].rad}
				mx, mn := rs[0], rs[0]
				for _, v := range rs {
					if v > mx {
						mx = v
					}
					if v < mn {
						mn = v
					}
				}
				if mx-mn > 0.25*mx {
					continue
				}
				sc := cands[i].score + cands[j].score + cands[k].score
				if best == nil || sc > best.score {
					best = &triple{score: sc, pts: pts3}
				}
			}
		}
	}
	if best == nil {
		return 0, 0, 0, 0, false
	}
	pts3 := best.pts
	dd := []struct {
		i, j int
		d    float64
	}{
		{1, 2, dist(pts3[1], pts3[2])},
		{0, 2, dist(pts3[0], pts3[2])},
		{0, 1, dist(pts3[0], pts3[1])},
	}
	sort.SliceStable(dd, func(a, b int) bool { return dd[a].d > dd[b].d })
	far := dd[0]
	opp := 3 - far.i - far.j
	A := pts3[opp]
	var rest [][2]float64
	for m := 0; m < 3; m++ {
		if m != opp {
			rest = append(rest, pts3[m])
		}
	}
	var B, C [2]float64
	if rest[0][1] > rest[1][1] {
		B, C = rest[0], rest[1]
	} else {
		B, C = rest[1], rest[0]
	}
	ccx := A[0] + ((B[0]-A[0])+(C[0]-A[0]))/2
	ccy := A[1] + ((B[1]-A[1])+(C[1]-A[1]))/2
	sc := dist(A, C) / 353.5533905932737
	// A→C 的方向应落在图像 +x 轴上；由此得到参考旋转角（用于缩小搜索范围）
	th0 = math.Atan2(C[1]-A[1], C[0]-A[0])
	return ccx, ccy, sc, th0, true
}
