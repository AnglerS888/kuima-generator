package service

import (
	"context"
	"errors"
	"fmt"
	"image"

	"wxcode/internal/qrcode"
)

// autoDecodeResult 是对外统一的响应，字段覆盖两种码型。
type AutoDecodeResult struct {
	Family      string            `json:"family"`
	Version     int               `json:"version"`
	ECLevel     string            `json:"ecLevel"`
	Mask        int               `json:"mask"`
	Typebits    int               `json:"typebits,omitempty"`
	Mode        string            `json:"mode"`
	Count       int               `json:"count,omitempty"`
	QRText      string            `json:"qrText,omitempty"`
	Payload     string            `json:"payload"`
	Text        string            `json:"text,omitempty"`
	URL         string            `json:"url,omitempty"`
	Modules     int               `json:"modules,omitempty"`
	Corrections int               `json:"corrections"`
	Box         *qrcode.DecodeBox `json:"box,omitempty"`
}

// decodeAutoImage 按偏好顺序尝试两种码型。图像只解码一次，两个算法共享它。
func DecodeAutoImage(ctx context.Context, img image.Image, preferQR bool) (*AutoDecodeResult, error) {
	type decoder struct {
		name string
		fn   func(context.Context, image.Image) (*AutoDecodeResult, error)
	}
	wx := decoder{"WX_CODE", decodeWXAuto}
	qr := decoder{"QR_CODE", decodeQRAuto}
	order := []decoder{wx, qr}
	if preferQR {
		order = []decoder{qr, wx}
	}
	var firstErr error
	for _, d := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := d.fn(ctx, img)
		if err == nil {
			return res, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = errors.New("没有可识别的码")
	}
	return nil, fmt.Errorf("未识别到太阳码或普通二维码: %w", firstErr)
}

func decodeWXAuto(ctx context.Context, img image.Image) (*AutoDecodeResult, error) {
	res, err := qrcode.DecodeImageContext(ctx, img)
	if err != nil {
		return nil, err
	}
	return &AutoDecodeResult{
		Family:      "WX_CODE",
		Version:     res.Version,
		ECLevel:     res.ECLevel,
		Mask:        res.Mask,
		Typebits:    res.Typebits,
		Mode:        res.Mode,
		Count:       res.Count,
		QRText:      res.QRText,
		Payload:     res.Payload,
		Corrections: res.Corrections,
		Box:         res.Box,
	}, nil
}

func decodeQRAuto(ctx context.Context, img image.Image) (*AutoDecodeResult, error) {
	res, err := qrcode.DecodeQRImageContext(ctx, img)
	if err != nil {
		return nil, err
	}
	payload := res.URL
	if payload == "" {
		payload = res.Text
	}
	return &AutoDecodeResult{
		Family:      "QR_CODE",
		Version:     res.Version,
		ECLevel:     res.ECLevel,
		Mask:        res.Mask,
		Mode:        res.Mode,
		Payload:     payload,
		Text:        res.Text,
		URL:         res.URL,
		Modules:     res.Modules,
		Corrections: res.Corrections,
	}, nil
}

// decodeResult 把统一结果转成记录层使用的结构，保证两种码型走同一套去重逻辑。
func (r *AutoDecodeResult) DecodeResult() *qrcode.DecodeResult {
	if r == nil {
		return nil
	}
	return &qrcode.DecodeResult{
		Family:      r.Family,
		Version:     r.Version,
		ECLevel:     r.ECLevel,
		Mask:        r.Mask,
		Typebits:    r.Typebits,
		Mode:        r.Mode,
		Count:       r.Count,
		QRText:      r.QRText,
		Payload:     r.Payload,
		Corrections: r.Corrections,
		Box:         r.Box,
	}
}
