package qrcode

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
)

// 其它输出格式：JPEG 与 GIF（中心 logo、徽标动图逐帧合成）。

// gifMaxFrames 限制输出帧数，控制逐帧绘制与量化的成本。
const gifMaxFrames = 20

// RenderJPEG 输出 JPEG（无 alpha，白底；质量 92）。
func RenderJPEG(info *EncodeInfo, opt RenderOptions) ([]byte, error) {
	img := RenderImage(info, opt)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderGIF 在同一时间轴上合成中心 logo 与徽标，输出无限循环 GIF。
// 超过帧预算时在完整周期内均匀抽帧，避免直接截掉动画后半段。
func RenderGIF(info *EncodeInfo, opt RenderOptions) ([]byte, error) {
	logo := newGIFTrack(opt.LogoFrames, opt.LogoDelays, opt.Logo)
	badge := newGIFTrack(opt.BadgeFrames, opt.BadgeDelays, opt.Badge)
	if ghostActive(ParseColorMode(opt.ColorMode)) {
		badge = gifTrack{}
	}
	opt.Logo, _ = logo.at(0)
	opt.Badge, _ = badge.at(0)
	if logo.duration == 0 && badge.duration == 0 {
		var buf bytes.Buffer
		if err := gif.Encode(&buf, toPaletted(RenderImage(info, opt)), nil); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// 徽标独立逐帧叠加；保留原先“有徽标时不填补空位”的判断。
	baseOpt := opt
	baseOpt.Badge = nil
	if opt.Badge != nil {
		baseOpt.FillBadge = false
	}
	base := renderBase(info, baseOpt)
	cx, cy, rad := logoGeometry(opt)
	out := &gif.GIF{LoopCount: 0}
	cache := palCache{}
	for _, step := range gifTimeline(logo, badge) {
		// 底图始终不变，透明像素不会泄漏上一帧的 logo、徽标或水印。
		canvas := CloneRGBA(base)
		badgeFrame, _ := badge.at(step.time)
		drawBadge(canvas, opt, badgeFrame)
		if logoFrame, _ := logo.at(step.time); logoFrame != nil {
			fillWhiteDisc(canvas, cx, cy, rad+3)
			drawImageCircle(canvas, logoFrame, cx, cy, rad)
		}
		applyWatermark(canvas, opt)
		out.Image = append(out.Image, toPalettedShared(canvas, cache))
		out.Delay = append(out.Delay, step.delay)
		out.Disposal = append(out.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type gifTrack struct {
	frames   []image.Image
	delays   []int
	duration int // 单帧图片为 0，不触发动画输出。
}

func newGIFTrack(frames []image.Image, delays []int, fallback image.Image) gifTrack {
	if len(frames) == 0 && fallback != nil {
		frames = []image.Image{fallback}
	}
	t := gifTrack{frames: frames}
	if len(frames) > 1 {
		for i := range frames {
			d := min(65535, gifDelay(delays, i))
			t.delays = append(t.delays, d)
			t.duration += d
		}
	}
	return t
}

// at 返回指定时间的画面，以及距离下一次切帧的时间；短动画独立循环。
func (t gifTrack) at(time int) (image.Image, int) {
	i, remaining := t.frameAt(time)
	if i < 0 {
		return nil, 0
	}
	return t.frames[i], remaining
}

// frameAt also exposes the frame index for choosing representative samples.
func (t gifTrack) frameAt(time int) (int, int) {
	if len(t.frames) == 0 {
		return -1, 0
	}
	if t.duration == 0 {
		return 0, 0
	}
	position := time % t.duration
	for i, delay := range t.delays {
		if position < delay {
			return i, delay - position
		}
		position -= delay
	}
	return 0, 0
}

type gifStep struct{ time, delay int }

func gifTimeline(logo, badge gifTrack) []gifStep {
	duration := max(logo.duration, badge.duration)
	if logo.duration > 0 && badge.duration > 0 {
		a, b := logo.duration, badge.duration
		for b != 0 {
			a, b = b, a%b
		}
		// 正常短动画用共同周期无缝循环；共同周期超过一分钟时用较长原动画周期，
		// 避免两个互质的帧延时把一张小动图放大成几小时。
		joint := int64(logo.duration/a) * int64(badge.duration)
		if joint <= 6000 {
			duration = int(joint)
		}
	}
	// GIF 单帧延时只有 16 位，同时约束超长输入的输出时长。
	duration = min(duration, gifMaxFrames*65535)
	var steps []gifStep
	for time := 0; time < duration; {
		delay := duration - time
		for _, track := range []gifTrack{logo, badge} {
			_, remaining := track.at(time)
			if remaining > 0 {
				delay = min(delay, remaining)
			}
		}
		steps = append(steps, gifStep{time, delay})
		time += delay
		if len(steps) > gifMaxFrames {
			steps = sampleGIFTimeline(logo, badge, duration)
			break
		}
	}
	// 两张动图的切帧点可能只差 1/100 秒，浏览器会把这种过短延时拉长。
	// 合并这些相邻时段，维持总播放时长且保证输出延时至少为 2。
	merged := steps[:0]
	for _, step := range steps {
		if len(merged) > 0 && merged[len(merged)-1].delay < 2 {
			previous := &merged[len(merged)-1]
			take := min(2-previous.delay, step.delay)
			previous.delay += take
			step.time += take
			step.delay -= take
		}
		if step.delay > 0 {
			merged = append(merged, step)
		}
	}
	if len(merged) > 1 && merged[len(merged)-1].delay < 2 {
		merged[len(merged)-2].delay += merged[len(merged)-1].delay
		merged = merged[:len(merged)-1]
	}
	return merged
}

// sampleGIFTimeline keeps equal output intervals, but chooses a representative
// source time within each interval. Prefer underrepresented frames in both
// tracks so a fast animation cannot disappear through periodic sampling.
func sampleGIFTimeline(logo, badge gifTrack, duration int) []gifStep {
	tracks := []gifTrack{logo, badge}
	counts := [][]int{make([]int, len(logo.frames)), make([]int, len(badge.frames))}
	steps := make([]gifStep, 0, gifMaxFrames)
	for i := 0; i < gifMaxFrames; i++ {
		start := i * duration / gifMaxFrames
		end := (i + 1) * duration / gifMaxFrames
		candidates := []int{start}
		for _, track := range tracks {
			// One cycle visits each source frame; later cycles repeat candidates.
			for time, n := start, 0; n < len(track.frames); n++ {
				_, remaining := track.frameAt(time)
				if remaining == 0 || time+remaining >= end {
					break
				}
				time += remaining
				candidates = append(candidates, time)
			}
		}
		best, bestScore := start, -1
		for _, time := range candidates {
			score := 0
			for j, track := range tracks {
				if frame, _ := track.frameAt(time); frame >= 0 && track.duration > 0 {
					score += 1000000 / (counts[j][frame] + 1)
				}
			}
			if score > bestScore {
				best, bestScore = time, score
			}
		}
		for j, track := range tracks {
			if frame, _ := track.frameAt(best); frame >= 0 {
				counts[j][frame]++
			}
		}
		steps = append(steps, gifStep{best, end - start})
	}
	return steps
}

// gifDelay 取第 i 帧延时（1/100 秒），缺省 10，且不小于 2 以防部分解码器加速。
func gifDelay(delays []int, i int) int {
	d := 10
	if i < len(delays) && delays[i] > 0 {
		d = delays[i]
	}
	if d < 2 {
		d = 2
	}
	return d
}

// palCache 缓存 RGB → 调色板下标。码图里绝大多数像素只有少数几种颜色，
// 缓存后 600×600 的量化从 ~600ms 降到十几毫秒。
type palCache map[uint32]uint8

func (c palCache) index(p color.Palette, r, g, b uint8) uint8 {
	key := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	if idx, ok := c[key]; ok {
		return idx
	}
	idx := uint8(p.Index(color.RGBA{R: r, G: g, B: b, A: 255}))
	c[key] = idx
	return idx
}

// toPaletted 转成 256 色调色板图。用最近色而不是抖动：
// 码点是纯黑白块，抖动会引入噪点、拉低扫描容错。
func toPaletted(src *image.RGBA) *image.Paletted {
	return toPalettedShared(src, palCache{})
}

// toPalettedShared 复用颜色缓存（动图多帧共用一份，越往后越快）。
func toPalettedShared(src *image.RGBA, cache palCache) *image.Paletted {
	p := image.NewPaletted(src.Bounds(), palette.Plan9)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	for y := 0; y < h; y++ {
		srow := src.Pix[y*src.Stride:]
		drow := p.Pix[y*p.Stride:]
		for x := 0; x < w; x++ {
			s := srow[x*4:]
			drow[x] = cache.index(p.Palette, s[0], s[1], s[2])
		}
	}
	return p
}

// cloneRGBA 深拷贝位图（GIF 逐帧合成用）。
func CloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}
