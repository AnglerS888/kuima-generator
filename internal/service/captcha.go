package service

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"image"
	"image/color"
	"image/png"
	mrand "math/rand"
	"strings"
	"sync"
	"time"

	"wxcode/internal/util"
)

const CaptchaTTL = 5 * time.Minute

const captchaMax = 5000

const captchaLength = 4

var errCaptchaRequired = errors.New("请输入验证码")

var errCaptchaInvalid = errors.New("验证码错误或已过期，请刷新后重试")

type captchaEntry struct {
	code    string
	expires time.Time
}

var captchaStore = struct {
	sync.Mutex
	items map[string]captchaEntry
}{items: make(map[string]captchaEntry)}

// 数字 + 字母，去掉 0/O/1/I 等易混淆字符；32 个字符便于拒绝采样。
const captchaAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func newCaptchaCode() string {
	for {
		out := make([]byte, captchaLength)
		hasDigit, hasLetter := false, false
		for i := range out {
			var b [1]byte
			for {
				if _, err := rand.Read(b[:]); err != nil {
					panic("crypto/rand 不可用: " + err.Error())
				}
				// 224 = 32×7，拒绝尾部避免按 32 取模产生偏差。
				if int(b[0]) < 224 {
					out[i] = captchaAlphabet[int(b[0])%len(captchaAlphabet)]
					break
				}
			}
			if out[i] >= '0' && out[i] <= '9' {
				hasDigit = true
			} else {
				hasLetter = true
			}
		}
		// 保证每次都是数字 + 字母混合，而不是碰巧全是数字或全是字母。
		if hasDigit && hasLetter {
			return string(out)
		}
	}
}

// newCaptcha 生成验证码并存入内存；返回 id 和答案，答案供服务端校验 / 测试使用。
func NewCaptcha() (id, code string) {
	code = newCaptchaCode()
	id = util.RandomHex(16)
	now := time.Now()
	entry := captchaEntry{code: code, expires: now.Add(CaptchaTTL)}

	captchaStore.Lock()
	defer captchaStore.Unlock()
	for k, v := range captchaStore.items {
		if now.After(v.expires) {
			delete(captchaStore.items, k)
		}
	}
	if len(captchaStore.items) >= captchaMax {
		// 极端情况下直接清掉一半，避免内存无限增长。
		n := len(captchaStore.items) / 2
		for k := range captchaStore.items {
			delete(captchaStore.items, k)
			n--
			if n <= 0 {
				break
			}
		}
	}
	captchaStore.items[id] = entry
	return id, code
}

// verifyCaptcha 一次性校验：无论成功失败，这个 id 都会被删除。
func verifyCaptcha(id, code string) bool {
	id = strings.TrimSpace(id)
	code = strings.ToUpper(strings.TrimSpace(code))
	if id == "" || code == "" {
		return false
	}
	captchaStore.Lock()
	entry, ok := captchaStore.items[id]
	delete(captchaStore.items, id)
	captchaStore.Unlock()
	if !ok || time.Now().After(entry.expires) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(entry.code), []byte(code)) == 1
}

func CheckCaptcha(id, code string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(code) == "" {
		return errCaptchaRequired
	}
	if !verifyCaptcha(id, code) {
		return errCaptchaInvalid
	}
	return nil
}

// 5×7 数字点阵。验证码用 PNG 栅格化，答案不会以文本形式暴露在前端源码里。
// 顺序与 captchaAlphabet 一致：2-9、A-H、J-N、P-Z。
var captchaGlyphs = [32][7]uint8{
	{0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111}, // 2
	{0b11111, 0b00010, 0b00100, 0b00010, 0b00001, 0b10001, 0b01110}, // 3
	{0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010}, // 4
	{0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110}, // 5
	{0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110}, // 6
	{0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000}, // 7
	{0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110}, // 8
	{0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100}, // 9
	{0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001}, // A
	{0b11110, 0b10001, 0b10001, 0b11110, 0b10001, 0b10001, 0b11110}, // B
	{0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110}, // C
	{0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110}, // D
	{0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111}, // E
	{0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b10000}, // F
	{0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01111}, // G
	{0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001}, // H
	{0b00111, 0b00010, 0b00010, 0b00010, 0b00010, 0b10010, 0b01100}, // J
	{0b10001, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010, 0b10001}, // K
	{0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111}, // L
	{0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001}, // M
	{0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001}, // N
	{0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000}, // P
	{0b01110, 0b10001, 0b10001, 0b10001, 0b10101, 0b10010, 0b01101}, // Q
	{0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001}, // R
	{0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110}, // S
	{0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100}, // T
	{0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110}, // U
	{0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100}, // V
	{0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b11011, 0b10001}, // W
	{0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001}, // X
	{0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100, 0b00100}, // Y
	{0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b10000, 0b11111}, // Z
}

func captchaDrawLine(dst *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx, dy := absInt(x1-x0), -absInt(y1-y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		if image.Pt(x0, y0).In(dst.Bounds()) {
			dst.SetRGBA(x0, y0, c)
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func drawCaptchaGlyph(dst *image.RGBA, glyph, x0, y0, scale int, c color.RGBA) {
	if glyph < 0 || glyph >= len(captchaGlyphs) {
		return
	}
	for row, bits := range captchaGlyphs[glyph] {
		for col := 0; col < 5; col++ {
			if bits&(1<<uint(4-col)) == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					x, y := x0+col*scale+dx, y0+row*scale+dy
					if image.Pt(x, y).In(dst.Bounds()) {
						dst.SetRGBA(x, y, c)
					}
				}
			}
		}
	}
}

// captchaPNG 生成 132×44 的数字 + 字母验证码图片。
func CaptchaPNG(code string) []byte {
	const w, h, scale = 132, 44, 4
	bg := []color.RGBA{
		{R: 244, G: 246, B: 250, A: 255}, {R: 238, G: 244, B: 255, A: 255},
		{R: 247, G: 249, B: 252, A: 255}, {R: 241, G: 247, B: 242, A: 255},
	}[mrand.Intn(4)]
	ink := []color.RGBA{
		{R: 27, G: 38, B: 56, A: 255}, {R: 47, G: 75, B: 124, A: 255},
		{R: 35, G: 78, B: 82, A: 255}, {R: 91, G: 58, B: 41, A: 255},
	}
	noise := []color.RGBA{
		{R: 157, G: 180, B: 216, A: 255}, {R: 183, G: 199, B: 223, A: 255},
		{R: 169, G: 193, B: 178, A: 255}, {R: 210, G: 184, B: 165, A: 255},
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	for i := 0; i < 8; i++ {
		c := noise[mrand.Intn(len(noise))]
		captchaDrawLine(img, mrand.Intn(w), mrand.Intn(h), mrand.Intn(w), mrand.Intn(h), c)
	}
	for i := 0; i < 70; i++ {
		img.SetRGBA(mrand.Intn(w), mrand.Intn(h), noise[mrand.Intn(len(noise))])
	}
	for i := 0; i < len(code); i++ {
		glyph := strings.IndexByte(captchaAlphabet, code[i])
		x := 12 + i*30 + mrand.Intn(3)
		y := 7 + mrand.Intn(6)
		// 先画一层浅色错位影子，再叠加主体，增加自动识别难度。
		drawCaptchaGlyph(img, glyph, x+1, y+1, scale, noise[mrand.Intn(len(noise))])
		drawCaptchaGlyph(img, glyph, x, y, scale, ink[mrand.Intn(len(ink))])
	}
	// 再压一条细干扰线，不遮挡整体轮廓。
	captchaDrawLine(img, 0, 8+mrand.Intn(30), w-1, 8+mrand.Intn(30), noise[mrand.Intn(len(noise))])

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
