package qrcode

import (
	"errors"
)

// GF(256)（本原多项式 0x11D）、Reed-Solomon 生成多项式与编码、块交织。

// ============================================================
// GF(256)，本原多项式 0x11D（与原生库一致）
// ============================================================

var (
	gfExp [512]byte
	gfLog [256]byte
)

func initGalois() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x = gfMulRaw(x, 2)
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMulRaw(a, b int) int {
	r := 0
	for i := 0; i < 8; i++ {
		if b&1 != 0 {
			r ^= a
		}
		b >>= 1
		a <<= 1
		if a&0x100 != 0 {
			a ^= 0x11D
		}
	}
	return r & 0xFF
}

func gmul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[(int(gfLog[a])+int(gfLog[b]))%255]
}

func gdiv(a, b byte) (byte, error) {
	if b == 0 {
		return 0, errors.New("GF 除零")
	}
	if a == 0 {
		return 0, nil
	}
	return gfExp[(int(gfLog[a])-int(gfLog[b])+255)%255], nil
}

func ginv(a byte) byte {
	return gfExp[(255-int(gfLog[a]))%255]
}

func gpow(a byte, n int) byte {
	r := byte(1)
	for i := 0; i < n; i++ {
		r = gmul(r, a)
	}
	return r
}

var rsGenCache = map[int][]byte{}

// rsGenerator 求 g(x) = Π(x - α^i), i = 0..degree-1，gen[0] 为最高次系数 1。
func rsGenerator(degree int) []byte {
	if g, ok := rsGenCache[degree]; ok {
		return g
	}
	g := []byte{1}
	for i := 0; i < degree; i++ {
		a := gpow(2, i)
		ng := make([]byte, len(g)+1)
		ng[0] = g[0]
		for j := 1; j < len(g); j++ {
			ng[j] = g[j] ^ gmul(a, g[j-1])
		}
		ng[len(g)] = gmul(a, g[len(g)-1])
		g = ng
	}
	rsGenCache[degree] = g
	return g
}

// rsEncode 计算 data 的 ecLen 个纠错码字。
func rsEncode(data []byte, ecLen int) []byte {
	gen := rsGenerator(ecLen)
	rem := make([]byte, len(data)+ecLen)
	copy(rem, data)
	for i := 0; i < len(data); i++ {
		coef := rem[i]
		if coef == 0 {
			continue
		}
		for j := 1; j < len(gen); j++ {
			rem[i+j] ^= gmul(gen[j], coef)
		}
	}
	return rem[len(data):]
}

// 交织：先逐轮取各块数据码字，再逐轮取各块纠错码字。
func interleave(dataCW []byte, version, level int) ([]byte, int) {
	v := ecTable[version]
	lv := v.Levels[level]
	var blocks [][]byte
	pos := 0
	for _, b := range lv.Blocks {
		for i := 0; i < b.Count; i++ {
			blocks = append(blocks, dataCW[pos:pos+b.DataCW])
			pos += b.DataCW
		}
	}
	ec := make([][]byte, len(blocks))
	for i, b := range blocks {
		ec[i] = rsEncode(b, lv.ECPerBlock)
	}
	out := make([]byte, 0, v.TotalCodewords)
	maxLen := 0
	for _, b := range blocks {
		if len(b) > maxLen {
			maxLen = len(b)
		}
	}
	for i := 0; i < maxLen; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < lv.ECPerBlock; i++ {
		for _, e := range ec {
			out = append(out, e[i])
		}
	}
	return out, lv.ECPerBlock
}
