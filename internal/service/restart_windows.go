//go:build windows

package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RestartSelf 在 Windows 上不能 exec 换镜像：先停掉监听（否则新进程抢不到端口），
// 再以同样的参数拉起新进程，最后退出自己。
func RestartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}

	if shutdownFn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = shutdownFn(ctx)
		cancel()
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	if dir, derr := os.Getwd(); derr == nil {
		cmd.Dir = dir
	}
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// 新进程已经起来了，旧进程直接退出（不再走 defer / 优雅关闭，端口已释放）
	os.Exit(0)
	return nil
}
