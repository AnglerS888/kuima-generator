package service

// ============================================================
// 在线更新（执行侧）：异步任务 + 分阶段进度 + 失败原因
//
// 更新分两条路，自动选择：
//   1) 下载发布产物（快，不需要编译）：GitHub Releases / UPDATE_ASSET_BASE
//   2) 下载源码 + 本地 go build（慢，需要环境里有 Go 编译器）
//
// 只有第 1 条不通时才会走第 2 条 —— 很多人并没有发 Releases 产物，
// 这时直接在服务器/容器里编译源码反而更省事。
//
// 进度通过 GetJob 暴露给后台页面（stage/message/percent），
// 前端轮询即可显示「下载中 62% → 编译中 → 替换程序 → 重启中」。
// ============================================================

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/logger"
)

// 更新阶段。前端按 stage 显示文案与进度条。
const (
	StageCheck    = "check"    // 检查版本
	StageDownload = "download" // 下载产物 / 源码
	StageExtract  = "extract"  // 解压源码
	StageBuild    = "build"    // 编译
	StageVerify   = "verify"   // 校验产物
	StageSwap     = "swap"     // 替换程序
	StageRestart  = "restart"  // 重启
	StageDone     = "done"     // 完成（无需更新 / 已就绪）
	StageError    = "error"    // 失败
)

const (
	updateApplyTimeout = 20 * time.Minute // 源码编译留足时间（首次要下依赖）
	updateJobKeep      = 30 * time.Minute // 完成的任务保留这么久，方便刷新页面后回看
	updateMaxDownload  = 300 << 20        // 单次下载上限（产物或源码包）
	updateBuildCache   = "wxcode-go-cache"
)

// UpdateJob 是一次更新任务的进度快照。
type UpdateJob struct {
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	Percent   int    `json:"percent"` // 0~100；-1 表示进度不确定（编译阶段）
	Strategy  string `json:"strategy,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
	Force     bool   `json:"force,omitempty"`
	Done      bool   `json:"done"`
	StartedAt string `json:"startedAt"`
	UpdatedAt string `json:"updatedAt"`
}

var (
	jobMu sync.Mutex
	job   *UpdateJob
)

// GetJob 返回当前（或最近一次）更新任务；没有任务时返回 nil。
func GetJob() *UpdateJob {
	jobMu.Lock()
	defer jobMu.Unlock()
	if job == nil {
		return nil
	}
	cp := *job
	return &cp
}

func jobSet(stage, msg string, pct int) {
	jobMu.Lock()
	defer jobMu.Unlock()
	if job == nil {
		return
	}
	job.Stage, job.Message, job.Percent = stage, msg, pct
	job.UpdatedAt = time.Now().Format(time.RFC3339)
}

func jobPatch(fn func(*UpdateJob)) {
	jobMu.Lock()
	defer jobMu.Unlock()
	if job == nil {
		return
	}
	fn(job)
	job.UpdatedAt = time.Now().Format(time.RFC3339)
}

func jobFail(msg string) {
	jobMu.Lock()
	defer jobMu.Unlock()
	if job == nil {
		return
	}
	job.Stage, job.Message, job.Error = StageError, msg, msg
	job.Percent, job.Done = 0, true
	job.UpdatedAt = time.Now().Format(time.RFC3339)
}

// StartApply 启动一次更新任务，立即返回；进度用 GetJob 轮询。
// 同一时间只允许一个任务。
//
// force=true 是「强制更新」：不看远端版本号大小，直接拉远端源码/产物覆盖当前程序，
// 版本号没变也照做（用于怀疑本地文件被改坏、或想回到远端当前代码）。
func StartApply(force bool) (*UpdateJob, error) {
	jobMu.Lock()
	if job != nil && !job.Done {
		cur := *job
		jobMu.Unlock()
		return &cur, errors.New("已有更新任务正在进行，请等待它结束")
	}
	now := time.Now().Format(time.RFC3339)
	msg := "正在检查最新版本…"
	if force {
		msg = "强制更新：正在获取远端版本…"
	}
	job = &UpdateJob{Stage: StageCheck, Message: msg, Percent: 1, Force: force, StartedAt: now, UpdatedAt: now}
	out := *job
	jobMu.Unlock()

	go runApply(force)
	return &out, nil
}

// runApply 是任务主体：检查 → 下载产物（失败则编译源码）→ 替换 → 重启。
func runApply(force bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("更新任务异常", "panic", fmt.Sprint(r))
			jobFail(fmt.Sprintf("更新过程中出现异常：%v", r))
		}
		// 完成的任务过一段时间自动清掉，避免一直挂着一份旧进度
		go func() {
			time.Sleep(updateJobKeep)
			jobMu.Lock()
			if job != nil && job.Done {
				job = nil
			}
			jobMu.Unlock()
		}()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), updateApplyTimeout)
	defer cancel()

	startMsg := "正在检查最新版本…"
	if force {
		startMsg = "强制更新：正在获取远端版本…"
	}
	jobSet(StageCheck, startMsg, 2)
	info := CheckUpdate(ctx, true)
	if !force {
		if info.Latest == "" && info.Error != "" {
			jobFail("检查最新版本失败：" + info.Error + "（可检查服务器能否访问 Gitee / GitHub，或配置 UPDATE_VERSION_URL）")
			return
		}
		if !info.HasUpdate {
			jobSet(StageDone, "已是最新版本（v"+info.Latest+"）", 100)
			jobPatch(func(j *UpdateJob) { j.Done = true })
			return
		}
	} else {
		// 强制更新：版本没变化也继续；连版本号都拿不到（离线）时用当前版本注入编译
		if info.Latest == "" {
			info.Latest = config.Version
		}
		logger.Info("强制更新：忽略版本比较", "current", config.Version, "latest", info.Latest, "error", info.Error)
	}
	jobPatch(func(j *UpdateJob) { j.Version = info.Latest })

	// 0) 先确认程序目录可写：两条更新路径都要替换二进制，权限问题先拦住，
	//    否则会先试产物、再试源码，把同一个错误报两遍。
	if exe, err := executablePath(); err != nil {
		jobFail(err.Error())
		return
	} else if err := checkWritable(exe); err != nil {
		jobFail(err.Error())
		return
	}

	// 1) 先试发布产物
	assetErr := applyFromAsset(ctx)
	if assetErr == nil {
		return
	}
	logger.Warn("发布产物更新失败，改用源码编译", "error", assetErr.Error())

	// 2) 再试源码编译（需要 Go）
	if err := applyFromSource(ctx, info.Latest); err != nil {
		msg := err.Error()
		if assetErr != nil {
			msg += "（发布产物也不可用：" + assetErr.Error() + "）"
		}
		jobFail(msg)
		return
	}
}

// applyFromAsset 下载各平台发布产物并替换。
func applyFromAsset(ctx context.Context) error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	if err := checkWritable(exe); err != nil {
		return err
	}
	jobPatch(func(j *UpdateJob) { j.Strategy = "binary" })

	var lastErr error
	for _, u := range assetURLs() {
		dest := exe + ".new"
		jobSet(StageDownload, "下载发布产物："+hostOf(u), 5)
		if err := downloadFile(ctx, u, dest, 5, 45); err != nil {
			lastErr = err
			_ = os.Remove(dest)
			continue
		}
		return finishUpdate(exe, dest, 45)
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载地址")
	}
	return lastErr
}

// applyFromSource 下载源码、编译、替换。要求环境里有 go。
func applyFromSource(ctx context.Context, version string) error {
	goBin, lookErr := exec.LookPath("go")
	if lookErr != nil {
		return fmt.Errorf("发布产物不可用，且当前环境没有 Go 编译器（go）无法在本地编译。" +
			"容器部署请在服务器上执行 bin/deploy.sh 重建镜像（或构建镜像时加 --build-arg WITH_GO=1），" +
			"也可以在 Releases 里放好各平台产物")
	}
	exe, err := executablePath()
	if err != nil {
		return err
	}
	if err := checkWritable(exe); err != nil {
		return err
	}
	jobPatch(func(j *UpdateJob) { j.Strategy = "source" })

	tmpDir, err := os.MkdirTemp("", "wxcode-update-")
	if err != nil {
		return fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(tmpDir)

	tarPath := filepath.Join(tmpDir, "source.tar.gz")
	jobSet(StageDownload, "下载最新源码…", 8)
	if err := downloadSource(ctx, tarPath); err != nil {
		return fmt.Errorf("下载源码失败：%w", err)
	}

	jobSet(StageExtract, "解压源码…", 30)
	srcDir, err := extractTarGz(tarPath, filepath.Join(tmpDir, "src"))
	if err != nil {
		return fmt.Errorf("解压源码失败：%w", err)
	}

	jobSet(StageBuild, "编译中（首次编译要下载依赖，可能需要几分钟）…", -1)
	// 直接编译到程序目录（exe.new）：/tmp 有可能是另一个挂载点（tmpfs），
	// 跨设备 rename 会报 invalid cross-device link，同目录就没这个问题。
	out := exe + ".new"
	if err := runGoBuild(ctx, goBin, srcDir, out, version); err != nil {
		_ = os.Remove(out)
		return fmt.Errorf("编译失败：%w", err)
	}
	return finishUpdate(exe, out, 90)
}

// finishUpdate 校验 → 替换 → 重启。
func finishUpdate(exe, newPath string, base int) error {
	jobSet(StageVerify, "校验新程序…", base+2)
	if err := verifyBinary(newPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	jobSet(StageSwap, "替换程序…", base+4)
	if err := swapBinaryFrom(exe, newPath); err != nil {
		return err
	}
	jobSet(StageRestart, "替换完成，正在重启服务…", 99)
	jobPatch(func(j *UpdateJob) { j.Done = true })
	go func() {
		// 留一点时间让前端拉到「重启中」这条状态
		time.Sleep(700 * time.Millisecond)
		if err := RestartSelf(); err != nil {
			jobFail("自动重启失败：" + err.Error() + "，请手动重启服务")
		}
	}()
	return nil
}

// ---------- 下载 ----------

// downloadSource 依次尝试源码归档地址，成功即停。
func downloadSource(ctx context.Context, dest string) error {
	var errs []string
	for _, u := range sourceURLs() {
		jobSet(StageDownload, "下载最新源码："+hostOf(u), 10)
		ct, err := fetchToFile(ctx, u, dest, 10, 30)
		if err == nil {
			// 校验必须紧跟下载：某些地址会返回 200 的网页（登录/风控页），
			// 不在这里发现的话，要等解压时才报 "gzip: invalid header"，也错过了换下一个源。
			err = validateArchive(dest, ct)
		}
		if err == nil {
			return nil
		}
		errs = append(errs, hostOf(u)+"："+err.Error())
		_ = os.Remove(dest)
	}
	if len(errs) == 0 {
		return errors.New("没有可用的源码地址")
	}
	return fmt.Errorf("所有源码地址都不可用（%s）", strings.Join(errs, "；"))
}

// validateArchive 确认下载到的确实是 gzip 包（.tar.gz）。
// 重点是把「拿到了网页」这种情况说清楚：常见于仓库私有、需要登录，或触发了人机校验。
func validateArchive(p, ctype string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b {
		return nil
	}
	low := strings.ToLower(string(head))
	hint := ""
	if strings.Contains(low, "<html") || strings.Contains(low, "<!doctype") || strings.Contains(low, "<script") {
		hint = "，返回的是网页（可能是仓库不可访问、需要登录，或被风控/人机校验拦了）"
	}
	ct := ""
	if ctype != "" {
		ct = "，Content-Type: " + ctype
	}
	return fmt.Errorf("下载到的不是 gzip 源码包%s%s", hint, ct)
}

// downloadFile 带进度地把 url 下载到 dest（不关心 Content-Type）。
func downloadFile(ctx context.Context, url, dest string, from, to int) error {
	_, err := fetchToFile(ctx, url, dest, from, to)
	return err
}

// fetchToFile 带进度下载并返回 Content-Type。
//
// 用独立的 client：检查版本用的 client 有 8 秒总超时，下载几十 MB 产物会被它误杀。
func fetchToFile(ctx context.Context, url, dest string, from, to int) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := updateDownloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接 %s 失败：%w", hostOf(url), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%s 上没有这个文件（404）", hostOf(url))
		}
		return "", fmt.Errorf("%s 返回 HTTP %d", hostOf(url), resp.StatusCode)
	}
	ctype := resp.Header.Get("Content-Type")

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", fmt.Errorf("写入临时文件失败：%w", err)
	}
	pw := &progressWriter{total: resp.ContentLength, from: from, to: to}
	n, copyErr := io.Copy(f, io.TeeReader(io.LimitReader(resp.Body, updateMaxDownload), pw))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("下载中断：%w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return "", closeErr
	}
	if n == 0 {
		_ = os.Remove(dest)
		return "", errors.New("下载到 0 字节")
	}
	return ctype, nil
}

// progressWriter 把已下载字节换算成百分比写进任务状态（节流 250ms）。
type progressWriter struct {
	total   int64
	from    int
	to      int
	written int64
	last    time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	now := time.Now()
	if now.Sub(p.last) < 250*time.Millisecond {
		return n, nil
	}
	p.last = now
	if p.total > 0 {
		pct := p.from + int(float64(p.to-p.from)*float64(p.written)/float64(p.total))
		jobSet(StageDownload, fmt.Sprintf("下载中 %d%%（%s / %s）",
			pct, humanBytes(p.written), humanBytes(p.total)), pct)
	} else {
		jobSet(StageDownload, "下载中…（已接收 "+humanBytes(p.written)+"）", -1)
	}
	return n, nil
}

var updateDownloadClient = &http.Client{} // 超时由每个请求的 ctx 控制

// sourceURLs 列出源码归档地址（UPDATE_SOURCE_URL 可指向自建源）。
func sourceURLs() []string {
	if u := strings.TrimSpace(os.Getenv("UPDATE_SOURCE_URL")); u != "" {
		return []string{u}
	}
	branch := updateBranch()
	gitee := "https://gitee.com/" + updateGiteeRepo() + "/repository/archive/" + branch + ".tar.gz"
	gh := "https://github.com/" + updateGithubRepo() + "/archive/refs/heads/" + branch + ".tar.gz"
	codeload := "https://codeload.github.com/" + updateGithubRepo() + "/tar.gz/refs/heads/" + branch
	urls := []string{gitee, gh, codeload}
	if m := updateMirror(); m != "" {
		prefix := strings.TrimRight(m, "/") + "/"
		urls = append(urls, prefix+gh, prefix+codeload)
	}
	return urls
}

// ---------- 编译 ----------

// runGoBuild 在源码目录里编译，把进度（最后一行输出）同步到任务状态。
func runGoBuild(ctx context.Context, goBin, dir, out, version string) error {
	cache := filepath.Join(os.TempDir(), updateBuildCache)
	_ = os.MkdirAll(cache, 0o755)

	cmd := exec.CommandContext(ctx, goBin, "build", "-trimpath",
		"-ldflags", "-s -w -X wxcode/internal/config.Version="+version,
		"-o", out, "./cmd/wxcode")
	cmd.Dir = dir
	env := os.Environ()
	env = append(env,
		"CGO_ENABLED=0",
		"GOFLAGS=-mod=mod",
		"HOME="+cache,
		"GOCACHE="+filepath.Join(cache, "build"),
		"GOMODCACHE="+filepath.Join(cache, "mod"),
		"GOPATH="+filepath.Join(cache, "path"),
	)
	if strings.TrimSpace(os.Getenv("GOPROXY")) == "" {
		env = append(env, "GOPROXY=https://goproxy.cn,direct")
	}
	cmd.Env = env

	tw := &tailWriter{}
	cmd.Stdout, cmd.Stderr = tw, tw
	if err := cmd.Run(); err != nil {
		detail := tailLines(tw.String(), 4)
		if detail != "" {
			return fmt.Errorf("%w（%s）", err, detail)
		}
		return err
	}
	return nil
}

// tailWriter 收集编译输出，并把最后一行实时写进进度（编译没有百分比，只能看日志）。
type tailWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	lastAt time.Time
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if t.buf.Len() > 16384 { // 只留尾部，避免编译告警把内存撑大
		b := t.buf.Bytes()
		keep := b[len(b)-8192:]
		t.buf.Reset()
		t.buf.Write(keep)
	}
	if time.Since(t.lastAt) > 400*time.Millisecond {
		if line := lastNonEmptyLine(string(p)); line != "" {
			t.lastAt = time.Now()
			jobSet(StageBuild, "编译中："+clip(line, 120), -1)
		}
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

// ---------- 解压 ----------

// extractTarGz 解压 .tar.gz 到 dest，返回真正的源码根目录（跳过 URL 里的顶层目录）。
// 只接受普通文件与目录：符号链接、绝对路径、.. 一律跳过。
func extractTarGz(archive, dest string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	root := filepath.Clean(dest)
	_ = os.MkdirAll(root, 0o755)
	var total int64
	files := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name := path.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			total += hdr.Size
			files++
			if total > updateMaxDownload || files > 50000 {
				return "", errors.New("源码包异常（文件过多或过大）")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, hdr.Size)); err != nil {
				_ = out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		default:
			// 符号链接 / 设备文件等直接忽略
		}
	}

	// 归档一般带一层顶层目录（kuima-generator-main/），有就进去
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(root, entries[0].Name()), nil
	}
	return root, nil
}

// ---------- 校验 / 替换 ----------

func verifyBinary(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("找不到新程序：%w", err)
	}
	if st.Size() < updateMinSize {
		return fmt.Errorf("新程序只有 %d 字节，可能下载/编译不完整", st.Size())
	}
	head := make([]byte, 2)
	fh, err := os.Open(p)
	if err != nil {
		return err
	}
	defer fh.Close()
	if _, err := io.ReadFull(fh, head); err != nil {
		return errors.New("读取新程序失败")
	}
	if runtime.GOOS == "windows" {
		if head[0] != 'M' || head[1] != 'Z' {
			return errors.New("新文件不是 Windows 可执行文件（可能是错误页）")
		}
		return nil
	}
	if head[0] != 0x7f || head[1] != 'E' {
		return errors.New("新文件不是 Linux 可执行文件（可能是错误页）")
	}
	return nil
}

func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前程序失败：%w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return exe, nil
}

// checkWritable 提前确认程序所在目录可写：容器只读根文件系统时直接给明确提示，
// 而不是下载完几十 MB 才发现替换不了。
func checkWritable(exe string) error {
	dir := filepath.Dir(exe)
	f, err := os.CreateTemp(dir, ".wxcode-write-test-*")
	if err != nil {
		return fmt.Errorf("程序所在目录不可写（%s）：%v。\n"+
			"在线更新需要在程序目录里改文件名并写入新程序，所以该目录必须对运行用户可写：\n"+
			"  * 用官方镜像时 /app 已 chown 给 app 用户；如果你是旧镜像，请重新构建（bin/deploy.sh）；\n"+
			"  * 自己改过 Dockerfile / 用 --user 指定了别的用户，请保证该用户对 %s 有写权限；\n"+
			"  * 容器以只读文件系统启动（--read-only）时无法内部更新，请改用 bin/deploy.sh 重建镜像；\n"+
			"  * 想立刻恢复：docker exec -u 0 <容器名> chown -R app:app /app（只对当前容器生效，重建容器后失效）",
			dir, err, dir)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// swapBinaryFrom 用 newPath 替换 exe（运行中的文件不能直接覆盖，先改名再移入）。
func swapBinaryFrom(exe, newPath string) error {
	oldPath := exe + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		return fmt.Errorf("备份旧程序失败：%w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(oldPath, exe) // 尽量还原，别把程序弄丢
		return fmt.Errorf("替换程序失败：%w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(exe, 0o755)
	}
	return nil
}

// ---------- 小工具 ----------

func hostOf(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return u
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if v := strings.TrimSpace(lines[i]); v != "" {
			return v
		}
	}
	return ""
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if v := strings.TrimSpace(l); v != "" {
			out = append(out, v)
		}
	}
	return clip(strings.Join(out, " | "), 300)
}
