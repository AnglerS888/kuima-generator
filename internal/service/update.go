package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/logger"
)

// ============================================================
// 在线更新：检查远端 VERSION → 下载发布产物 → 替换自身 → 重启
//
// 版本号只有一个来源：仓库根目录的 VERSION 文件（Gitee / GitHub 内容同步）。
// 产物按固定命名放在 Release 里：wxcode-<goos>-<goarch>[.exe]，
// 例如 wxcode-linux-amd64、wxcode-windows-amd64.exe。
// ============================================================

const (
	updateCacheTTL   = 5 * time.Minute
	updateHTTPTime   = 8 * time.Second
	updateMinSize    = 1 << 20 // 产物至少 1MB，挡住错误页/空文件
	updateMaxSize    = 200 << 20
	updateMaxVersion = 64
)

// UpdateInfo 是「检查更新」的结果。
type UpdateInfo struct {
	Current     string `json:"current"`          // 当前版本（构建时注入）
	Latest      string `json:"latest"`           // 远端 VERSION
	HasUpdate   bool   `json:"hasUpdate"`        // 是否有新版本
	Source      string `json:"source,omitempty"` // gitee / github / mirror
	CheckedAt   string `json:"checkedAt"`        // 检查时间
	InContainer bool   `json:"inContainer"`      // 是否跑在容器里（更新不持久）
	Asset       string `json:"asset,omitempty"`  // 预期产物名
	Notes       string `json:"notes,omitempty"`  // 附加说明
	Error       string `json:"error,omitempty"`  // 拉取失败原因（有值时其它字段仍返回）
}

// ApplyResult 是「执行更新」的结果。
type ApplyResult struct {
	Version     string `json:"version"`
	Asset       string `json:"asset"`
	Restart     bool   `json:"restart"`
	InContainer bool   `json:"inContainer"`
}

var (
	updateMu    sync.Mutex
	updateCache *UpdateInfo
	updateAt    time.Time

	// shutdownFn 由 main 注入：重启前先释放端口（Windows 必须先停监听才能起新进程）。
	shutdownFn func(context.Context) error
)

// SetShutdownFunc 注入优雅关闭函数（main 里接 srv.Shutdown）。
func SetShutdownFunc(fn func(context.Context) error) { shutdownFn = fn }

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func updateGiteeRepo() string  { return envOr("UPDATE_GITEE_REPO", "anglers/kuima-generator") }
func updateGithubRepo() string { return envOr("UPDATE_GITHUB_REPO", "AnglerS888/kuima-generator") }
func updateBranch() string     { return envOr("UPDATE_BRANCH", "main") }
func updateMirror() string     { return strings.TrimSpace(os.Getenv("UPDATE_MIRROR")) }

// InContainer 报告当前是否运行在容器里。
//
// 容器内替换二进制只对「本次容器生命周期」有效，重建容器就回到镜像版本，
// 所以界面上要提示用户这个前提。
func InContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil { // podman
		return true
	}
	return false
}

// AssetName 返回当前平台的发布产物文件名。
func AssetName() string {
	name := fmt.Sprintf("wxcode-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// CheckUpdate 检查是否有新版本（带 5 分钟缓存；force=true 跳过缓存）。
func CheckUpdate(ctx context.Context, force bool) UpdateInfo {
	updateMu.Lock()
	defer updateMu.Unlock()
	if !force && updateCache != nil && time.Since(updateAt) < updateCacheTTL {
		return *updateCache
	}

	info := UpdateInfo{
		Current:     config.Version,
		Asset:       AssetName(),
		CheckedAt:   time.Now().Format(time.RFC3339),
		InContainer: InContainer(),
	}
	latest, source, err := fetchLatestVersion(ctx)
	if err != nil {
		info.Error = err.Error()
		// 失败也缓存一小会儿，避免用户连点把远端打爆；下次成功会自动覆盖
		updateCache, updateAt = &info, time.Now()
		return info
	}
	info.Latest, info.Source = latest, source
	info.HasUpdate = versionLess(info.Current, latest)
	if info.InContainer {
		info.Notes = "当前运行在容器内：替换只对本次容器生效，重建容器会回到镜像版本。"
	}
	updateCache, updateAt = &info, time.Now()
	return info
}

// fetchLatestVersion 依次尝试 Gitee → GitHub → 镜像，返回第一个能读到的版本号。
// 设了 UPDATE_VERSION_URL 时只用它（自建更新源 / 内网离线环境）。
func fetchLatestVersion(ctx context.Context) (string, string, error) {
	if u := strings.TrimSpace(os.Getenv("UPDATE_VERSION_URL")); u != "" {
		v, err := fetchText(ctx, u)
		if err != nil {
			return "", "", err
		}
		v = normalizeVersion(v)
		if v == "" {
			return "", "", errors.New("更新源返回的版本号为空")
		}
		return v, "custom", nil
	}

	branch := updateBranch()
	githubRaw := "https://raw.githubusercontent.com/" + updateGithubRepo() + "/" + branch + "/VERSION"
	type source struct {
		name string
		url  string
	}
	list := []source{
		{"gitee", "https://gitee.com/" + updateGiteeRepo() + "/raw/" + branch + "/VERSION"},
		{"github", githubRaw},
	}
	if m := updateMirror(); m != "" {
		list = append(list, source{"mirror", strings.TrimRight(m, "/") + "/" + githubRaw})
	}

	var lastErr error
	for _, s := range list {
		v, err := fetchText(ctx, s.url)
		if err != nil {
			lastErr = err
			continue
		}
		v = normalizeVersion(v)
		if v == "" {
			lastErr = fmt.Errorf("%s 返回的版本号为空", s.name)
			continue
		}
		return v, s.name, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的版本来源")
	}
	return "", "", lastErr
}

// fetchText 读一个小文本文件（VERSION 只有十几字节）。
func fetchText(ctx context.Context, url string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, updateHTTPTime)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := updateClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

var updateClient = &http.Client{Timeout: updateHTTPTime}

// normalizeVersion 去掉 BOM / 空白 / 前缀 v，只保留一行。
func normalizeVersion(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if len(s) > updateMaxVersion {
		s = s[:updateMaxVersion]
	}
	return s
}

// versionLess 比较版本号：current < latest 时返回 true。
//
// 只按数字段比较（1.0.10 > 1.0.9）；非数字段按字符串兜底，
// 当前版本不是数字（dev / 未知）时只要远端有值就认为可更新。
func versionLess(current, latest string) bool {
	cur, lat := normalizeVersion(current), normalizeVersion(latest)
	if lat == "" || cur == lat {
		return false
	}
	cn, cok := versionParts(cur)
	ln, lok := versionParts(lat)
	if !cok || !lok {
		return cur != lat // 非数字版本：只要不相同就提示
	}
	for i := 0; i < len(cn) || i < len(ln); i++ {
		a, b := 0, 0
		if i < len(cn) {
			a = cn[i]
		}
		if i < len(ln) {
			b = ln[i]
		}
		if a != b {
			return a < b
		}
	}
	return false
}

func versionParts(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		// 允许 1.2.3-beta 这种后缀，只取前面的数字
		digits := f
		for i, r := range f {
			if r < '0' || r > '9' {
				digits = f[:i]
				break
			}
		}
		if digits == "" {
			return nil, false
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// assetURLs 列出发布产物的候选下载地址（GitHub 官方 + 镜像；可用 UPDATE_ASSET_BASE 覆盖）。
func assetURLs() []string {
	asset := AssetName()
	if base := strings.TrimSpace(os.Getenv("UPDATE_ASSET_BASE")); base != "" {
		return []string{strings.TrimRight(base, "/") + "/" + asset}
	}
	gh := "https://github.com/" + updateGithubRepo() + "/releases/latest/download/" + asset
	urls := []string{gh}
	if m := updateMirror(); m != "" {
		urls = append(urls, strings.TrimRight(m, "/")+"/"+gh)
	}
	return urls
}

// ApplyUpdate 下载并替换当前可执行文件，成功后调用方负责重启。
func ApplyUpdate(ctx context.Context) (ApplyResult, error) {
	info := CheckUpdate(ctx, true)
	if info.Error != "" && info.Latest == "" {
		return ApplyResult{}, fmt.Errorf("检查更新失败：%s", info.Error)
	}
	if !info.HasUpdate {
		return ApplyResult{}, fmt.Errorf("已经是最高版本（v%s）", info.Current)
	}

	exe, err := os.Executable()
	if err != nil {
		return ApplyResult{}, fmt.Errorf("定位当前程序失败：%w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("解析程序路径失败：%w", err)
	}

	var lastErr error
	for _, u := range assetURLs() {
		if err := downloadTo(ctx, u, exe+".new"); err == nil {
			if err := swapBinary(exe); err != nil {
				return ApplyResult{}, err
			}
			return ApplyResult{Version: info.Latest, Asset: AssetName(), Restart: true, InContainer: info.InContainer}, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载地址")
	}
	return ApplyResult{}, fmt.Errorf("下载发布产物失败：%w", lastErr)
}

// downloadTo 下载产物到 dest，并做大小与可执行格式校验。
func downloadTo(ctx context.Context, url, dest string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := updateClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d（%s）", resp.StatusCode, url)
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, updateMaxSize))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dest)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if n < updateMinSize {
		_ = os.Remove(dest)
		return fmt.Errorf("产物过小（%d 字节），可能不是有效程序", n)
	}
	// 粗校验文件头：Linux 是 ELF，Windows 是 MZ
	head := make([]byte, 2)
	if fh, err := os.Open(dest); err == nil {
		_, _ = io.ReadFull(fh, head)
		_ = fh.Close()
	}
	switch runtime.GOOS {
	case "windows":
		if head[0] != 'M' || head[1] != 'Z' {
			_ = os.Remove(dest)
			return errors.New("产物不是 Windows 可执行文件")
		}
	default:
		if head[0] != 0x7f || head[1] != 'E' {
			_ = os.Remove(dest)
			return errors.New("产物不是 Linux 可执行文件")
		}
	}
	return nil
}

// ScheduleRestart 延迟重启：先把 HTTP 响应写回浏览器，再换进程。
func ScheduleRestart(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		if err := RestartSelf(); err != nil {
			logger.Error("自动重启失败，请手动重启服务", "error", err.Error())
		}
	}()
}

// swapBinary 用 exe.new 替换 exe。
//
// 正在运行的程序文件不能直接覆盖，所以先把旧文件改名成 .old 再移入新文件：
// Linux 上运行中的进程仍持有旧 inode，Windows 允许重命名运行中的 exe。
func swapBinary(exe string) error {
	newPath, oldPath := exe+".new", exe+".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		return fmt.Errorf("备份旧程序失败：%w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		// 尽量把旧文件还原，避免程序消失
		_ = os.Rename(oldPath, exe)
		return fmt.Errorf("替换程序失败：%w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(exe, 0o755)
	}
	return nil
}
