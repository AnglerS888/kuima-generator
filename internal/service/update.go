package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	updateErrTTL     = 30 * time.Second // 检查失败只缓存一会儿，方便立刻重试
	updateHTTPTime   = 8 * time.Second
	updateMinSize    = 1 << 20 // 产物至少 1MB，挡住错误页/空文件
	updateMaxVersion = 64

	// 自动检查：启动后先等一会儿（避开启动时的其它初始化），之后每 24 小时一次。
	// 想让检查更频繁/更稀疏可用 UPDATE_CHECK_INTERVAL=6h 覆盖。
	updateStartupDelay = 20 * time.Second
	updateInterval     = 24 * time.Hour
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

var (
	updateMu    sync.Mutex
	updateCache *UpdateInfo
	updateAt    time.Time

	// 后台自动检查的开关与时间点，供后台页面展示「上次 / 下次检查」
	autoEnabled bool
	autoLast    time.Time
	autoNext    time.Time

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
	if !force && updateCache != nil {
		ttl := updateCacheTTL
		if updateCache.Error != "" && updateCache.Latest == "" {
			ttl = updateErrTTL
		}
		if time.Since(updateAt) < ttl {
			return *updateCache
		}
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

// StartUpdateWatcher 启动「每天自动检查一次新版本」的后台任务。
//
// 检查结果落进和「检查更新」同一个缓存里，后台页面只读缓存（GET /api/admin/update/status），
// 所以打开概览页不会因为拉远端 VERSION 而卡住；发现新版本时页面会直接亮出提示。
// stop 被关闭时退出（main 里与清理任务共用同一个信号）。
//
// 以下情况不启动：
//   - UPDATE_DISABLE=1：整个在线更新被关掉；
//   - 当前版本是 dev / 空：本地开发的构建不该天天提示「有新版本」。
func StartUpdateWatcher(stop <-chan struct{}) {
	if config.EnvTrue("UPDATE_DISABLE") {
		logger.Info("自动检查更新已关闭（UPDATE_DISABLE=1）")
		return
	}
	if v := strings.TrimSpace(config.Version); v == "" || strings.EqualFold(v, "dev") {
		logger.Info("跳过自动检查更新：当前是开发版本", "version", config.Version)
		return
	}

	interval := updateInterval
	if s := strings.TrimSpace(os.Getenv("UPDATE_CHECK_INTERVAL")); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d >= time.Minute {
			interval = d
		} else {
			logger.Warn("UPDATE_CHECK_INTERVAL 不合法，按 24h 处理", "value", s)
		}
	}

	updateMu.Lock()
	autoEnabled = true
	autoNext = time.Now().Add(updateStartupDelay)
	updateMu.Unlock()

	logger.Info("已开启自动检查更新",
		"interval", interval.String(),
		"first_run_in", updateStartupDelay.String(),
		"current", config.Version,
	)

	go func() {
		timer := time.NewTimer(updateStartupDelay)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			info := CheckUpdate(ctx, true)
			cancel()

			now := time.Now()
			updateMu.Lock()
			autoLast, autoNext = now, now.Add(interval)
			updateMu.Unlock()

			switch {
			case info.Error != "":
				logger.Warn("自动检查更新失败", "error", info.Error)
			case info.HasUpdate:
				logger.Info("发现新版本，可在后台「版本与更新」升级",
					"current", info.Current, "latest", info.Latest, "source", info.Source)
			default:
				logger.Info("已是最新版本", "version", info.Current)
			}
			timer.Reset(interval)
		}
	}()
}

// CachedUpdate 返回最近一次检查结果，不触发任何网络请求。
func CachedUpdate() (UpdateInfo, bool) {
	updateMu.Lock()
	defer updateMu.Unlock()
	if updateCache == nil {
		return UpdateInfo{}, false
	}
	return *updateCache, true
}

// AutoCheckTiming 返回自动检查是否开启、上次与下次检查时间（未查过时 last 为零值）。
func AutoCheckTiming() (enabled bool, last, next time.Time) {
	updateMu.Lock()
	defer updateMu.Unlock()
	return autoEnabled, autoLast, autoNext
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

	var errs []string
	for _, s := range list {
		v, err := fetchText(ctx, s.url)
		if err != nil {
			errs = append(errs, s.name+"："+err.Error())
			continue
		}
		v = normalizeVersion(v)
		if v == "" {
			errs = append(errs, s.name+"：返回的版本号为空")
			continue
		}
		return v, s.name, nil
	}
	if len(errs) == 0 {
		return "", "", errors.New("没有可用的版本来源")
	}
	return "", "", fmt.Errorf("所有来源都不可用（%s）", strings.Join(errs, "；"))
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
