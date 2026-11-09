package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"wxcode/internal/httpx"
	"wxcode/internal/imagex"
	"wxcode/internal/store"
)

// ============================================================
// 预设 LOGO
//
// 管理员在后台传一批图片，用户在生成页「用预设」一键选用：
//   GET  /api/preset-logos          公开列表（只有 id / 名称 / 大小）
//   GET  /api/preset-logos/{id}     公开取原图（页面 <img> 与生成前转 data URL 都走这里）
//   GET  /api/admin/preset-logos    管理端列表
//   POST /api/admin/preset-logos    上传（multipart 字段 logo/file/image，或直接 POST 图片字节）
//   PUT  /api/admin/preset-logos/{id}  改名
//   DELETE /api/admin/preset-logos/{id} 删除
// ============================================================

// presetLogoUploadMax 是单个预设允许的原始字节数。
// 中心图在码里最多占 1/5 边长，4MB 已经很宽松；超了基本都是没压过的大图。
const presetLogoUploadMax = 4 << 20

// handlePresetLogos 返回「启用中」的预设列表（公开，不含图片字节）。
//
// 前端进页面时拉一次，空数组就把「用预设」这个入口藏起来。
// 排序由后台配置（sort 小的在前），停用的不会出现在这里。
//
// GET /api/preset-logos
func handlePresetLogos(w http.ResponseWriter, r *http.Request) {
	items, err := store.ListEnabledPresetLogos()
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	// 管理员随时可能增删改排序：别让浏览器缓存列表
	w.Header().Set("Cache-Control", "no-cache")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handlePresetLogoImage 返回预设 LOGO 原图。
//
// 按 id 取图，内容一旦写入就不再变化（改名不影响字节），
// 所以给长缓存；删掉后自然 404。
//
// GET /api/preset-logos/{id}
func handlePresetLogoImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("id 不合法"))
		return
	}
	mime, data, err := store.PresetLogoData(id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.WriteErr(w, http.StatusNotFound, errors.New("预设 LOGO 不存在"))
		return
	}
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, err)
		return
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(data)
}

// handleAdminPresetLogos 管理端：列表（GET）/ 上传（POST）。
func handleAdminPresetLogos(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := store.ListPresetLogos()
		if err != nil {
			httpx.WriteErr(w, http.StatusInternalServerError, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"items": items, "total": len(items), "max": store.PresetLogoMax,
		})

	case http.MethodPost:
		name, raw, err := readPresetUpload(r)
		if err != nil {
			httpx.WriteErr(w, StatusOf(err), err)
			return
		}
		data, mime, err := imagex.PresetLogo(raw, imagex.PresetLogoMaxSide)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
		// sort 不传就追加到末尾（后台列表里的「排序」可以再改）
		item, err := store.AddPresetLogo(name, mime, data, -1)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}
}

// handleAdminPresetLogoItem 管理端：局部更新（PUT）/ 删除（DELETE）。
//
// PUT 只改传上来的字段：{name}、{sort}、{enabled} 可以单独发，也可以一起发。
func handleAdminPresetLogoItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteErr(w, http.StatusBadRequest, errors.New("id 不合法"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body struct {
			Name    *string `json:"name"`
			Sort    *int    `json:"sort"`
			Enabled *bool   `json:"enabled"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpx.WriteErr(w, StatusOf(err), err)
			return
		}
		if body.Name == nil && body.Sort == nil && body.Enabled == nil {
			httpx.WriteErr(w, http.StatusBadRequest, errors.New("没有要修改的字段"))
			return
		}
		// 排序范围收在 0~9999：列表里就一个数字输入框，超出这个范围没意义
		if body.Sort != nil {
			if *body.Sort < 0 || *body.Sort > 9999 {
				httpx.WriteErr(w, http.StatusBadRequest, errors.New("排序值需在 0~9999 之间"))
				return
			}
		}
		err := store.UpdatePresetLogo(id, store.PresetLogoPatch{
			Name: body.Name, Sort: body.Sort, Enabled: body.Enabled,
		})
		if err != nil {
			httpx.WriteErr(w, statusOfStoreErr(err), err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})

	case http.MethodDelete:
		if err := store.DeletePresetLogo(id); err != nil {
			httpx.WriteErr(w, statusOfStoreErr(err), err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		w.Header().Set("Allow", "PUT, DELETE")
		httpx.WriteErr(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}
}

// readPresetUpload 读取上传的图片：multipart（logo / file / image / icon 任一字段），
// 或直接把图片字节当请求体 POST（方便 curl 一行传图）。
func readPresetUpload(r *http.Request) (string, []byte, error) {
	if httpx.IsMultipart(r) {
		if err := r.ParseMultipartForm(imagex.MaxUploadBody); err != nil {
			return "", nil, err
		}
		defer r.MultipartForm.RemoveAll()
		name := strings.TrimSpace(r.FormValue("name"))
		for _, key := range []string{"logo", "file", "image", "icon"} {
			fhs := r.MultipartForm.File[key]
			if len(fhs) == 0 {
				continue
			}
			f, err := fhs[0].Open()
			if err != nil {
				return "", nil, err
			}
			defer f.Close()
			raw, err := httpx.ReadBody(f, presetLogoUploadMax)
			if err != nil {
				return "", nil, err
			}
			if name == "" {
				name = fhs[0].Filename
			}
			return name, raw, nil
		}
		return "", nil, errNoImageField
	}

	if err := httpx.CapRequestBody(r, presetLogoUploadMax); err != nil {
		return "", nil, err
	}
	raw, err := httpx.ReadBody(r.Body, presetLogoUploadMax)
	if err != nil {
		return "", nil, err
	}
	if len(raw) == 0 {
		return "", nil, errEmptyBody
	}
	return strings.TrimSpace(r.URL.Query().Get("name")), raw, nil
}

// statusOfStoreErr 把「找不到」映射成 404，其它当参数错误。
func statusOfStoreErr(err error) int {
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
