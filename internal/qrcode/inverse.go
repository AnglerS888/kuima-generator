package qrcode

import (
	"errors"
	"strings"
)

// 逆变换：base-45 文本 → 字节流 → base-82 文本（BFS 反推，变换非双射）。

// ============================================================
// 逆变换（仅解码用）
// ============================================================

func emit45(c int) ([]int, int) {
	var out []int
	for c >= 45 {
		out = append(out, c%45)
		c /= 45
	}
	return out, c
}

type st2Key struct{ j, c int }

// invStage2 由 base-45 低位数字序列反推 stage1 的字节流。
func invStage2(digits []int) []byte {
	n := len(digits)
	seen := map[st2Key]bool{}
	type item struct {
		j, c int
		path []byte
	}
	var q []item
	for b0 := 0; b0 < 256; b0++ {
		digs, c := emit45(b0)
		if len(digs) > n {
			continue
		}
		ok := true
		for i := range digs {
			if digs[i] != digits[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		k := st2Key{len(digs), c}
		if seen[k] {
			continue
		}
		seen[k] = true
		q = append(q, item{len(digs), c, []byte{byte(b0)}})
	}
	for head := 0; head < len(q); head++ {
		it := q[head]
		if it.j == n && it.c == 0 {
			return it.path
		}
		if it.j == n-1 && it.c != 0 && it.c == digits[n-1] {
			return it.path
		}
		if it.j >= n {
			continue
		}
		for b := 0; b < 256; b++ {
			X := b + 256*it.c
			digs, cnew := emit45(X)
			t := len(digs)
			if it.j+t > n {
				continue
			}
			ok := true
			for i := 0; i < t; i++ {
				if digs[i] != digits[it.j+i] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			k := st2Key{it.j + t, cnew}
			if seen[k] {
				continue
			}
			seen[k] = true
			path := make([]byte, len(it.path)+1)
			copy(path, it.path)
			path[len(it.path)] = byte(b)
			q = append(q, item{it.j + t, cnew, path})
		}
	}
	return nil
}

func step1(acc uint32, d int) ([]byte, uint32) {
	acc = uint32(d) + acc*82
	var out []byte
	for acc >= 0x100 {
		out = append(out, byte(acc&0xFF))
		hi := acc >> 16
		acc >>= 8
		if hi == 0 {
			break
		}
	}
	return out, acc
}

type st1Key struct {
	k   int
	acc uint32
}

// invStage1 由字节流反推 base-82 文本。
func invStage1(body []byte) string {
	n := len(body)
	target := make([]byte, n)
	for i := 0; i < n; i++ {
		target[i] = body[n-1-i]
	}
	seen := map[st1Key]bool{}
	type item struct {
		k   int
		acc uint32
		ds  []byte
	}
	var q []item
	for d := 1; d < 82; d++ {
		em, acc := step1(0, d)
		if len(em) > n {
			continue
		}
		ok := true
		for i := range em {
			if em[i] != target[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		k := st1Key{len(em), acc}
		if seen[k] {
			continue
		}
		seen[k] = true
		q = append(q, item{len(em), acc, []byte{byte(d)}})
	}
	for head := 0; head < len(q); head++ {
		it := q[head]
		if it.k == n && it.acc == 0 {
			return digitsTo82(it.ds)
		}
		if it.k == n-1 && it.acc != 0 && it.acc == uint32(target[n-1]) {
			return digitsTo82(it.ds)
		}
		if it.k >= n {
			continue
		}
		for d := 0; d < 82; d++ {
			em, a2 := step1(it.acc, d)
			if len(em) > n-it.k {
				continue
			}
			ok := true
			for i := range em {
				if em[i] != target[it.k+i] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			k := st1Key{it.k + len(em), a2}
			if seen[k] {
				continue
			}
			seen[k] = true
			ds := make([]byte, len(it.ds)+1)
			copy(ds, it.ds)
			ds[len(it.ds)] = byte(d)
			q = append(q, item{it.k + len(em), a2, ds})
		}
	}
	return ""
}

func digitsTo82(ds []byte) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteByte(alpha82[int(d)])
	}
	return b.String()
}

// Invert 把 base-45 文本还原成原始载荷。
func Invert(text string) (string, error) {
	digits := make([]int, 0, len(text))
	for i := len(text) - 1; i >= 0; i-- {
		d := idx45[text[i]]
		if d < 0 {
			return "", errors.New("非 base-45 字符: " + string(text[i]))
		}
		digits = append(digits, int(d))
	}
	body := invStage2(digits)
	if body == nil {
		return "", errors.New("base-45 逆变换失败")
	}
	out := invStage1(body)
	if out == "" {
		return "", errors.New("base-82 逆变换失败")
	}
	return out, nil
}
