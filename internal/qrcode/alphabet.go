package qrcode

import (
	"fmt"
	"strings"
)

// 字母表与载荷正变换：base-82 文本 → 字节流 → base-45 文本（流式除法）。
const alpha82 = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ!#$&'()*+,/:;=?@-._~"

const alpha45 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

var (
	idx82 [256]int8
	idx45 [256]int8
)

// init 建表：两张字母表的反查表 + GF(256) 的指数/对数表。
func init() {
	initAlphabet()
	initGalois()
}

// initAlphabet 填充 idx82 / idx45（-1 表示不属于该字母表）。
func initAlphabet() {
	for i := range idx82 {
		idx82[i] = -1
		idx45[i] = -1
	}
	for i := 0; i < len(alpha82); i++ {
		idx82[alpha82[i]] = int8(i)
	}
	for i := 0; i < len(alpha45); i++ {
		idx45[alpha45[i]] = int8(i)
	}
}

// stage1 把 base-82 文本压成字节流：首位是前导 '0' 的个数，其余为大端字节。
func stage1(s string) ([]byte, error) {
	var (
		els     []byte
		acc     uint32
		lead    int
		started bool
	)
	for i := 0; i < len(s); i++ {
		d := idx82[s[i]]
		if d < 0 {
			return nil, fmt.Errorf("字符 %q 不在 base-82 字母表内", rune(s[i]))
		}
		dd := uint32(d)
		if started || dd != 0 {
			acc = dd + acc*82
			if acc >= 0x100 {
				for {
					els = append(els, byte(acc&0xFF))
					hi := acc >> 16
					acc >>= 8
					if hi == 0 {
						break
					}
				}
			}
			started = true
		} else {
			started = false
			lead++
		}
	}
	if acc != 0 {
		els = append(els, byte(acc&0xFF))
	}
	out := make([]byte, 0, len(els)+1)
	out = append(out, byte(lead))
	for i := len(els) - 1; i >= 0; i-- {
		out = append(out, els[i])
	}
	return out, nil
}

// stage2 把 stage1 的字节流重编码成 base-45 文本（流式除法，一旦产生即固定）。
func stage2(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	lead := int(data[0])
	var digits []int
	if len(data) > 1 {
		carry := int(data[1])
		for carry >= 45 {
			q := carry / 45
			digits = append(digits, carry-q*45)
			carry = q
		}
		for i := 2; i < len(data); i++ {
			carry = int(data[i]) + carry*256
			for carry >= 45 {
				q := carry / 45
				digits = append(digits, carry-q*45)
				carry = q
			}
		}
		if carry != 0 {
			digits = append(digits, carry)
		}
	}
	var b strings.Builder
	b.Grow(lead + len(digits))
	for i := 0; i < lead; i++ {
		b.WriteByte('0')
	}
	for i := len(digits) - 1; i >= 0; i-- {
		b.WriteByte(alpha45[digits[i]])
	}
	return b.String()
}

// transform 完成 base-82 → base-45 变换（等价于原生 revert.encode）。
func transform(payload string) (string, error) {
	body, err := stage1(payload)
	if err != nil {
		return "", err
	}
	return stage2(body), nil
}

// isAlpha82 判断文本是否全部落在 URL 字母表内。
func isAlpha82(payload string) bool {
	if payload == "" {
		return false
	}
	for i := 0; i < len(payload); i++ {
		if idx82[payload[i]] < 0 {
			return false
		}
	}
	return true
}

// isNumeric 判断是否全为十进制数字。
func isNumeric(payload string) bool {
	if payload == "" {
		return false
	}
	for i := 0; i < len(payload); i++ {
		if payload[i] < '0' || payload[i] > '9' {
			return false
		}
	}
	return true
}
