package qrcode

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"reflect"
	"testing"
)

func gifTestFrame(c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

func TestRenderGIFBadgeAndLogoTimings(t *testing.T) {
	info, err := Encode("https://example.com/", DefaultEncodeConfig())
	if err != nil {
		t.Fatal(err)
	}
	red := gifTestFrame(color.RGBA{R: 255, A: 255})
	blue := gifTestFrame(color.RGBA{B: 255, A: 255})
	clear := gifTestFrame(color.Transparent)
	for _, style := range []string{StyleClassic, StyleHand} {
		t.Run(style, func(t *testing.T) {
			opt := RenderOptions{
				Size: 180, Margin: 5, Style: style, FillBadge: true,
				Logo: red, LogoFrames: []image.Image{red, clear}, LogoDelays: []int{10, 20},
				Badge: blue, BadgeFrames: []image.Image{blue, clear}, BadgeDelays: []int{20, 10},
			}
			raw, err := RenderGIF(info, opt)
			if err != nil {
				t.Fatal(err)
			}
			out, err := gif.DecodeAll(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if out.LoopCount != 0 || !reflect.DeepEqual(out.Delay, []int{10, 10, 10}) {
				t.Fatalf("wrong animation timing: loop=%d delays=%v", out.LoopCount, out.Delay)
			}
			// 独立静态渲染每个时刻，检查透明帧没有残影、填补纹理没有露在徽标下。
			logos := []image.Image{red, clear, clear}
			badges := []image.Image{blue, blue, clear}
			for i, frame := range out.Image {
				still := opt
				still.Logo, still.Badge = logos[i], badges[i]
				want := toPaletted(RenderImage(info, still))
				if !bytes.Equal(frame.Pix, want.Pix) {
					t.Fatalf("frame %d differs from independent rendering (stale pixels or missing layer)", i)
				}
			}
		})
	}
}

func TestRenderGIFAnimatedBadgeWithoutAnimatedLogo(t *testing.T) {
	info, err := Encode("https://example.com/", DefaultEncodeConfig())
	if err != nil {
		t.Fatal(err)
	}
	red := gifTestFrame(color.RGBA{R: 255, A: 255})
	blue := gifTestFrame(color.RGBA{B: 255, A: 255})
	opt := RenderOptions{Size: 120, Logo: red, Badge: red,
		BadgeFrames: []image.Image{red, blue}, BadgeDelays: []int{7, 13}}
	for _, ghost := range []bool{false, true} {
		if ghost {
			opt.ColorMode = ColorGhost
			opt.FillBadge = true
		}
		raw, err := RenderGIF(info, opt)
		if err != nil {
			t.Fatal(err)
		}
		out, err := gif.DecodeAll(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if ghost {
			if len(out.Image) != 1 {
				t.Fatal("hidden badge still triggered animation")
			}
			if !bytes.Equal(out.Image[0].Pix, toPaletted(RenderImage(info, opt)).Pix) {
				t.Fatal("ghost rendering or badge fill changed")
			}
		} else {
			if !reflect.DeepEqual(out.Delay, []int{7, 13}) {
				t.Fatalf("badge delays lost: %v", out.Delay)
			}
			if bytes.Equal(out.Image[0].Pix, out.Image[1].Pix) {
				t.Fatal("badge did not animate")
			}
			if out.Image[0].At(60, 60) != out.Image[1].At(60, 60) {
				t.Fatal("static center logo changed")
			}
		}
	}
}

func TestGIFTimelineLoopsAndSamplesWholeCycle(t *testing.T) {
	frame := gifTestFrame(color.Black)
	logo := newGIFTrack([]image.Image{frame, frame}, []int{10, 20}, nil)
	badge := newGIFTrack([]image.Image{frame, frame}, []int{10, 10}, nil)
	want := []gifStep{{0, 10}, {10, 10}, {20, 10}, {30, 10}, {40, 10}, {50, 10}}
	if steps := gifTimeline(logo, badge); !reflect.DeepEqual(steps, want) {
		t.Fatalf("different animation periods did not loop independently: %v", steps)
	}
	shortLogo := newGIFTrack([]image.Image{frame, frame}, []int{2, 3}, nil)
	shortBadge := newGIFTrack([]image.Image{frame, frame}, []int{3, 2}, nil)
	shortTotal := 0
	for _, step := range gifTimeline(shortLogo, shortBadge) {
		if step.delay < 2 {
			t.Fatalf("merged timeline introduced a delay browsers would stretch: %v", step)
		}
		shortTotal += step.delay
	}
	if shortTotal != 5 {
		t.Fatal("merging short intervals changed the total duration")
	}
	frames := make([]image.Image, 25)
	delays := make([]int, len(frames))
	for i := range frames {
		frames[i], delays[i] = frame, 5
	}
	steps := gifTimeline(gifTrack{}, newGIFTrack(frames, delays, nil))
	total := 0
	for _, step := range steps {
		total += step.delay
	}
	if len(steps) != gifMaxFrames || total != 125 || steps[len(steps)-1].time < 100 {
		t.Fatalf("sampling truncated the animation: %v", steps)
	}
}

func TestRenderGIFSamplingPreservesFastAndSlowAnimations(t *testing.T) {
	info, err := Encode("https://example.com/", DefaultEncodeConfig())
	if err != nil {
		t.Fatal(err)
	}
	red := gifTestFrame(color.RGBA{R: 255, A: 255})
	blue := gifTestFrame(color.RGBA{B: 255, A: 255})
	opt := RenderOptions{Size: 180,
		LogoFrames: []image.Image{red, blue}, LogoDelays: []int{2, 2},
		BadgeFrames: []image.Image{red, blue}, BadgeDelays: []int{200, 200},
	}
	raw, err := RenderGIF(info, opt)
	if err != nil {
		t.Fatal(err)
	}
	out, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	center, corner := map[color.Color]bool{}, map[color.Color]bool{}
	total := 0
	for i, frame := range out.Image {
		center[frame.At(90, 90)] = true
		corner[frame.At(144, 144)] = true
		total += out.Delay[i]
	}
	if len(out.Image) != gifMaxFrames || total != 400 || len(center) < 2 || len(corner) < 2 {
		t.Fatalf("sampling lost animation: frames=%d duration=%d logo colors=%d badge colors=%d", len(out.Image), total, len(center), len(corner))
	}
}
