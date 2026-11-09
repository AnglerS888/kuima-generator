//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"syscall"
)

// RestartSelf 用磁盘上的新二进制替换当前进程（PID 不变，systemd / docker 不受影响）。
//
// 监听 socket 都是 CLOEXEC，exec 之后自动关闭，新镜像可以重新 bind 同一端口。
func RestartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
