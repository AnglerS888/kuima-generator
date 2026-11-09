package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DBPath 默认 data/wxcode.db，可用 DATA_DIR / DB_PATH 覆盖（容器里挂卷到这个目录）。
func DBPath() string {
	if p := strings.TrimSpace(os.Getenv("DB_PATH")); p != "" {
		return p
	}
	dir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dir == "" {
		dir = "data"
	}
	return filepath.Join(dir, "wxcode.db")
}
