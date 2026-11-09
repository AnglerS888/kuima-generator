package api

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wxcode/internal/config"
	"wxcode/internal/httpx"
	"wxcode/internal/store"
)

// ============================================================
// 全站数据备份（导出 / 导入）
//
// 备份包是一个 zip：manifest.json（版本、时间）+ wxcode.db（VACUUM INTO 快照）。
// 站点图标、公告、SMTP 等配置都在数据库里，所以一个库文件就是全站数据。
// ============================================================

const (
	backupMaxUpload    = 512 << 20 // 上传压缩包上限 512MB
	backupMaxUnzip     = 2 << 30   // 解压上限 2GB（防 zip 炸弹）
	backupDBName       = "wxcode.db"
	backupManifestName = "manifest.json"
)

type backupManifest struct {
	App       string `json:"app"`
	Version   string `json:"version"`
	CreatedAt string `json:"createdAt"`
	Database  string `json:"database"`
}

// handleAdminBackupExport 导出全站数据压缩包。
//
// GET /api/admin/backup/export
func handleAdminBackupExport(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", "wxcode-export-")
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, backupDBName)
	if err := store.SnapshotTo(dbFile); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	manifest := backupManifest{
		App:       "wxcode",
		Version:   config.Version,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Database:  backupDBName,
	}
	zipPath := filepath.Join(dir, "backup.zip")
	if err := writeBackupZip(zipPath, dbFile, manifest); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}

	name := "wxcode-backup-" + time.Now().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, zipPath)
}

// writeBackupZip 把 manifest 与数据库快照打进一个 zip。
func writeBackupZip(dst, dbFile string, manifest backupManifest) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	// manifest：给人看，也方便以后做版本兼容
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: backupManifestName, Method: zip.Deflate})
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if _, err := mw.Write(body); err != nil {
		return err
	}

	dw, err := zw.CreateHeader(&zip.FileHeader{Name: backupDBName, Method: zip.Deflate})
	if err != nil {
		return err
	}
	db, err := os.Open(dbFile)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := io.Copy(dw, db); err != nil {
		return err
	}
	return zw.Close()
}

// handleAdminBackupImport 导入全站数据压缩包（覆盖现有数据）。
//
// POST /api/admin/backup/import  multipart 字段 file
//
// 导入后数据库被整体替换，当前会话很可能已不存在，前端应引导重新登录。
func handleAdminBackupImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, backupMaxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("上传的备份文件过大或格式不正确"))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("缺少备份文件（multipart 字段 file）"))
		return
	}
	defer file.Close()

	dir, err := os.MkdirTemp("", "wxcode-import-")
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(dir)

	upload := filepath.Join(dir, "upload.zip")
	dst, err := os.OpenFile(upload, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	n, err := io.Copy(dst, io.LimitReader(file, backupMaxUpload+1))
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, fmt.Errorf("保存上传文件失败: %w", err))
		return
	}
	if n > backupMaxUpload {
		httpx.WriteErr(w, http.StatusRequestEntityTooLarge, errors.New("备份文件超过 512MB"))
		return
	}

	dbFile, err := extractBackupDB(upload, dir)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if err := store.ValidateBackupFile(dbFile); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if err := store.ReplaceDatabase(dbFile); err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		// 库被换掉后旧会话可能已失效，前端据此提示重新登录
		"relogin": true,
	})
}

// extractBackupDB 从 zip 里取出 wxcode.db 到 dir，返回它的路径。
//
// 只认 wxcode.db 这一个条目：其它文件（包括 ../ 路径、绝对路径）一律忽略，
// 同时限制解压大小，避免被人用 zip 炸弹打爆内存/磁盘。
func extractBackupDB(zipPath, dir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", errors.New("不是有效的 zip 压缩包")
	}
	defer zr.Close()

	var entry *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			continue
		}
		if filepath.Base(name) == backupDBName {
			if entry != nil {
				return "", errors.New("压缩包里有多份 wxcode.db，无法确定用哪个")
			}
			entry = f
		}
	}
	if entry == nil {
		return "", errors.New("压缩包里没有 wxcode.db，请上传本站导出的备份")
	}
	if entry.UncompressedSize64 > backupMaxUnzip {
		return "", errors.New("备份数据库超过 2GB，拒绝解压")
	}

	rc, err := entry.Open()
	if err != nil {
		return "", fmt.Errorf("读取备份数据库失败: %w", err)
	}
	defer rc.Close()

	dstPath := filepath.Join(dir, backupDBName)
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(rc, backupMaxUnzip+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("解压备份数据库失败: %w", err)
	}
	if n > backupMaxUnzip {
		return "", errors.New("备份数据库超过 2GB，拒绝解压")
	}
	return dstPath, nil
}
