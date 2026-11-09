package imagex

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"wxcode/internal/netutil"
)

var errBadImage = errors.New("无法解析图片")

const MaxImageSide = 8000 // 单边像素上限

// checkImageConfig 在真正 Decode 之前用 DecodeConfig 看一下尺寸，避免 30000×30000 的炸弹图。
func checkImageConfig(cfg image.Config) error {
	w, h := cfg.Width, cfg.Height
	if w <= 0 || h <= 0 {
		return errors.New("图片尺寸非法")
	}
	if w > MaxImageSide || h > MaxImageSide {
		return fmt.Errorf("图片单边超过 %d 像素", MaxImageSide)
	}
	if w*h > netutil.MaxImagePixels {
		return fmt.Errorf("图片像素数超过 %d 万", netutil.MaxImagePixels/10000)
	}
	return nil
}

// decodeImageSafe 先校验尺寸再解码：几 KB 的「巨大尺寸」PNG 足以把进程内存打爆，
// 而 image.DecodeConfig 只读头部，成本可以忽略。
func DecodeImageSafe(raw []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errBadImage, err)
	}
	if err := checkImageConfig(cfg); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errBadImage, err)
	}
	return img, nil
}

// heavySem 限制同时进行的重计算，避免几条大图把 CPU 打满。
var HeavySem = make(chan struct{}, netutil.MaxConcurrentHeavy)

// allowHeavy 取一个并发令牌，拿不到就拒绝（不让请求无限排队）。
func AllowHeavy() bool {
	select {
	case HeavySem <- struct{}{}:
		return true
	default:
		return false
	}
}

func ReleaseHeavy() { <-HeavySem }

// ============================================================
// 五、本地文件读取开关
// ============================================================

// allowLocalFile 默认关闭：禁止把 logo / url 参数当成本地文件路径读，
// 否则任何人都能用 /api/encode?logo=/etc/passwd 探测服务器文件系统。
func allowLocalFile() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_LOCAL_IMAGE")))
	return v == "1" || v == "true" || v == "yes"
}
