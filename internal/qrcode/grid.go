package qrcode

// 模块排布：72×16 稀疏位图、格式环、32 种掩码与打分选取。

// baseGrid 建立固定模块（同步环第 3 列、四角定位核心）。
func baseGrid() [72][16]byte {
	var g [72][16]byte
	for r := 1; r < 72; r += 2 {
		g[r][3] = 1
	}
	for _, r := range []int{9, 11, 27, 29, 45, 47, 63, 65} {
		g[r][4] = 1
		g[r][5] = 1
	}
	return g
}

// buildGrid 把码字按位图铺进网格，套掩码，写格式环，最后复制第 15 列。
func buildGrid(codewords []byte, version, mask, typebits int) [72][16]byte {
	g := baseGrid()
	bits := make([]byte, 0, len(codewords)*8)
	for _, cw := range codewords {
		for i := 7; i >= 0; i-- {
			bits = append(bits, byte((cw>>uint(i))&1))
		}
	}
	idx := 0
	for r := 0; r < 72; r++ {
		prow := placeTable[version][r]
		mrow := maskTable[mask][r]
		for c := 0; c < 16; c++ {
			if (prow>>uint(c))&1 != 0 {
				if idx < len(bits) {
					g[r][c] = bits[idx] ^ byte((mrow>>uint(c))&1)
				}
				idx++
			}
		}
	}
	stream := make([]byte, 72)
	for i := 0; i < 72; i++ {
		stream[i] = byte((typebits >> uint(17-(i%18))) & 1)
	}
	k := 0
	for r := 0; r < 72; r++ {
		for c := 0; c < 16; c++ {
			if (ringTable[r]>>uint(c))&1 != 0 {
				if k < len(stream) {
					g[r][c] = stream[k]
				}
				k++
			}
		}
	}
	for r := 0; r < 72; r++ {
		g[r][15] = g[r][14]
	}
	return g
}

func typebitsFor(version, level, mask int) int {
	return typebitsTable[(levelBits[level]+version*4)*32+mask]
}

// cdiv 是 C 语义的整数除法（向零截断）。
func cdiv(a, b int) int {
	if b == 0 {
		return 0
	}
	if a >= 0 {
		return a / b
	}
	return -((-a) / b)
}

func maxRun(vals []byte, wantOne bool) int {
	best, cur := 0, 0
	for _, v := range vals {
		if (v != 0) == wantOne {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 0
		}
	}
	return best
}

// gridRows 按行抽取该版本的数据格（不含固定模块）。
func gridRows(g [72][16]byte, version int) [][]byte {
	rows := make([][]byte, 72)
	for r := 0; r < 72; r++ {
		prow := placeTable[version][r]
		row := make([]byte, 0, 16)
		for c := 0; c < 16; c++ {
			if (prow>>uint(c))&1 != 0 {
				row = append(row, g[r][c])
			}
		}
		rows[r] = row
	}
	return rows
}

// maskScore 计算掩码评分（原生取分数最大者）。
func maskScore(rows [][]byte) int {
	total, ones := 0, 0
	for _, r := range rows {
		total += len(r)
		for _, v := range r {
			if v != 0 {
				ones++
			}
		}
	}
	if total == 0 {
		total = 1
	}
	var w8 [][]byte
	for _, r := range rows {
		if len(r) > 7 {
			w8 = append(w8, r)
		}
	}
	n8 := 0
	for _, r := range w8 {
		if maxRun(r, true) > 7 {
			n8++
		}
	}
	d8 := len(w8)
	if d8 == 0 {
		d8 = 1
	}
	var w4 [][]byte
	for _, r := range rows {
		if len(r) > 3 {
			w4 = append(w4, r)
		}
	}
	n4 := 0
	for _, r := range w4 {
		if maxRun(r, false) > 3 {
			n4++
		}
	}
	d4 := len(w4)
	if d4 == 0 {
		d4 = 1
	}
	return cdiv(100*ones, total) + cdiv(-100*n8, d8) + cdiv(-100*n4, d4)
}

// ChooseMask 遍历 32 种掩码，返回分数最大者（严格大于才替换）。
func ChooseMask(info *EncodeInfo) int {
	bestMask := 0
	bestScore := -0x7FFFFFFF
	for m := 0; m < 32; m++ {
		tb := typebitsFor(info.Version, info.Level, m)
		g := buildGrid(info.Codewords, info.Version, m, tb)
		s := maskScore(gridRows(g, info.Version))
		if s > bestScore {
			bestScore = s
			bestMask = m
		}
	}
	return bestMask
}
