package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ============================================================
// 全站数据备份：导出快照 / 校验备份文件 / 替换数据库
// ============================================================

var (
	dbPath string
	dbMu   sync.Mutex
)

// DBFilePath 返回当前数据库文件的绝对/相对路径（启动时 Open 传入的那个）。
func DBFilePath() string {
	dbMu.Lock()
	defer dbMu.Unlock()
	return dbPath
}

// SnapshotTo 导出一份一致的数据库快照到 dest。
//
// 用 SQLite 的 VACUUM INTO：它会把当前库重写成一个干净的库（不含空闲页、WAL 已合并），
// 比直接拷贝 .db 文件安全——拷贝时 WAL 里可能还有没落盘的数据。
func SnapshotTo(dest string) error {
	if DB == nil {
		return errors.New("数据库未初始化")
	}
	_ = os.Remove(dest)
	if _, err := DB.Exec(`VACUUM INTO ?`, dest); err != nil {
		// 某些 SQLite 版本不支持参数化 VACUUM INTO，退回手工拼路径（路径由服务端生成）
		q := `VACUUM INTO '` + escapeSQLPath(dest) + `'`
		if _, err2 := DB.Exec(q); err2 != nil {
			return fmt.Errorf("导出数据库快照失败: %w", err)
		}
	}
	if _, err := os.Stat(dest); err != nil {
		return fmt.Errorf("导出数据库快照失败: %w", err)
	}
	return nil
}

// escapeSQLPath 只用于 VACUUM INTO 的文件名（服务端自己拼的临时路径）。
func escapeSQLPath(p string) string {
	out := make([]rune, 0, len(p))
	for _, r := range p {
		if r == '\'' {
			out = append(out, '\'')
		}
		out = append(out, r)
	}
	return string(out)
}

// ValidateBackupFile 检查一个 SQLite 文件能否当作备份导入。
//
// 只读打开，跑 integrity_check，并要求至少有 users / settings 两张表，
// 避免把随便一个 sqlite 文件（或损坏文件）覆盖到线上库。
func ValidateBackupFile(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return fmt.Errorf("无法打开备份文件: %w", err)
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("备份文件不是有效的 SQLite 数据库: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("备份数据库完整性校验失败: %s", integrity)
	}
	for _, name := range []string{"users", "settings"} {
		var n int
		q := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.QueryRow(q, name).Scan(&n); err != nil {
			return fmt.Errorf("读取备份表结构失败: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("备份文件缺少 %s 表，可能不是本站的数据库", name)
		}
	}
	return nil
}

// ReplaceDatabase 用 src 覆盖当前库并重新打开（导入备份）。
//
// 步骤：关连接 → 备份旧库到 .bak → 清 WAL/SHM → 复制新库 → 重新打开并迁移。
// 复制失败会用 .bak 回滚，尽量不把服务留在“没有库”的状态。
func ReplaceDatabase(src string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if dbPath == "" {
		return errors.New("数据库尚未初始化")
	}
	return replaceLocked(src)
}

func replaceLocked(src string) error {
	old := dbPath
	backup := old + ".bak"

	if DB != nil {
		// 先用 VACUUM INTO 做一份一致性备份（包含还在 WAL 里、没落盘的数据）。
		// 失败不阻断导入，只是少一层回滚保险。
		if _, err := os.Stat(old); err == nil {
			_ = SnapshotTo(backup)
		}
		// 把 WAL 合回主库，再关连接；否则下面拷走的可能只是 4KB 的库头
		_, _ = DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		_ = DB.Close()
		DB = nil
	}

	// WAL / SHM 必须删掉且确认成功：只要旧 WAL 还在，SQLite 打开新库时会把它重放回去，
	// 表现就是“导入成功但数据没变”。删不掉（被别的进程占用）就明确报错，别硬来。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(old + suffix); err != nil && !os.IsNotExist(err) {
			_ = openLocked(old)
			return fmt.Errorf("数据库仍被占用，导入失败，请稍后重试: %w", err)
		}
	}

	if err := copyFile(src, old); err != nil {
		if _, e := os.Stat(backup); e == nil {
			_ = copyFile(backup, old)
		}
		_ = openLocked(old)
		return fmt.Errorf("写入备份数据库失败: %w", err)
	}

	if err := openLocked(old); err != nil {
		// 新库打不开：回滚到导入前的备份，保证服务还能用
		if _, e := os.Stat(backup); e == nil {
			_ = copyFile(backup, old)
			_ = openLocked(old)
		}
		return fmt.Errorf("导入后打开数据库失败: %w", err)
	}
	return nil
}

// copyFile 复制文件（权限收到 0600，备份里有密码哈希与 API Key）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil && filepath.Dir(dst) != "." {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
