package qrcode

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"sort"
)

// 解码主干：解析码字、分块纠错、位姿搜索与逆变换。

// ============================================================
// 解码：灰度 → 二值 → 定位图案求位姿 → 采样 → RS 纠错 → 逆变换
// ============================================================

// DecodeResult 是解码结果。
type DecodeResult struct {
	Family      string `json:"family"`
	Version     int    `json:"version"`
	ECLevel     string `json:"ecLevel"`
	Mask        int    `json:"mask"`
	Typebits    int    `json:"typebits"`
	Mode        string `json:"mode"`
	Count       int    `json:"count"`
	QRText      string `json:"qrText,omitempty"`
	Payload     string `json:"payload"`
	Corrections int    `json:"corrections"`

	// Box 是码在图片里的位置（归一化 0..1），给前端做"裁到码"的缩略图用。
	// 一张 1080×2400 的手机截图直接等比塞进 200px 方框，码只剩下几十像素，看不出是什么。
	Box *DecodeBox `json:"box,omitempty"`
}

// DecodeBox 描述码的外接圆：圆心与半径都按图片宽高归一化。
// 非正方形图宽高比例不同，所以 rx / ry 分开给。
type DecodeBox struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	RX float64 `json:"rx"`
	RY float64 `json:"ry"`
}

// correctCodewords 把交织的码字流按块结构拆开、逐块 RS 校验/纠错，返回数据码字。
// 注意：v1/v2 的 Q、H 是 2 块结构，必须解交织后分块校验（整体校验会失败）。
func correctCodewords(cws []byte, version, level int) ([]byte, int, error) {
	lv := ecTable[version].Levels[level]
	var lens []int
	for _, b := range lv.Blocks {
		for i := 0; i < b.Count; i++ {
			lens = append(lens, b.DataCW)
		}
	}
	nb := len(lens)
	if nb == 0 {
		return nil, 0, errors.New("块结构为空")
	}
	maxLen := 0
	for _, l := range lens {
		if l > maxLen {
			maxLen = l
		}
	}
	data := make([][]byte, nb)
	for i := range data {
		data[i] = make([]byte, lens[i])
	}
	pos := 0
	for i := 0; i < maxLen; i++ {
		for b := 0; b < nb; b++ {
			if i < lens[b] {
				if pos >= len(cws) {
					return nil, 0, errors.New("码字数量不足")
				}
				data[b][i] = cws[pos]
				pos++
			}
		}
	}
	ec := make([][]byte, nb)
	for b := range ec {
		ec[b] = make([]byte, lv.ECPerBlock)
	}
	for i := 0; i < lv.ECPerBlock; i++ {
		for b := 0; b < nb; b++ {
			if pos >= len(cws) {
				return nil, 0, errors.New("纠错码字数量不足")
			}
			ec[b][i] = cws[pos]
			pos++
		}
	}
	out := make([]byte, 0, len(cws))
	nfix := 0
	for b := 0; b < nb; b++ {
		block := make([]byte, 0, len(data[b])+len(ec[b]))
		block = append(block, data[b]...)
		block = append(block, ec[b]...)
		if !allZero(syndromes(block, lv.ECPerBlock)) {
			res, err := rsCorrect(block, lv.ECPerBlock)
			if err != nil {
				return nil, 0, err
			}
			nfix += res.NFix
			block = res.CW
		}
		out = append(out, block[:lens[b]]...)
	}
	return out, nfix, nil
}

// parseStream 解析数据码字里的模式与文本。
func parseStream(data []byte) (mode string, count int, text string, eci int, err error) {
	if len(data) == 0 {
		return "", 0, "", -1, errors.New("数据码字为空")
	}
	var bits []byte
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (b>>uint(i))&1)
		}
	}
	pos := 0
	take := func(n int) (int, bool) {
		if n < 0 || pos+n > len(bits) {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			v = v<<1 | int(bits[pos])
			pos++
		}
		return v, true
	}
	eci = -1
	if len(bits) > 12 && bits[0] == 0 && bits[1] == 1 && bits[2] == 1 && bits[3] == 1 {
		if _, ok := take(4); !ok {
			return "", 0, "", eci, errors.New("ECI 标识不完整")
		}
		v, ok := take(8)
		if !ok {
			return "", 0, "", eci, errors.New("ECI 数据不完整")
		}
		eci = v
	}
	m, ok := take(4)
	if !ok {
		return "", 0, "", eci, errors.New("模式标识不完整")
	}
	switch m {
	case ModeNumeric:
		count, ok = take(10)
		if !ok {
			return "", 0, "", eci, errors.New("数字模式计数不完整")
		}
		var sb []byte
		for len(sb) < count {
			n := 3
			if count-len(sb) < 3 {
				n = count - len(sb)
			}
			nb := 4
			if n == 3 {
				nb = 10
			} else if n == 2 {
				nb = 7
			}
			v, ok := take(nb)
			if !ok {
				return "", 0, "", eci, errors.New("数字模式数据被截断")
			}
			// 10bit 最多 1023，三位数必须 <1000；超出说明码流语义非法。
			limit := 1
			for i := 0; i < n; i++ {
				limit *= 10
			}
			if v >= limit {
				return "", 0, "", eci, errors.New("数字模式取值越界")
			}
			s := fmt.Sprintf("%0*d", n, v)
			sb = append(sb, s...)
		}
		return "NUMERIC", count, string(sb), eci, nil
	case ModeAlnum:
		count, ok = take(9)
		if !ok {
			return "", 0, "", eci, errors.New("字母数字模式计数不完整")
		}
		var sb []byte
		for len(sb) < count {
			if count-len(sb) >= 2 {
				v, ok := take(11)
				if !ok {
					return "", 0, "", eci, errors.New("字母数字模式数据被截断")
				}
				if v >= 45*45 {
					return "", 0, "", eci, errors.New("字母数字模式取值越界")
				}
				sb = append(sb, alpha45[v/45], alpha45[v%45])
			} else {
				v, ok := take(6)
				if !ok {
					return "", 0, "", eci, errors.New("字母数字模式数据被截断")
				}
				if v >= len(alpha45) {
					return "", 0, "", eci, errors.New("字母数字模式取值越界")
				}
				sb = append(sb, alpha45[v])
			}
		}
		if len(sb) > count {
			sb = sb[:count]
		}
		return "ALPHANUMERIC", count, string(sb), eci, nil
	case ModeByte:
		count, ok = take(8)
		if !ok {
			return "", 0, "", eci, errors.New("字节模式计数不完整")
		}
		raw := make([]byte, 0, count)
		for i := 0; i < count; i++ {
			v, ok := take(8)
			if !ok {
				return "", 0, "", eci, errors.New("字节模式数据被截断")
			}
			raw = append(raw, byte(v))
		}
		return "BYTE", count, string(raw), eci, nil
	}
	return "", 0, "", eci, fmt.Errorf("不支持的模式 %d", m)
}

// DecodeBinary 在二值图上解出载荷。
func DecodeBinary(img *BinImage) (*DecodeResult, error) {
	return DecodeBinaryContext(context.Background(), img)
}

// DecodeBinaryContext 是 DecodeBinary 的可取消版本。
// 几何搜索和微调都很吃 CPU，客户端断开后应尽快退出。
func DecodeBinaryContext(ctx context.Context, img *BinImage) (*DecodeResult, error) {
	if img.W == 0 || img.H == 0 {
		return nil, errors.New("图像为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cx0, cy0, s0, th0, ok := findFinderPose(img)
	if !ok {
		return nil, errors.New("未找到葵花码的 3 组定位图案")
	}
	K0 := s0 * 11.111111
	diag := float64(max(img.W, img.H))

	type bestCand struct {
		score                float64
		version, level, mask int
		data                 []byte
		nfix                 int
		cx, cy, K, a         float64 // 命中的几何，用来给前端算缩略图裁切框
	}
	var best *bestCand

	// 几何判定分三段，越往后越贵：同步环 + 角标 → 格式环 → RS。
	// 同步环门槛有意放低：真实照片/截图缩放后细线会糊，实测一张 900px 的图
	// 最佳几何也只能拿到 29/36；门槛卡在 31 会直接判定"没有码"。RS 才是最终仲裁。
	check := func(cell cellFn) (ring, corner, fmtM, idx int) {
		ring = ringScore(cell)
		if ring < minRingScore {
			return
		}
		corner = cornerScore(cell)
		fmtM, idx = bestFormat(cell)
		return
	}
	tryDecode := func(cx, cy, K, a, th float64) {
		if ctx.Err() != nil {
			return
		}
		cell := makeCell(img, cx, cy, K, a, th)
		ring, corner, m, idx := check(cell)
		if m < 33 {
			return
		}
		version := idx / 128
		level := levelFromBits[(idx%128)/32]
		mask := idx % 32
		cws := wxCodewords(cell, version, mask)
		data, nfix, cerr := correctCodewords(cws, version, level)
		if cerr != nil {
			return
		}
		// 纠错数越少越可信，避免挑到一个“碰巧能修”的错位解
		score := float64(ring*2+m+corner) - float64(nfix)*6
		if best == nil || score > best.score {
			best = &bestCand{
				score: score, version: version, level: level, mask: mask,
				data: data, nfix: nfix, cx: cx, cy: cy, K: K, a: a,
			}
		}
	}

	// 几何搜索用两个独立维度（环距 pitch、内圈偏移 a），而不是只动 pitch。
	// 外部渲染器把整码缩放/加白边后，pitch 与 a 的比例会变（实测某张官方码
	// 需要 pitch≈11.95 / a≈9.9，固定 a=10.5 时怎么调 pitch 都对不上）。
	//
	// 搜索窗口必须够宽：同一张码在 1080/900/720 三种缩放下，最佳几何的 a 会
	// 在 8.9~12.6、kf 在 0.90~1.10 之间漂（重采样引起的亚像素相位差），
	// 窗口收窄到 a≤11.7 / kf≥0.94 就会整档解不出来。
	//
	// 代价控制靠"两段式"：先用最便宜的同步环打分铺满窗口，只对分数靠前的
	// 少数候选做格式环匹配和 RS —— 格式环匹配（512 项）比环采样贵两个数量级。
	//
	// 行距 5°（0.0873 rad），所以只需在若干基准角附近各覆盖 ±2.5°（一个周期）。
	// 步长必须够细：实测某张官方码只有 -0.573° 这一个相位能对齐，
	// 步长放到 1.26° 就会整张解不出来 —— 0.63°（0.011 rad）是实测定下来的上限。
	dths := []float64{-0.044, -0.033, -0.022, -0.011, 0, 0.011, 0.022, 0.033, 0.044}

	// 基准角：0（码没转）、th0（按定位图案估出的旋转）、-th0（防止符号约定相反）。
	// 小角度时三者几乎重合，去重掉避免白跑三倍；旋转大时才真正需要多基准。
	var angles []float64
	addAngle := func(v float64) {
		for _, a := range angles {
			if math.Abs(a-v) < 0.004 {
				return
			}
		}
		angles = append(angles, v)
	}
	for _, d := range dths {
		addAngle(d)
	}
	if math.Abs(th0) > 0.004 {
		for _, d := range dths {
			addAngle(th0 + d)
		}
	}
	if math.Abs(th0) > 0.05 {
		for _, d := range dths {
			addAngle(-th0 + d)
		}
	}

	kfs := []float64{0.84, 0.86, 0.88, 0.90, 0.92, 0.94, 0.96, 0.98,
		1.0, 1.02, 1.04, 1.06, 1.08, 1.10, 1.12}

	type cand struct {
		a, kf, th float64
		score     int
	}
	var coarse []cand
	for _, th := range angles {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, kf := range kfs {
			pitch := K0 * kf
			for a := 8.2; a <= 13.4001; a += 0.1 {
				cell := makeCell(img, cx0, cy0, pitch, a, th)
				ring := ringScore(cell)
				if ring < minRingScore {
					continue
				}
				// 角标不作为硬性门槛（部分渲染器不带角标），只用来排序
				coarse = append(coarse, cand{a, kf, th, ring*2 + cornerScore(cell)})
			}
		}
	}
	sort.Slice(coarse, func(i, j int) bool { return coarse[i].score > coarse[j].score })
	if len(coarse) > 1500 {
		coarse = coarse[:1500]
	}
	for i, c := range coarse {
		if i%32 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tryDecode(cx0, cy0, K0*c.kf, c.a, c.th)
		if best != nil && best.nfix == 0 {
			break // 零纠错，已经是最优解
		}
	}
	// 粗搜没成功时，对最好的几个几何做中心/尺度微调（抗亚像素偏移）
	if best == nil {
		for i := 0; i < 10 && i < len(coarse); i++ {
			c := coarse[i]
			for _, dcx := range []float64{-0.004, 0, 0.004} {
				for _, dcy := range []float64{-0.004, 0, 0.004} {
					for _, dkf := range []float64{-0.01, 0, 0.01} {
						for _, da := range []float64{-0.1, 0, 0.1} {
							tryDecode(cx0+dcx*diag, cy0+dcy*diag, K0*(c.kf+dkf), c.a+da, c.th)
						}
					}
				}
			}
			if best != nil {
				break
			}
		}
	}
	if best == nil {
		return nil, errors.New("葵花码读取未通过 Reed-Solomon 校验")
	}
	mode, count, text, _, err := parseStream(best.data)
	if err != nil {
		return nil, err
	}
	payload := text
	qrText := ""
	if mode == "ALPHANUMERIC" {
		qrText = text
		if inv, ierr := Invert(text); ierr == nil {
			payload = inv
		} else {
			return nil, fmt.Errorf("逆变换失败: %v", ierr)
		}
	}
	// 码的外接圆半径：最外一圈数据在第 15 列，半径 = (15 + a) * K。
	// 这个关系在已知几何的渲染图上验证过（600px 码算出 277.9，真值 280）。
	// 乘 1.08 留一点边，前端裁出来的缩略图不至于把码的尖角切掉。
	rad := (15 + best.a) * best.K * 1.08
	box := &DecodeBox{
		X:  best.cx / float64(img.W),
		Y:  best.cy / float64(img.H),
		RX: rad / float64(img.W),
		RY: rad / float64(img.H),
	}

	return &DecodeResult{
		Family:      "WX_CODE",
		Version:     best.version,
		ECLevel:     string(LevelNames[best.level]),
		Mask:        best.mask,
		Typebits:    typebitsFor(best.version, best.level, best.mask),
		Mode:        mode,
		Count:       count,
		QRText:      qrText,
		Payload:     payload,
		Corrections: best.nfix,
		Box:         box,
	}, nil
}

// DecodeImage 从任意图片解码葵花码。
//
// 分辨率采用"阶梯"策略：先降到 1200 解（绝大多数图够快也够准）；只有失败且原图
// 更大时，才按接近原图的分辨率再解一次 —— 手机整屏截图、海报这类"小码贴在大画布
// 上"的图，降到 1200 后码可能只剩几十像素，连定位图案都凑不够 12px（实测 1080 宽
// 截图里 200px 的码，降到 540 宽必失败，按原尺寸就能零纠错解出）。
func DecodeImage(img image.Image) (*DecodeResult, error) {
	return DecodeImageContext(context.Background(), img)
}

// DecodeImageContext 是 DecodeImage 的可取消版本，并按需懒加载高分辨率重试。
func DecodeImageContext(ctx context.Context, img image.Image) (*DecodeResult, error) {
	bounds := img.Bounds()
	maxSide := max(bounds.Dx(), bounds.Dy())
	if maxSide <= 0 {
		return nil, errors.New("图像为空")
	}

	sides := []int{min(1200, maxSide)}
	if maxSide > 1200 {
		sides = append(sides, min(maxSide, maxDecodeSide))
	}

	var firstErr error
	for _, side := range sides {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// 第一阶段就按目标尺寸采样，避免为大图先分配完整二值图。
		b := ToBinaryScaled(img, side)
		res, err := DecodeBinaryContext(ctx, b)
		if err == nil {
			return res, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// maxDecodeSide 是高分辨率重试时的边长上限（2400 已经覆盖 1080×2400 的手机截图）。
const maxDecodeSide = 2400
