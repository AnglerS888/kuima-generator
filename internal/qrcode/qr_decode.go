package qrcode

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"strings"
)

// QR 码解码：从图片里读出二维码文本。
//
// 微信「加好友」二维码就是一个普通 QR，内容是 https://u.wechat.com/xxxx 这类链接，
// 所以这里实现一条精简但按规范来的读取链路：
// 二值化 → 找 3 个定位图案 → 估版本 → 采样 → 格式信息 → 去掩码 → 解交织 → RS 纠错 → 解析比特流。
// 支持版本 1..40、L/M/Q/H、数字 / 字母数字 / 字节模式。

// QRResult 是一次 QR 解码的结果。
type QRResult struct {
	Text    string `json:"text"`
	URL     string `json:"url,omitempty"`
	Version int    `json:"version"`
	ECLevel string `json:"ecLevel"`
	Mask    int    `json:"mask"`
	Mode    string `json:"mode"`
	Modules int    `json:"modules"`
	// Corrections 是为纠正错误而修掉的码字数（0 表示原图很干净）
	Corrections int `json:"corrections"`
}

var errQRNoFinder = errors.New("没找到二维码的定位图案")

var errQRGeometry = errors.New("二维码尺寸估算失败")

var errQRFormat = errors.New("二维码格式信息损坏")

var errQRRS = errors.New("二维码未通过 Reed-Solomon 校验")

var errQRBitstream = errors.New("二维码比特流解析失败")

type qrPoint struct{ x, y float64 }

func (p qrPoint) dist(q qrPoint) float64 { return math.Hypot(p.x-q.x, p.y-q.y) }

// ============================================================
// 定位：扫 1:1:3:1:1 找三个定位图案
// ============================================================

type qrFinder struct {
	center qrPoint
	module float64
	hits   int
}

func isFinderRatio(a, b, c, d, e int) (module float64, ok bool) {
	total := a + b + c + d + e
	if total < 7 {
		return 0, false
	}
	m := float64(total) / 7.0
	tol := m * 0.7
	if math.Abs(float64(a)-m) > tol || math.Abs(float64(b)-m) > tol ||
		math.Abs(float64(c)-3*m) > 3*tol || math.Abs(float64(d)-m) > tol ||
		math.Abs(float64(e)-m) > tol {
		return 0, false
	}
	return m, true
}

// scanRowFinders 在每一行里找 1:1:3:1:1 的横向片段。
func scanRowFinders(bi *BinImage) []qrFinder {
	var out []qrFinder
	row := make([]byte, 0, bi.W)
	for y := 0; y < bi.H; y++ {
		row = bi.Bin[y*bi.W : (y+1)*bi.W]
		x := 0
		for x < bi.W {
			for x < bi.W && row[x] == 0 {
				x++
			}
			if x >= bi.W {
				break
			}
			start := x
			var lens [5]int
			idx := 0
			px := x
			for idx < 5 && px < bi.W {
				v := row[px]
				run := 0
				for px < bi.W && row[px] == v {
					px++
					run++
				}
				lens[idx] = run
				idx++
			}
			if idx < 5 {
				break
			}
			m, ok := isFinderRatio(lens[0], lens[1], lens[2], lens[3], lens[4])
			if !ok {
				x = start + 1
				continue
			}
			cx := float64(start + lens[0] + lens[1] + lens[2]/2)
			if cy, my, ok2 := verticalCenter(bi, int(cx), y, m); ok2 {
				out = append(out, qrFinder{
					center: qrPoint{cx, cy},
					module: (m + my) / 2,
					hits:   1,
				})
			}
			x = px
		}
	}
	return out
}

// verticalCenter 在给定列上找长度约 3 个模块的暗游程，作为定位图案的纵向中心。
func verticalCenter(bi *BinImage, x, yHint int, module float64) (float64, float64, bool) {
	if x < 0 || x >= bi.W {
		return 0, 0, false
	}
	best, bestDist := -1.0, math.MaxFloat64
	bestMod := 0.0
	y := 0
	for y < bi.H {
		for y < bi.H && bi.Bin[y*bi.W+x] == 0 {
			y++
		}
		if y >= bi.H {
			break
		}
		start := y
		for y < bi.H && bi.Bin[y*bi.W+x] == 1 {
			y++
		}
		runLen := y - start
		// 中心 3 模块的暗块
		if math.Abs(float64(runLen)-3*module) <= 1.8*module {
			c := float64(start) + float64(runLen)/2
			d := math.Abs(c - float64(yHint))
			if d <= 2.2*module && d < bestDist {
				best, bestDist, bestMod = c, d, float64(runLen)/3
			}
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestMod, true
}

// clusterFinders 合并重复命中，返回按命中次数排序的候选。
func clusterFinders(list []qrFinder) []qrFinder {
	var out []qrFinder
	for _, f := range list {
		merged := false
		for i := range out {
			if out[i].center.dist(f.center) <= math.Max(out[i].module, f.module)*1.5 {
				n := float64(out[i].hits)
				out[i].center.x = (out[i].center.x*n + f.center.x) / (n + 1)
				out[i].center.y = (out[i].center.y*n + f.center.y) / (n + 1)
				out[i].module = (out[i].module*n + f.module) / (n + 1)
				out[i].hits++
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, f)
		}
	}
	return out
}

// locateQR 找到三个定位图案，返回左上 / 右上 / 左下 中心与模块尺寸。
func locateQR(bi *BinImage) (tl, tr, bl qrPoint, module float64, err error) {
	cands := clusterFinders(scanRowFinders(bi))
	if len(cands) < 3 {
		return tl, tr, bl, 0, errQRNoFinder
	}
	// 命中次数最多的前 12 个里找直角等腰三角形
	if len(cands) > 12 {
		sortFinders(cands)
		cands = cands[:12]
	}
	bestScore := math.MaxFloat64
	found := false
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			for k := j + 1; k < len(cands); k++ {
				p := [3]qrPoint{cands[i].center, cands[j].center, cands[k].center}
				d := [3]float64{p[0].dist(p[1]), p[0].dist(p[2]), p[1].dist(p[2])}
				// 找出斜边（最长），另两条应近似相等
				hypI := 0
				for t := 1; t < 3; t++ {
					if d[t] > d[hypI] {
						hypI = t
					}
				}
				leg1, leg2 := d[(hypI+1)%3], d[(hypI+2)%3]
				if leg1 < 20 {
					continue
				}
				ratio := leg1 / leg2
				hypRatio := math.Sqrt(leg1*leg1+leg2*leg2) / d[hypI]
				if ratio < 0.8 || ratio > 1.25 || hypRatio < 0.97 || hypRatio > 1.03 {
					continue
				}
				// 直角顶点 = 斜边对面的点：边 0→顶点 2，边 1→顶点 1，边 2→顶点 0
				opp := 2 - hypI
				a := p[opp]
				others := [2]qrPoint{}
				n := 0
				for t := 0; t < 3; t++ {
					if t != opp {
						others[n] = p[t]
						n++
					}
				}
				// 用叉积区分右上 / 左下（图像坐标 y 向下）
				cross := (others[0].x-a.x)*(others[1].y-a.y) - (others[0].y-a.y)*(others[1].x-a.x)
				var right, bottom qrPoint
				if cross > 0 {
					right, bottom = others[0], others[1]
				} else {
					right, bottom = others[1], others[0]
				}
				score := math.Abs(ratio-1) + math.Abs(hypRatio-1)
				if score < bestScore {
					bestScore = score
					tl, tr, bl = a, right, bottom
					module = (cands[i].module + cands[j].module + cands[k].module) / 3
					found = true
				}
			}
		}
	}
	if !found {
		return tl, tr, bl, 0, errQRNoFinder
	}
	return tl, tr, bl, module, nil
}

func sortFinders(c []qrFinder) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].hits > c[j-1].hits; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

// ============================================================
// 采样
// ============================================================

// sampleQR 用三个定位图案建仿射网格，采样出 dim×dim 的模块矩阵。
func sampleQR(bi *BinImage, tl, tr, bl qrPoint, dim int) [][]bool {
	ex := qrPoint{(tr.x - tl.x) / float64(dim-7), (tr.y - tl.y) / float64(dim-7)}
	ey := qrPoint{(bl.x - tl.x) / float64(dim-7), (bl.y - tl.y) / float64(dim-7)}
	m := make([][]bool, dim)
	for r := 0; r < dim; r++ {
		m[r] = make([]bool, dim)
		for c := 0; c < dim; c++ {
			// 定位图案中心在模块坐标 (3.5,3.5)，模块 (c,r) 中心在 (c+0.5, r+0.5)
			fx := float64(c) - 3.0
			fy := float64(r) - 3.0
			px := tl.x + fx*ex.x + fy*ey.x
			py := tl.y + fx*ex.y + fy*ey.y
			m[r][c] = qrSample(bi, px, py)
		}
	}
	return m
}

// qrSample 在 (x,y) 附近取 3×3 小窗的多数值，抗抗锯齿。
func qrSample(bi *BinImage, x, y float64) bool {
	ix, iy := int(math.Round(x)), int(math.Round(y))
	n, dark := 0, 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			px, py := ix+dx, iy+dy
			if px < 0 || py < 0 || px >= bi.W || py >= bi.H {
				continue
			}
			n++
			if bi.Bin[py*bi.W+px] != 0 {
				dark++
			}
		}
	}
	if n == 0 {
		return false
	}
	return dark*2 > n
}

// ============================================================
// 格式信息 / 版本
// ============================================================

// bch15_5 计算格式信息的 10 位校验。
func bch15_5(data int) int {
	d := data << 10
	for i := 14; i >= 10; i-- {
		if (d>>uint(i))&1 != 0 {
			d ^= 0x537 << uint(i-10)
		}
	}
	return (data << 10) | d
}

var qrValidFormat = func() [32]int {
	var out [32]int
	for d := 0; d < 32; d++ {
		out[d] = bch15_5(d) ^ 0x5412
	}
	return out
}()

func popcount15(v int) int {
	n := 0
	for i := 0; i < 15; i++ {
		n += (v >> uint(i)) & 1
	}
	return n
}

// readFormat 读两处格式信息，返回纠错等级下标（0=L,1=M,2=Q,3=H）与掩码。
func readFormat(m [][]bool, dim int) (ecIdx, mask int, err error) {
	at := func(x, y int) int {
		if m[y][x] {
			return 1
		}
		return 0
	}
	b1, b2 := 0, 0
	for i := 0; i <= 5; i++ {
		b1 |= at(8, i) << uint(i)
	}
	b1 |= at(8, 7) << 6
	b1 |= at(8, 8) << 7
	b1 |= at(7, 8) << 8
	for i := 9; i < 15; i++ {
		b1 |= at(14-i, 8) << uint(i)
	}
	for i := 0; i < 8; i++ {
		b2 |= at(dim-1-i, 8) << uint(i)
	}
	for i := 8; i < 15; i++ {
		b2 |= at(8, dim-15+i) << uint(i)
	}

	bestDist, bestData := 99, -1
	for _, v := range []int{b1, b2} {
		for d, code := range qrValidFormat {
			dist := popcount15(v ^ code)
			if dist < bestDist {
				bestDist, bestData = dist, d
			}
		}
	}
	if bestData < 0 || bestDist > 3 {
		return 0, 0, errQRFormat
	}
	ecBits := bestData >> 3
	mask = bestData & 7
	switch ecBits {
	case 0b01:
		ecIdx = 0 // L
	case 0b00:
		ecIdx = 1 // M
	case 0b11:
		ecIdx = 2 // Q
	default:
		ecIdx = 3 // H
	}
	return ecIdx, mask, nil
}

// ============================================================
// 功能图案与数据读取
// ============================================================

// qrFunctionMap 标记哪些模块是功能图案（定位、时序、校正、格式、版本）。
func qrFunctionMap(v int, dim int) [][]bool {
	f := make([][]bool, dim)
	for i := range f {
		f[i] = make([]bool, dim)
	}
	set := func(x, y int) {
		if x >= 0 && y >= 0 && x < dim && y < dim {
			f[y][x] = true
		}
	}
	// 三个定位图案及分隔符
	for _, c := range [][2]int{{0, 0}, {dim - 7, 0}, {0, dim - 7}} {
		for dy := -1; dy <= 7; dy++ {
			for dx := -1; dx <= 7; dx++ {
				set(c[0]+dx, c[1]+dy)
			}
		}
	}
	// 时序
	for i := 0; i < dim; i++ {
		set(6, i)
		set(i, 6)
	}
	// 校正图案
	al := qrVersions[v].alignment
	for _, ay := range al {
		for _, ax := range al {
			if ax == 6 && ay == 6 || ax == 6 && ay == dim-7 || ax == dim-7 && ay == 6 {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					set(ax+dx, ay+dy)
				}
			}
		}
	}
	// 格式信息
	for i := 0; i <= 8; i++ {
		set(8, i)
		set(i, 8)
	}
	for i := 0; i < 8; i++ {
		set(dim-1-i, 8)
		set(8, dim-1-i)
	}
	set(8, dim-8) // 固定暗模块
	// 版本信息（v7+）
	if v >= 7 {
		for i := 0; i < 6; i++ {
			for j := 0; j < 3; j++ {
				set(dim-11+j, i)
				set(i, dim-11+j)
			}
		}
	}
	return f
}

// qrMaskBit 返回该位置是否被掩码翻转。
func qrMaskBit(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return (r*c)%2+(r*c)%3 == 0
	case 6:
		return ((r*c)%2+(r*c)%3)%2 == 0
	default:
		return ((r+c)%2+(r*c)%3)%2 == 0
	}
}

// qrReadData 按之字形读出数据位（已去掩码）。
func qrReadData(m [][]bool, v, dim, mask int) []byte {
	fn := qrFunctionMap(v, dim)
	var bits []byte
	for right := dim - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < dim; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := ((right + 1) & 2) == 0
				y := vert
				if upward {
					y = dim - 1 - vert
				}
				if fn[y][x] {
					continue
				}
				bit := byte(0)
				if m[y][x] {
					bit = 1
				}
				if qrMaskBit(mask, y, x) {
					bit ^= 1
				}
				bits = append(bits, bit)
			}
		}
	}
	out := make([]byte, 0, len(bits)/8)
	for i := 0; i+7 < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			b = b<<1 | bits[i+j]
		}
		out = append(out, b)
	}
	return out
}

// qrDeinterleave 按版本/纠错结构拆块，逐块 RS 纠错后拼回数据码字。
func qrDeinterleave(codewords []byte, v, ecIdx int) ([]byte, int, error) {
	lv := qrVersions[v].levels[ecIdx]
	var blocks []qrECGroup
	for _, g := range lv.groups {
		for i := 0; i < g.count; i++ {
			blocks = append(blocks, qrECGroup{1, g.dataCW})
		}
	}
	if len(blocks) == 0 {
		return nil, 0, errQRRS
	}
	dataLens := make([]int, len(blocks))
	maxLen := 0
	for i, b := range blocks {
		dataLens[i] = b.dataCW
		if b.dataCW > maxLen {
			maxLen = b.dataCW
		}
	}
	data := make([][]byte, len(blocks))
	ec := make([][]byte, len(blocks))
	for i := range ec {
		ec[i] = make([]byte, lv.ecPerBlock)
	}
	pos := 0
	take := func() (byte, bool) {
		if pos >= len(codewords) {
			return 0, false
		}
		b := codewords[pos]
		pos++
		return b, true
	}
	for i := 0; i < maxLen; i++ {
		for b := range blocks {
			if i < dataLens[b] {
				v, ok := take()
				if !ok {
					return nil, 0, errQRRS
				}
				data[b] = append(data[b], v)
			}
		}
	}
	for i := 0; i < lv.ecPerBlock; i++ {
		for b := range blocks {
			v, ok := take()
			if !ok {
				return nil, 0, errQRRS
			}
			ec[b][i] = v
		}
	}

	out := make([]byte, 0, pos)
	fixed := 0
	for b := range blocks {
		block := make([]byte, 0, dataLens[b]+lv.ecPerBlock)
		block = append(block, data[b]...)
		block = append(block, ec[b]...)
		if !allZero(syndromes(block, lv.ecPerBlock)) {
			res, err := rsCorrect(block, lv.ecPerBlock)
			if err != nil {
				return nil, 0, errQRRS
			}
			fixed += res.NFix
			block = res.CW
		}
		out = append(out, block[:dataLens[b]]...)
	}
	return out, fixed, nil
}

// ============================================================
// 比特流解析
// ============================================================

// qrParse 解析数据码字里的模式段，返回文本与模式名。
func qrParse(data []byte, v int) (string, string, error) {
	bits := make([]byte, 0, len(data)*8)
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (b>>uint(i))&1)
		}
	}
	pos := 0
	take := func(n int) (int, bool) {
		if pos+n > len(bits) {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			v = v<<1 | int(bits[pos])
			pos++
		}
		return v, true
	}
	var (
		sb       strings.Builder
		modes    []string
		countBit = func(kind int) int {
			// kind: 0=numeric 1=alnum 2=byte 3=kanji
			switch {
			case v <= 9:
				return []int{10, 9, 8, 8}[kind]
			case v <= 26:
				return []int{12, 11, 16, 10}[kind]
			default:
				return []int{14, 13, 16, 12}[kind]
			}
		}
	)

	for {
		if pos+4 > len(bits) {
			break
		}
		mode, _ := take(4)
		switch mode {
		case 0: // 终止符
			goto done
		case 1: // 数字
			cnt, ok := take(countBit(0))
			if !ok {
				return "", "", errQRBitstream
			}
			for i := 0; i < cnt; i += 3 {
				n := 3
				if cnt-i < 3 {
					n = cnt - i
				}
				width := []int{0, 4, 7, 10}[n]
				val, ok := take(width)
				if !ok {
					return "", "", errQRBitstream
				}
				fmt.Fprintf(&sb, "%0*d", n, val)
			}
			modes = append(modes, "NUMERIC")
		case 2: // 字母数字
			cnt, ok := take(countBit(1))
			if !ok {
				return "", "", errQRBitstream
			}
			for i := 0; i < cnt; i += 2 {
				if cnt-i >= 2 {
					val, ok := take(11)
					if !ok {
						return "", "", errQRBitstream
					}
					if val >= 45*45 {
						return "", "", errQRBitstream
					}
					sb.WriteByte(qrAlnum[val/45])
					sb.WriteByte(qrAlnum[val%45])
				} else {
					val, ok := take(6)
					if !ok {
						return "", "", errQRBitstream
					}
					if val >= len(qrAlnum) {
						return "", "", errQRBitstream
					}
					sb.WriteByte(qrAlnum[val])
				}
			}
			modes = append(modes, "ALPHANUMERIC")
		case 4: // 字节
			cnt, ok := take(countBit(2))
			if !ok {
				return "", "", errQRBitstream
			}
			raw := make([]byte, 0, cnt)
			for i := 0; i < cnt; i++ {
				val, ok := take(8)
				if !ok {
					return "", "", errQRBitstream
				}
				raw = append(raw, byte(val))
			}
			sb.Write(raw)
			modes = append(modes, "BYTE")
		case 7: // ECI
			if _, ok := take(8); !ok {
				return "", "", errQRBitstream
			}
		case 8: // 汉字（Shift-JIS）——微信码不会用到，明确不支持
			return "", "", fmt.Errorf("暂不支持汉字模式的二维码")
		default:
			// 结构化追加 / FNC1 等，跳到结束
			goto done
		}
	}
done:
	if sb.Len() == 0 {
		return "", "", errQRBitstream
	}
	mode := "BYTE"
	if len(modes) > 0 {
		mode = strings.Join(modes, "+")
	}
	return sb.String(), mode, nil
}

const qrAlnum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

// ============================================================
// 对外入口
// ============================================================

// DecodeQRImage 从图片里解出二维码文本。
func DecodeQRImage(img image.Image) (*QRResult, error) {
	return DecodeQRImageContext(context.Background(), img)
}

// DecodeQRImageContext 是 DecodeQRImage 的可取消版本。
func DecodeQRImageContext(ctx context.Context, img image.Image) (*QRResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bi := ToBinaryScaled(img, 1400)
	if bi.W < 21 || bi.H < 21 {
		return nil, errQRGeometry
	}
	tl, tr, bl, module, err := locateQR(bi)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 两臂长度换算出模块数，再吸附到 4v+17
	// 两个定位图案中心相距 dim-7 个模块，dim = 17+4v → v = (arm-10)/4
	armX := tl.dist(tr) / module
	armY := tl.dist(bl) / module
	arm := (armX + armY) / 2
	estVer := int(math.Round((arm - 10) / 4))
	if estVer < 1 {
		estVer = 1
	}
	if estVer > 40 {
		return nil, errQRGeometry
	}

	var lastErr error = errQRGeometry
	for _, dv := range []int{0, -1, 1, -2, 2} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v := estVer + dv
		if v < 1 || v > 40 {
			continue
		}
		dim := 17 + 4*v
		m := sampleQR(bi, tl, tr, bl, dim)
		ecIdx, mask, ferr := readFormat(m, dim)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		data, fixed, derr := qrDeinterleave(qrReadData(m, v, dim, mask), v, ecIdx)
		if derr != nil {
			lastErr = derr
			continue
		}
		text, mode, perr := qrParse(data, v)
		if perr != nil {
			lastErr = perr
			continue
		}
		res := &QRResult{
			Text:        text,
			Version:     v,
			ECLevel:     string("LMQH"[ecIdx]),
			Mask:        mask,
			Mode:        mode,
			Modules:     dim,
			Corrections: fixed,
		}
		if u := pickURL(text); u != "" {
			res.URL = u
		}
		return res, nil
	}
	return nil, lastErr
}

// pickURL 从解出的文本里挑出链接（微信好友码就是一条 https://u.wechat.com/... 链接）。
func pickURL(text string) string {
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			return f
		}
	}
	return ""
}
