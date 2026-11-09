package netutil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// 安全策略：出站请求防 SSRF、图片解码防炸弹、接口限速防 DoS、统一安全响应头。
const maxRemoteRedirects = 3

const remoteDialTimeout = 5 * time.Second

// errPrivateAddr 表示目标解析到内网/回环/链路本地等不该被访问的地址。
var ErrPrivateAddr = errors.New("拒绝访问内网 / 回环地址")

// isPublicIP 判断 IP 是否属于可以安全访问的公网地址。
// 注意要在 DNS 解析后的实际连接地址上判断（Dialer.Control），这样 DNS rebinding 也绕不过去。
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// 部分云厂商元数据 / 保留网段
	for _, b := range blockedCIDRs {
		if b.Contains(ip) {
			return false
		}
	}
	return true
}

var blockedCIDRs = func() []*net.IPNet {
	// 这些网段既不是公网，也常被用于探测内网服务或云元数据
	list := []string{
		"0.0.0.0/8",       // 本网络
		"100.64.0.0/10",   // CGNAT
		"169.254.0.0/16",  // 链路本地（含 169.254.169.254 云元数据）
		"192.0.0.0/24",    // IETF 协议分配
		"192.0.2.0/24",    // 文档用
		"198.18.0.0/15",   // 基准测试
		"198.51.100.0/24", // 文档用
		"203.0.113.0/24",  // 文档用
		"240.0.0.0/4",     // 保留
		"::1/128",         // 回环
		"fc00::/7",        // 唯一本地地址
		"fe80::/10",       // 链路本地
		"ff00::/8",        // 组播
		"64:ff9b::/96",    // NAT64
		"2001:db8::/32",   // 文档用
	}
	out := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// safeDialControl 在实际建立连接前检查目标 IP，拦住内网地址。
func safeDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrPrivateAddr
	}
	ip := net.ParseIP(host)
	if !IsPublicIP(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateAddr, host)
	}
	return nil
}

// remoteHTTPClient 只用于下载用户给的图片：不走环境代理、限制跳转次数、禁止内网地址。
var RemoteHTTPClient = &http.Client{
	Timeout: remoteDialTimeout + 5*time.Second,
	Transport: &http.Transport{
		Proxy:                 nil, // 不吃 HTTP_PROXY：否则内网判断会被代理绕开
		DialContext:           newSafeDialer(),
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableKeepAlives:     false,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRemoteRedirects {
			return errors.New("重定向次数过多")
		}
		return ValidateRemoteURL(req.URL.String())
	},
}

// validateRemoteURL 只放行 http/https，且不允许带 userinfo（防 http://trusted@internal/ 这类绕过）。
func ValidateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("只支持 http / https 图片地址")
	}
	if u.Host == "" {
		return errors.New("图片地址缺少主机名")
	}
	if u.User != nil {
		return errors.New("图片地址不允许携带用户名密码")
	}
	return nil
}

const MaxImagePixels = 40 << 20 // 总像素上限（4000 万）

const MaxGIFFrames = 120 // 输入动图最多取多少帧（超出只保留前若干帧）

const MaxAnimPixels = 80 << 20 // 动图逐帧合成的总像素预算

const MaxTextLen = 8192 // 单次编码的内容长度上限（字节）

const MaxLogoRatio = 3.0 // logo 半径缩放上限，防画出巨大的圆

const MinLogoRatio = 0.2

const minDecodePixels = 16 // 小于这个尺寸的图没必要解

const RateLimitPerMinute = 120 // 单 IP 每分钟请求数

const authRatePerMinute = 12 // 登录：单 IP / 单账号每分钟尝试次数

const registerRatePerHour = 20 // 注册：单 IP 每小时成功前的提交次数

const MaxConcurrentHeavy = 8 // 同时进行的重计算（编码/解码）上限

// clientIP 取真实客户端 IP。
//
// 默认策略（TRUST_PROXY 未设置）= 自动判断：
//   - 直连对端是公网地址 → 只认 RemoteAddr，客户端伪造 X-Forwarded-For 也没用；
//   - 直连对端是内网/回环（本机 Nginx、Docker 网桥、云负载均衡）→ 采信转发头，
//     否则记录和限速都会把整个站点算成一个「代理 IP」。
//
// 想强制某种行为：
//
//	TRUST_PROXY=0  永远只用 RemoteAddr（前面没有代理时最安全）
//	TRUST_PROXY=1  永远采信转发头（代理不在内网段时用）
func ClientIP(r *http.Request) string {
	peer := hostOnly(r.RemoteAddr)
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRUST_PROXY"))) {
	case "0", "false", "no", "off":
		return peer
	case "1", "true", "yes", "on":
		if ip := ForwardedIP(r); ip != "" {
			return ip
		}
		return peer
	}
	if p := net.ParseIP(peer); p != nil && !IsPublicIP(p) {
		if ip := ForwardedIP(r); ip != "" {
			return ip
		}
	}
	return peer
}

// hostOnly 去掉端口，只留主机部分。
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// forwardedIP 从 X-Forwarded-For 里取真实客户端。
//
// 取法是从右往左跳过内网/回环地址：代理（Nginx）通常把真实 IP 追加在链尾，
// 而客户端自己塞进来的伪造值在链头，跳过内网段后得到的就是真实来源。
// 整条链都是内网（公司内网互访）时退回最左侧，再不行才用 X-Real-IP。
func ForwardedIP(r *http.Request) string {
	var chain []string
	for _, part := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			chain = append(chain, ip.String())
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if ip := net.ParseIP(chain[i]); ip != nil && IsPublicIP(ip) {
			return chain[i]
		}
	}
	if len(chain) > 0 {
		return chain[0]
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip.String()
		}
	}
	return ""
}

// newSafeDialer 返回带内网校验的拨号函数（DNS 解析后、真正连接前检查目标 IP）。
func newSafeDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   remoteDialTimeout,
		KeepAlive: 30 * time.Second,
		Control:   safeDialControl,
	}
	return d.DialContext
}
