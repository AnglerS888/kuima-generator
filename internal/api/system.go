package api

import (
	"runtime"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/store"
)

// processStarted 记录进程启动时间，用于算运行时长。
var processStarted = time.Now()

// systemInfo 是后台「系统信息」卡片的数据。
//
// 只给管理员看（挂在 /api/admin/overview 下），所以可以带上数据库路径，
// 方便排查「库到底写在哪」；对公网页面不会暴露这些信息。
type systemInfo struct {
	Version    string `json:"version"`
	GoVersion  string `json:"goVersion"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	CPUs       int    `json:"cpus"`
	Goroutines int    `json:"goroutines"`

	StartedAt     string `json:"startedAt"`
	UptimeSeconds int64  `json:"uptimeSeconds"`

	// 内存（字节）
	MemAlloc uint64 `json:"memAlloc"`
	MemSys   uint64 `json:"memSys"`
	GCCount  uint32 `json:"gcCount"`

	// 数据库
	DBPath      string            `json:"dbPath"`
	DBSizeBytes int64             `json:"dbSizeBytes"`
	DBTables    []store.TableStat `json:"dbTables"`
}

// collectSystemInfo 采集一次系统信息（调用很轻，按需取即可）。
func collectSystemInfo() systemInfo {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	path := config.DBPath()
	return systemInfo{
		Version:       config.Version,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		CPUs:          runtime.NumCPU(),
		Goroutines:    runtime.NumGoroutine(),
		StartedAt:     processStarted.UTC().Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(processStarted).Seconds()),
		MemAlloc:      ms.Alloc,
		MemSys:        ms.Sys,
		GCCount:       ms.NumGC,
		DBPath:        path,
		DBSizeBytes:   store.DBFileSize(path),
		DBTables:      store.TableStats(),
	}
}
