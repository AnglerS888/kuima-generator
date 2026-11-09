package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// 纯 Go 的 SQLite 驱动：注册 "sqlite" 方言，全程序只在这里引入一次。
	_ "modernc.org/sqlite"

	"wxcode/internal/util"
)

// ============================================================
// SQLite：库句柄、建表、配置项
//
// 用 modernc.org/sqlite（纯 Go 实现）而不是 mattn/go-sqlite3，
// 因为后者要 CGO，会破坏 Dockerfile 里的 CGO_ENABLED=0 静态编译。
// ============================================================

var DB *sql.DB

// Open 打开数据库并建表。单连接：SQLite 的写是串行的，
// 多连接只会换来 "database is locked"，这个量级的服务单连接完全够。
func Open(path string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	dbPath = path
	return openLocked(path)
}

// openLocked 是 Open 的实际实现，调用方必须持有 dbMu（导入备份时会先关库再打开）。
func openLocked(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	var err error
	DB, err = sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	DB.SetMaxOpenConns(1)
	DB.SetMaxIdleConns(1)
	if err = DB.Ping(); err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	// 库里有密码哈希、API Key、邮箱与 SMTP 授权码，文件权限收紧到仅属主可读写。
	// Windows 上 Chmod 基本是空操作，真正生效的是 Linux/容器部署；失败不阻断启动。
	_ = os.Chmod(path, 0o600)
	if err := migrate(); err != nil {
		return err
	}
	// 库刚打开/刚被导入替换，配置快照必须重读
	InvalidateSettings()
	return nil
}

const SetInviteRequired = "invite_required" // 注册是否需要邀请码

const SetRequireLogin = "require_login" // 使用是否需要登录（生成/解码/好友码）

const SetCaptchaEnabled = "captcha_enabled" // 登录 / 注册是否启用图形验证码

const SetDefaultQuota = "default_quota" // 新用户默认额度

const SetSiteName = "site_name" // 站点名（页面标题用）

const SetSiteIcon = "site_icon" // 自定义站点图标（PNG 的 base64，不带 data: 前缀）

const SetSiteIconVer = "site_icon_ver" // 图标版本号：每次上传换一个，用来给 /icon.png?v= 换 URL

const SetFooterHTML = "footer_html" // 底部统计代码（原样注入 </body> 之前）

const SetSiteKeywords = "site_keywords" // 搜索关键词（写进 <meta name="keywords">）

const SetSiteAnnounce = "site_announce" // 网站公告（注入到每个页面 <main> 之前）

const SetCORSOrigins = "cors_origins" // 允许跨域的来源白名单（逗号分隔，* = 全放开）

// SetWatermarkEnabled 是旧版「开启水印」开关，已废弃：启动时会被合并进 SetWatermarkMode 并删除。
const SetWatermarkEnabled = "watermark_enabled"

const SetWatermarkMode = "watermark_mode" // 水印全局配置：none / all / user / admin

const SetWatermarkText = "watermark_text" // 默认水印文字（全局默认值）

const SetWatermarkSize = "watermark_size" // 默认字号（像素，8~240）

const SetWatermarkColor = "watermark_color" // 默认颜色 #RRGGBB

const SetWatermarkOpacity = "watermark_opacity" // 默认透明度（百分比 0~100）

const SetWatermarkPosition = "watermark_position" // 默认位置 center|bottom|bottom-right|tile

const SetRecordRetentionDays = "record_retention_days" // 记录保留天数，0 = 永久保留

const SetRecordEnabled = "record_enabled" // 是否记录生成/解析历史

const SetRecordPayloadMode = "record_payload_mode" // full = 原文，hash = 哈希，none = 不存内容

const SetAnonLogoKB = "anon_logo_kb" // 未登录：中心 logo

const SetUserLogoKB = "user_logo_kb" // 已登录：中心 logo

const SetAnonImageKB = "anon_image_kb" // 未登录：解析图片

const SetUserImageKB = "user_image_kb" // 已登录：解析图片

const SetEmailVerify = "email_verify_enabled" // 是否开启邮箱验证（找回密码 / 绑定邮箱）

const SetRegisterMail = "register_email_required" // 是否强制邮箱注册（注册必须验证邮箱）

const SetSMTPHost = "smtp_host" // SMTP 服务器，如 smtp.qq.com

const SetSMTPPort = "smtp_port" // 端口，465（SSL）/ 25 / 587

const SetSMTPUser = "smtp_user" // 账号，通常是完整邮箱地址

const SetSMTPPass = "smtp_pass" // 密码 / 授权码

const SetSMTPFrom = "smtp_from" // 发信地址（发件人邮箱）

const SetSMTPFromName = "smtp_from_name" // 发信人名称

const SetSMTPTLS = "smtp_tls" // ssl（隐式 TLS）| starttls | none

// ---------- 小工具 ----------

// newBatchID 生成一个可读、可排序的生成批次号：20261109-115530-a1b2c3。
// 批次只用于「导出本次生成」，不参与业务判断。
func newBatchID() string {
	return time.Now().UTC().Format("20060102-150405") + "-" + util.RandomHex(3)
}

func NowStr() string { return time.Now().UTC().Format(time.RFC3339) }

var ErrNotFound = errors.New("记录不存在")

// isUniqueErr 判断是否唯一索引冲突（用户名/邀请码重复）。
func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique constraint") || strings.Contains(s, "constraint failed")
}

// parseTimeLoose 接受 "2006-01-02" 或 RFC3339。
// 只给日期时按当天 23:59:59 算 —— 邀请码写"到 12-31"应该包含这一天。
func ParseTimeLoose(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("时间为空")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.Add(24*time.Hour - time.Second).UTC(), nil
	}
	return time.Time{}, errors.New("时间格式不支持")
}

// ============================================================
// 运维信息（后台概览展示用）
// ============================================================

// TableStat 是一张表的行数统计。
type TableStat struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// DBFileSize 返回数据库文件总占用（含 WAL 日志与共享内存文件），单位字节。
func DBFileSize(path string) int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(path + suffix); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// TableStats 统计核心表的行数。
//
// 表名来自包内白名单（不接受外部输入），避免把用户数据拼进 SQL。
func TableStats() []TableStat {
	names := []string{
		"users", "sessions", "records", "invite_codes",
		"card_keys", "email_codes", "blacklist", "settings", "preset_logos",
	}
	out := make([]TableStat, 0, len(names))
	for _, name := range names {
		var n int64
		if err := DB.QueryRow(`SELECT COUNT(*) FROM ` + name).Scan(&n); err != nil {
			continue
		}
		out = append(out, TableStat{Name: name, Rows: n})
	}
	return out
}
