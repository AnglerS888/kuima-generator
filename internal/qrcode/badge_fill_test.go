package qrcode

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"regexp"
	"testing"
	"time"
)

func TestBadgeFillPreservesCodewords(t *testing.T) {
	for version := 0; version <= 2; version++ {
		for mask := 0; mask < 32; mask++ {
			info, err := Encode("https://example.com/", EncodeConfig{Version: version, Mask: mask})
			if err != nil {
				t.Fatal(err)
			}
			original := info.Grid
			if badgeFilledGrid(info, false) != original {
				t.Fatal("disabled fill changed the grid")
			}
			grid := badgeFilledGrid(info, true)
			added := 0
			for row := range grid {
				for col := range grid[row] {
					if grid[row][col] == original[row][col] {
						continue
					}
					if row < 42 || row > 48 || col < 7 || original[row][col] != 0 ||
						(placeTable[version][row]|ringTable[row])&(1<<uint(col)) != 0 {
						t.Fatalf("v%d mask %d modified a protected cell (%d,%d)", version, mask, row, col)
					}
					added++
				}
			}
			if added == 0 {
				t.Fatalf("v%d mask %d did not fill any cells", version, mask)
			}
			if info.Grid != original {
				t.Fatal("fill mutated the encoded grid")
			}
			read := func(g [72][16]byte) []byte {
				return wxCodewords(func(row, col int) byte { return g[row][col] }, version, mask)
			}
			if !bytes.Equal(read(grid), read(original)) {
				t.Fatalf("v%d mask %d changed sampled codewords", version, mask)
			}
		}
	}
}

func TestBadgeFillHandRandomKeepsOtherColors(t *testing.T) {
	info, err := Encode("https://example.com/", DefaultEncodeConfig())
	if err != nil {
		t.Fatal(err)
	}
	opt := RenderOptions{Size: 600, Style: "hand", ColorMode: "random"}
	before := RenderImage(info, opt)
	opt.FillBadge = true
	after := RenderImage(info, opt)
	changed := 0
	for y := 0; y < 600; y++ {
		for x := 0; x < 600; x++ {
			if before.At(x, y) != after.At(x, y) {
				changed++
				if x < 400 || y < 400 {
					t.Fatalf("fill changed a pixel outside the badge corner at (%d,%d)", x, y)
				}
			}
		}
	}
	if changed == 0 {
		t.Fatal("fill did not change the image")
	}

	svgOpt := SVGOptions{BadgeHref: "none", Style: "hand", ColorMode: "random"}
	plainSVG := RenderSVG(info, svgOpt)
	svgOpt.FillBadge = true
	filledSVG := RenderSVG(info, svgOpt)
	paths := regexp.MustCompile(`<path d="([^"]+)" stroke="([^"]+)"`)
	colors := make(map[string]string)
	for _, match := range paths.FindAllStringSubmatch(plainSVG, -1) {
		colors[match[1]] = match[2]
	}
	shared := 0
	for _, match := range paths.FindAllStringSubmatch(filledSVG, -1) {
		if color, ok := colors[match[1]]; ok {
			shared++
			if color != match[2] {
				t.Fatalf("unchanged SVG path changed color from %s to %s", color, match[2])
			}
		}
	}
	if shared < 100 {
		t.Fatalf("only %d SVG paths were compared", shared)
	}
}

func TestBadgeFillVariesWithContent(t *testing.T) {
	for version := 0; version <= 2; version++ {
		seen := make(map[[72][16]byte]bool)
		for i := 0; i < 64; i++ {
			info, err := Encode(fmt.Sprintf("https://example.com/%d", i), EncodeConfig{Version: version, Mask: 14})
			if err != nil {
				t.Fatal(err)
			}
			filled := badgeFilledGrid(info, true)
			if filled != badgeFilledGrid(info, true) {
				t.Fatal("same content produced different fill")
			}
			// 经典笔画会把连续格位连成线，真正的点必须是单格游程。
			// 统计最终网格，确保它们没有与原有码点或外缘延伸合并。
			dots := 0
			for _, run := range gridRuns(filled) {
				if run.Row >= 42 && run.Row <= 48 && run.Col1 >= 7 && run.Col1 == run.Col2 && info.Grid[run.Row][run.Col1] == 0 {
					dots++
				}
			}
			if dots < 2 {
				t.Fatalf("v%d URL %d: fill contains only %d independent dots", version, i, dots)
			}
			// 只比较新增的纹理，排除 URL 本身导致的数据码点变化。
			var decoration [72][16]byte
			for row := range filled {
				for col := range filled[row] {
					decoration[row][col] = filled[row][col] ^ info.Grid[row][col]
				}
			}
			seen[decoration] = true
		}
		if len(seen) < 32 {
			t.Fatalf("v%d: 64 URLs produced only %d fill patterns", version, len(seen))
		}
	}
}

func TestBadgeFillRenderRoundTrip(t *testing.T) {
	for version := 0; version <= 2; version++ {
		for _, style := range StyleNames {
			t.Run(fmt.Sprintf("v%d/%s", version, style), func(t *testing.T) {
				info, err := Encode("https://example.com/", EncodeConfig{Version: version, Mask: -1})
				if err != nil {
					t.Fatal(err)
				}
				img := RenderImage(info, RenderOptions{Size: 600, Style: style, FillBadge: true})
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				got, err := DecodeImageContext(ctx, img)
				if err != nil {
					t.Fatal(err)
				}
				if got.Payload != info.Payload || got.Corrections != 0 {
					t.Fatalf("decoded %q with %d corrections", got.Payload, got.Corrections)
				}
			})
		}
	}
}

func TestBadgeFillOnlyWhenHidden(t *testing.T) {
	info, err := Encode("https://example.com/", DefaultEncodeConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, badge := range []string{"default", "none"} {
		t.Run(badge, func(t *testing.T) {
			var img image.Image
			if badge == "default" {
				img = image.NewRGBA(image.Rect(0, 0, 10, 10))
			}
			plain, err := RenderPNG(info, RenderOptions{Size: 300, Badge: img})
			if err != nil {
				t.Fatal(err)
			}
			filled, err := RenderPNG(info, RenderOptions{Size: 300, Badge: img, FillBadge: true})
			if err != nil {
				t.Fatal(err)
			}
			plainSVG := RenderSVG(info, SVGOptions{BadgeHref: badge})
			filledSVG := RenderSVG(info, SVGOptions{BadgeHref: badge, FillBadge: true})
			wantSame := badge == "default"
			if bytes.Equal(plain, filled) != wantSame || (plainSVG == filledSVG) != wantSame {
				t.Fatal("fill did not follow badge visibility in both renderers")
			}
			// Pixel differences must stay in the reserved corner in the raster output too.
			before, _ := png.Decode(bytes.NewReader(plain))
			after, _ := png.Decode(bytes.NewReader(filled))
			for y := 0; y < 300; y++ {
				for x := 0; x < 300; x++ {
					if before.At(x, y) != after.At(x, y) && (x < 200 || y < 200) {
						t.Fatalf("fill changed a pixel outside the corner (%d,%d)", x, y)
					}
				}
			}
		})
	}
}
