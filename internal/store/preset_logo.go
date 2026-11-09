package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"wxcode/internal/util"
)

// ============================================================
// 预设 LOGO：管理员上传，用户在生成页一键选用
//
// 只存原始图片字节（GIF 保留动画），随数据库备份 / 导入一起走。
// 页面拿 /api/preset-logos/{id} 当 <img> 用，选中后再转成 data URL
// 发给 /api/encode —— 生成链路本身完全不知道「预设」的存在。
//
// 排序与启用都在库里：sort 小的排在前面（同值按 id），enabled=0 的
// 只对后台可见，前台列表直接过滤掉。
// ============================================================

const (
	PresetLogoMax     = 30 // 最多保留多少个，防止把库塞爆
	PresetLogoNameMax = 20 // 名称最大字符数
	PresetSortStep    = 10 // 新上传的排序步长：留出中间插队的空间
)

// PresetLogo 是预设 LOGO 的元信息（不含图片字节）。
type PresetLogo struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	Bytes     int    `json:"bytes"`
	Sort      int    `json:"sort"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

const presetLogoCols = `id, name, mime, bytes, sort, enabled, created_at`

// listPresetLogos 是列表的公共实现；onlyEnabled 为真时只取前台的可见项。
func listPresetLogos(onlyEnabled bool) ([]PresetLogo, error) {
	q := `SELECT ` + presetLogoCols + ` FROM preset_logos`
	if onlyEnabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort, id`
	rows, err := DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PresetLogo, 0, 8)
	for rows.Next() {
		var p PresetLogo
		if err := rows.Scan(&p.ID, &p.Name, &p.MIME, &p.Bytes, &p.Sort, &p.Enabled, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPresetLogos 返回全部预设（后台用），按 sort、id 升序。
func ListPresetLogos() ([]PresetLogo, error) { return listPresetLogos(false) }

// ListEnabledPresetLogos 返回启用中的预设（前台用），同样按 sort、id 升序。
func ListEnabledPresetLogos() ([]PresetLogo, error) { return listPresetLogos(true) }

// CountPresetLogos 返回已保存的预设数量。
func CountPresetLogos() (int, error) {
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM preset_logos`).Scan(&n)
	return n, err
}

// NextPresetSort 返回追加到末尾时用的排序值（最大 + 步长）。
func NextPresetSort() (int, error) {
	var n int
	if err := DB.QueryRow(`SELECT COALESCE(MAX(sort), 0) FROM preset_logos`).Scan(&n); err != nil {
		return 0, err
	}
	return n + PresetSortStep, nil
}

// PresetLogoData 取出某个预设的 MIME 与原始字节。
//
// 这里不过滤 enabled：已经打开的页面、浏览器缓存里可能还挂着它的 URL，
// 停用只影响「列表里还能不能选」，不该让图突然 404。
func PresetLogoData(id int64) (string, []byte, error) {
	var mime string
	var data []byte
	err := DB.QueryRow(`SELECT mime, data FROM preset_logos WHERE id = ?`, id).Scan(&mime, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	return mime, data, nil
}

// AddPresetLogo 新增一个预设。name 为空时用「LOGO {id}」兜底，
// sort < 0 表示追加到末尾。
func AddPresetLogo(name, mime string, data []byte, sort int) (PresetLogo, error) {
	if len(data) == 0 {
		return PresetLogo{}, errors.New("图片内容为空")
	}
	n, err := CountPresetLogos()
	if err != nil {
		return PresetLogo{}, err
	}
	if n >= PresetLogoMax {
		return PresetLogo{}, fmt.Errorf("预设 LOGO 最多 %d 个，请先删掉不用的", PresetLogoMax)
	}
	if sort < 0 {
		if sort, err = NextPresetSort(); err != nil {
			return PresetLogo{}, err
		}
	}
	name = cleanPresetName(name)
	now := NowStr()
	res, err := DB.Exec(`INSERT INTO preset_logos(name, mime, bytes, sort, enabled, data, created_at)
		VALUES(?, ?, ?, ?, 1, ?, ?)`, name, mime, len(data), sort, data, now)
	if err != nil {
		return PresetLogo{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PresetLogo{}, err
	}
	if name == "" {
		name = fmt.Sprintf("LOGO %d", id)
		if _, err := DB.Exec(`UPDATE preset_logos SET name = ? WHERE id = ?`, name, id); err != nil {
			return PresetLogo{}, err
		}
	}
	return PresetLogo{
		ID: id, Name: name, MIME: mime, Bytes: len(data),
		Sort: sort, Enabled: true, CreatedAt: now,
	}, nil
}

// PresetLogoPatch 是「只改传来的字段」，nil 表示不动。
type PresetLogoPatch struct {
	Name    *string
	Sort    *int
	Enabled *bool
}

// UpdatePresetLogo 局部更新名称 / 排序 / 启用状态。
//
// 先查存在再改，而不是看 RowsAffected：SQLite 的 changes() 是「连接级」的，
// 单连接模式下另一条语句插进来就会把它冲掉；而且「写回同样的值」本来也是 0 行变更，
// 用它判断存在性会误报「记录不存在」。
func UpdatePresetLogo(id int64, p PresetLogoPatch) error {
	ok, err := presetLogoExists(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	sets := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if p.Name != nil {
		name := cleanPresetName(*p.Name)
		if name == "" {
			return errors.New("名称不能为空")
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if p.Sort != nil {
		sets = append(sets, "sort = ?")
		args = append(args, *p.Sort)
	}
	if p.Enabled != nil {
		v := 0
		if *p.Enabled {
			v = 1
		}
		sets = append(sets, "enabled = ?")
		args = append(args, v)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err = DB.Exec(`UPDATE preset_logos SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// DeletePresetLogo 删除一个预设。
func DeletePresetLogo(id int64) error {
	ok, err := presetLogoExists(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	_, err = DB.Exec(`DELETE FROM preset_logos WHERE id = ?`, id)
	return err
}

// presetLogoExists 报告该 id 的预设是否存在。
func presetLogoExists(id int64) (bool, error) {
	var one int
	err := DB.QueryRow(`SELECT 1 FROM preset_logos WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// cleanPresetName 去空白、去换行并截断到 NameMax。
func cleanPresetName(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	return strings.TrimSpace(util.Truncate(s, PresetLogoNameMax))
}
