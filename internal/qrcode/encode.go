package qrcode

import (
	"errors"
	"fmt"
	"strconv"
)

// 编码主干：模式判定 → 比特流 → 版本与等级选择 → 码字拼装。
const ModeNumeric = 1 // 0001

const ModeAlnum = 2 // 0010

const ModeByte = 4 // 0100

type bitBuf struct{ bits []byte }

func newBitBuf() *bitBuf { return &bitBuf{} }

func (b *bitBuf) append(v, n int) {
	for i := n - 1; i >= 0; i-- {
		b.bits = append(b.bits, byte((v>>uint(i))&1))
	}
}

func (b *bitBuf) len() int { return len(b.bits) }

type encodedData struct {
	Mode int
	Text string // NUMERIC/ALNUM 的（变换后）文本
	Bits []byte // 数据段比特
	Raw  []byte // BYTE 模式的原始字节
}

// encodeData 生成数据段比特流。
func encodeData(payload string) (*encodedData, error) {
	if isNumeric(payload) {
		bb := newBitBuf()
		for i := 0; i < len(payload); i += 3 {
			end := i + 3
			if end > len(payload) {
				end = len(payload)
			}
			chunk := payload[i:end]
			v, err := strconv.Atoi(chunk)
			if err != nil {
				return nil, err
			}
			n := 4
			switch len(chunk) {
			case 3:
				n = 10
			case 2:
				n = 7
			}
			bb.append(v, n)
		}
		return &encodedData{Mode: ModeNumeric, Text: payload, Bits: bb.bits}, nil
	}
	if isAlpha82(payload) {
		text, err := transform(payload)
		if err != nil {
			return nil, err
		}
		bb := newBitBuf()
		for i := 0; i < len(text); i += 2 {
			if i+1 < len(text) {
				v := int(idx45[text[i]])*45 + int(idx45[text[i+1]])
				bb.append(v, 11)
			} else {
				bb.append(int(idx45[text[i]]), 6)
			}
		}
		return &encodedData{Mode: ModeAlnum, Text: text, Bits: bb.bits}, nil
	}
	raw := []byte(payload)
	bb := newBitBuf()
	for _, c := range raw {
		bb.append(int(c), 8)
	}
	return &encodedData{Mode: ModeByte, Text: payload, Bits: bb.bits, Raw: raw}, nil
}

func countBitsFor(mode int) int {
	switch mode {
	case ModeNumeric:
		return 10
	case ModeAlnum:
		return 9
	default:
		return 8
	}
}

// dataCapacity 返回该版本/等级的数据码字数。
func dataCapacity(version, level int) int {
	v := ecTable[version]
	n := 0
	for _, b := range v.Levels[level].Blocks {
		n += b.Count * b.DataCW
	}
	return n
}

type chosenVersion struct {
	Version, Level int
	Enc            *encodedData
}

// chooseVersion 在能装下的前提下取最高纠错等级；自动模式取最小版本。
func chooseVersion(payload string, minLevel, versionOpt int) (*chosenVersion, error) {
	enc, err := encodeData(payload)
	if err != nil {
		return nil, err
	}
	versions := []int{0, 1, 2}
	if versionOpt >= 0 && versionOpt <= 2 {
		versions = []int{versionOpt}
	}
	for _, version := range versions {
		for level := 3; level >= 0; level-- {
			capCW := dataCapacity(version, level)
			header := 4 + countBitsFor(enc.Mode)
			if enc.Mode == ModeByte {
				header += 12 // ECI: 4 + 8
			}
			need := header + len(enc.Bits)
			if capCW*8 >= need {
				if level >= minLevel {
					return &chosenVersion{Version: version, Level: level, Enc: enc}, nil
				}
				break
			}
		}
	}
	if versionOpt >= 0 && versionOpt <= 2 {
		return nil, fmt.Errorf("载荷超出 v%d 容量（可用 v0..v2，请缩短内容或降低纠错等级）", versionOpt)
	}
	return nil, errors.New("载荷超出一枚葵花码容量（v0..v2 均装不下，请缩短内容）")
}

// EncodeInfo 是编码中间/最终结果。
type EncodeInfo struct {
	Payload     string
	Version     int
	Level       int // 0..3
	Mask        int
	Typebits    int
	Mode        int
	Transformed string // ALPHANUMERIC 模式下的 QR 内文本
	DataCW      []byte
	Codewords   []byte
	ECPB        int
	Grid        [72][16]byte
}

// buildCodewords 拼装比特流、填充、纠错与交织。
func buildCodewords(payload string, version, level int, enc *encodedData) (*EncodeInfo, error) {
	dataCW := dataCapacity(version, level)
	capBits := dataCW * 8
	bb := newBitBuf()
	if enc.Mode == ModeByte {
		bb.append(7, 4)  // ECI 指示符
		bb.append(26, 8) // UTF-8
	}
	bb.append(enc.Mode, 4)
	count := len(enc.Text)
	if enc.Mode == ModeByte {
		count = len(enc.Raw)
	}
	bb.append(count, countBitsFor(enc.Mode))
	bb.bits = append(bb.bits, enc.Bits...)
	if bb.len() > capBits {
		return nil, errors.New("数据超出版本容量")
	}
	for i := 0; i < 4 && bb.len() < capBits; i++ {
		bb.bits = append(bb.bits, 0)
	}
	for bb.len()%8 != 0 {
		bb.bits = append(bb.bits, 0)
	}
	cw := make([]byte, 0, dataCW)
	for i := 0; i < bb.len(); i += 8 {
		var v byte
		for j := 0; j < 8; j++ {
			v = v<<1 | bb.bits[i+j]
		}
		cw = append(cw, v)
	}
	pad := true
	for len(cw) < dataCW {
		if pad {
			cw = append(cw, 0xEC)
		} else {
			cw = append(cw, 0x11)
		}
		pad = !pad
	}
	full, ecpb := interleave(cw, version, level)
	return &EncodeInfo{
		Payload:     payload,
		Version:     version,
		Level:       level,
		Mode:        enc.Mode,
		Transformed: enc.Text,
		DataCW:      cw,
		Codewords:   full,
		ECPB:        ecpb,
	}, nil
}

// Encode 一步生成可渲染的葵花码。
func Encode(text string, cfg EncodeConfig) (*EncodeInfo, error) {
	if text == "" {
		return nil, errors.New("载荷不能为空")
	}
	if cfg.MinLevel < 0 || cfg.MinLevel > 3 {
		return nil, errors.New("minLevel 必须在 0..3")
	}
	ch, err := chooseVersion(text, cfg.MinLevel, cfg.Version)
	if err != nil {
		return nil, err
	}
	info, err := buildCodewords(text, ch.Version, ch.Level, ch.Enc)
	if err != nil {
		return nil, err
	}
	mask := cfg.Mask
	if mask < 0 || mask > 31 {
		mask = ChooseMask(info)
	}
	info.Mask = mask
	info.Typebits = typebitsFor(info.Version, info.Level, mask)
	info.Grid = buildGrid(info.Codewords, info.Version, mask, info.Typebits)
	return info, nil
}

// ModeName 返回模式名。
func ModeName(mode int) string {
	switch mode {
	case ModeNumeric:
		return "NUMERIC"
	case ModeAlnum:
		return "ALPHANUMERIC"
	default:
		return "BYTE"
	}
}
