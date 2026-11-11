package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"net/http/httptest"
	"strings"
	"testing"

	"wxcode/internal/imagex"
)

func TestCustomGIFBadgeAPI(t *testing.T) {
	palette := color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 8, 8), palette)
	second := image.NewPaletted(first.Bounds(), palette)
	for i := range first.Pix {
		first.Pix[i], second.Pix[i] = 1, 2
	}
	var source bytes.Buffer
	if err := gif.EncodeAll(&source, &gif.GIF{
		Image: []*image.Paletted{first, second}, Delay: []int{7, 13}, LoopCount: 0,
	}); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(source.Bytes())
	ref, href, err := imagex.ResolveBadgeRef(uri)
	if err != nil || href != uri {
		t.Fatalf("GIF badge resolution: href matches=%v err=%v", href == uri, err)
	}
	if animated, frames := imagex.AnimInfo(ref); !animated || frames != 2 {
		t.Fatalf("badge animation was discarded: %v %d", animated, frames)
	}
	for _, format := range []string{"gif", "png", "svg", "json"} {
		t.Run(format, func(t *testing.T) {
			req := &encodeReq{Text: "https://example.com/", Format: format, Size: 120, Mask: -1, Badge: uri}
			req.normalize()
			response := httptest.NewRecorder()
			_, _, ok := serveCode(response, httptest.NewRequest("POST", "/api/encode", nil), req)
			if !ok || response.Code != 200 {
				t.Fatalf("render failed: %d %s", response.Code, response.Body.String())
			}
			switch format {
			case "gif":
				out, err := gif.DecodeAll(bytes.NewReader(response.Body.Bytes()))
				if err != nil || len(out.Image) != 2 || out.Delay[0] != 7 || out.Delay[1] != 13 {
					t.Fatalf("GIF animation missing: %v %v", out, err)
				}
			case "svg":
				if !strings.Contains(response.Body.String(), uri) {
					t.Fatal("SVG did not embed original GIF")
				}
			case "json":
				var meta runtimeMeta
				if err := json.Unmarshal(response.Body.Bytes(), &meta); err != nil || !meta.BadgeAnimated || meta.BadgeFrames != 2 {
					t.Fatalf("badge animation metadata missing: %+v %v", meta, err)
				}
			}
		})
	}
}

func TestEncodeRequestAcceptsTwoMaximumImages(t *testing.T) {
	// 两张 8MB 图片转为 base64 后会超过原来的 16MB 请求体限制。
	spec := "data:image/png;base64," + strings.Repeat("A", (imagex.MaxImageBytes+2)/3*4)
	body, err := json.Marshal(encodeReq{Text: "https://example.com/", Logo: spec, Badge: spec})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/encode", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	req, err := readEncodeReq(r, imagex.MaxImageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if req.Logo != spec || req.Badge != spec {
		t.Fatal("one of the two image parameters was lost")
	}
}
