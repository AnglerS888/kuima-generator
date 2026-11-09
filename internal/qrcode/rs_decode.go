package qrcode

import (
	"errors"
)

// Reed-Solomon 解码：伴随式、Berlekamp-Massey 求错误定位多项式、
// Chien 搜索定位、GF(256) 高斯消元求错误幅度。

// ============================================================
// 编码侧约定（见 rs.go）：g(x) = Π(x - α^i), i = 0..ecLen-1，
// 码字 c[0] 是最高次项，S_i = c(α^i)。
// 位置 p 的错误对应定位数 X = α^(n-1-p)。
// ============================================================

func polyEval(p []byte, x byte) byte {
	y := p[0]
	for i := 1; i < len(p); i++ {
		y = gmul(y, x) ^ p[i]
	}
	return y
}

// polyEvalAsc 求 Σ p[i]·x^i（p[0] 为常数项）。
func polyEvalAsc(p []byte, x byte) byte {
	var y byte
	for i := len(p) - 1; i >= 0; i-- {
		y = gmul(y, x) ^ p[i]
	}
	return y
}

// syndromes 计算 ecLen 个伴随式。
func syndromes(cw []byte, ecLen int) []byte {
	out := make([]byte, ecLen)
	for i := 0; i < ecLen; i++ {
		out[i] = polyEval(cw, gfExp[i])
	}
	return out
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// RSResult 是纠错结果。
type RSResult struct {
	CW   []byte
	NFix int
}

// rsCorrect 纠正 cw 中的错误（最多 ecLen/2 个），失败返回 error。
func rsCorrect(cw []byte, ecLen int) (*RSResult, error) {
	synd := syndromes(cw, ecLen)
	if allZero(synd) {
		out := make([]byte, len(cw))
		copy(out, cw)
		return &RSResult{CW: out, NFix: 0}, nil
	}
	if ecLen <= 0 || ecLen >= len(cw) {
		return nil, errors.New("纠错参数异常")
	}
	loc, err := berlekampMassey(synd)
	if err != nil {
		return nil, err
	}
	t := len(loc) - 1
	if t <= 0 || 2*t > ecLen {
		return nil, errors.New("错误过多，无法纠错")
	}

	// Chien 搜索：位置 p 的定位数 X = α^(n-1-p)，错误位置满足 Λ(X⁻¹) = 0
	n := len(cw)
	var pos []int
	var xs []byte
	for p := 0; p < n; p++ {
		x := gfExp[(n-1-p)%255]
		xinv := ginv(x)
		if polyEvalAsc(loc, xinv) == 0 {
			pos = append(pos, p)
			xs = append(xs, x)
		}
	}
	if len(pos) != t {
		return nil, errors.New("无法定位错误位置")
	}

	// 由前 t 个伴随式解 VanderMonde 方程 S_i = Σ e_k · X_k^i，求出错误幅度
	A := make([][]byte, t)
	for i := 0; i < t; i++ {
		A[i] = make([]byte, t+1)
		for k := 0; k < t; k++ {
			A[i][k] = gpow(xs[k], i)
		}
		A[i][t] = synd[i]
	}
	mag, err := gfSolve(A, t)
	if err != nil {
		return nil, err
	}

	fixed := make([]byte, len(cw))
	copy(fixed, cw)
	for k, p := range pos {
		fixed[p] ^= mag[k]
	}
	if !allZero(syndromes(fixed, ecLen)) {
		return nil, errors.New("纠错失败")
	}
	return &RSResult{CW: fixed, NFix: len(pos)}, nil
}

// berlekampMassey 返回错误定位多项式（升幂，loc[0] = 1）。
func berlekampMassey(synd []byte) ([]byte, error) {
	C := []byte{1}
	B := []byte{1}
	L, m := 0, 1
	b := byte(1)
	for n := 0; n < len(synd); n++ {
		d := synd[n]
		for i := 1; i <= L && i < len(C); i++ {
			d ^= gmul(C[i], synd[n-i])
		}
		if d == 0 {
			m++
			continue
		}
		T := append([]byte{}, C...)
		coef, err := gdiv(d, b)
		if err != nil {
			return nil, err
		}
		for len(C) < len(B)+m {
			C = append(C, 0)
		}
		for j := 0; j < len(B); j++ {
			C[j+m] ^= gmul(coef, B[j])
		}
		if 2*L <= n {
			L = n + 1 - L
			B = T
			b = d
			m = 1
		} else {
			m++
		}
	}
	if L+1 <= len(C) {
		C = C[:L+1]
	}
	for len(C) > 1 && C[len(C)-1] == 0 {
		C = C[:len(C)-1]
	}
	return C, nil
}

// gfSolve 用高斯消元解 GF(256) 上的线性方程组 A·x = b（A 为 n×(n+1)）。
func gfSolve(A [][]byte, n int) ([]byte, error) {
	for col := 0; col < n; col++ {
		pivot := -1
		for r := col; r < n; r++ {
			if A[r][col] != 0 {
				pivot = r
				break
			}
		}
		if pivot < 0 {
			return nil, errors.New("方程无唯一解")
		}
		A[col], A[pivot] = A[pivot], A[col]
		inv := ginv(A[col][col])
		for j := col; j <= n; j++ {
			A[col][j] = gmul(A[col][j], inv)
		}
		for r := 0; r < n; r++ {
			if r == col || A[r][col] == 0 {
				continue
			}
			f := A[r][col]
			for j := col; j <= n; j++ {
				A[r][j] ^= gmul(f, A[col][j])
			}
		}
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = A[i][n]
	}
	return out, nil
}
