package api

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFillBadgeRequestFormats(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
	}{
		{"json", "application/json", `{"text":"https://example.com/","badge":"none","fillBadge":true}`},
		{"form", "application/x-www-form-urlencoded", "text=https%3A%2F%2Fexample.com%2F&badge=none&fillBadge=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/encode", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			req, err := readEncodeReq(r, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !req.FillBadge || req.Badge != "none" || !renderOptions(req, nil, nil).FillBadge || !svgOptions(req, nil, "none").FillBadge {
				t.Fatal("fillBadge was not passed through the request and render options")
			}
		})
	}
	for _, value := range []string{"true", "1", "false", "0", ""} {
		req := &encodeReq{}
		fillFromValues(req, url.Values{"fillBadge": {value}})
		if req.FillBadge != (value == "true" || value == "1") {
			t.Fatalf("query fillBadge=%q parsed incorrectly", value)
		}
	}
}
