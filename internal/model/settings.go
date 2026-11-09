package model

import (
	"errors"
	"strings"

	"wxcode/internal/util"
)

// settingsSnapshot 汇总前端需要的配置（不含敏感信息）。
// 水印全局配置（四档互斥）。
const (
	WatermarkNone  = "none"  // 无水印
	WatermarkAll   = "all"   // 所有人可以配置
	WatermarkUser  = "user"  // 登录用户可以配置
	WatermarkAdmin = "admin" // 管理员全局配置
)

type Settings struct {
	RegisterOpen   bool   `json:"registerOpen"`
	InviteRequired bool   `json:"inviteRequired"`
	RequireLogin   bool   `json:"requireLogin"`
	CaptchaEnabled bool   `json:"captchaEnabled"`
	DefaultQuota   int    `json:"defaultQuota"`
	SiteName       string `json:"siteName"`
	HasIcon        bool   `json:"hasIcon"`    // 是否上传过自定义图标
	IconVer        string `json:"iconVer"`    // 图标版本（拼 /icon.png?v= 用）
	FooterHTML     string `json:"footerHtml"` // 底部统计代码，只在管理端返回
	SiteKeywords   string `json:"siteKeywords"`
	SiteAnnounce   string `json:"siteAnnounce"`
	CORSOrigins    string `json:"corsOrigins"` // 允许跨域的来源白名单
	// 记录保留天数（0 = 永久保留）
	RecordRetentionDays int    `json:"recordRetentionDays"`
	RecordEnabled       bool   `json:"recordEnabled"`
	RecordPayloadMode   string `json:"recordPayloadMode"`
	// 自定义图标的 base64 原文。存进快照是为了让 /icon.png 不必再单独查一次库；
	// 用 json:"-" 排除，避免它跟着管理端 settings 响应一起下发（有几个 KB）。
	IconData string `json:"-"`
	// 上传上限（KB）
	AnonLogoKB  int `json:"anonLogoKb"`
	UserLogoKB  int `json:"userLogoKb"`
	AnonImageKB int `json:"anonImageKb"`
	UserImageKB int `json:"userImageKb"`
	// 邮箱验证 / 发信配置（管理端）
	EmailVerifyEnabled    bool   `json:"emailVerifyEnabled"`
	RegisterEmailRequired bool   `json:"registerEmailRequired"`
	SMTPHost              string `json:"smtpHost"`
	SMTPPort              int    `json:"smtpPort"`
	SMTPUser              string `json:"smtpUser"`
	// SMTPPass 只留在服务端内部用；管理端响应只给「是否已设置」。
	SMTPPass     string `json:"-"`
	SMTPPassSet  bool   `json:"smtpPassSet"`
	SMTPFrom     string `json:"smtpFrom"`
	SMTPFromName string `json:"smtpFromName"`
	SMTPTLS      string `json:"smtpTls"`

	// 生成码水印（全局默认值 + 四档全局配置）
	//
	// WatermarkMode 是唯一的总开关：none（不加水印）/ all（所有人可配置）/
	// user（登录用户可配置）/ admin（管理员全局配置，前端不展示水印设置）。
	WatermarkMode     string `json:"watermarkMode"`
	WatermarkText     string `json:"watermarkText"`
	WatermarkSize     int    `json:"watermarkSize"`    // 像素
	WatermarkColor    string `json:"watermarkColor"`   // #RRGGBB
	WatermarkOpacity  int    `json:"watermarkOpacity"` // 百分比 0~100
	WatermarkPosition string `json:"watermarkPosition"`
}

// ---------- 配置 ----------

type SMTPConfig struct {
	Host     string
	Port     int
	User     string
	Pass     string
	From     string
	FromName string
	TLS      string // ssl | starttls | none
}

// normalizeSMTPTLS 只接受三种值；空串/未知值回落到 ssl（更安全的默认）。
func NormalizeSMTPTLS(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none", "plain", "off":
		return "none"
	case "starttls", "tls_starttls":
		return "starttls"
	default:
		return "ssl"
	}
}

func smtpConfigFromSnapshot(s Settings) SMTPConfig {
	return SMTPConfig{
		Host:     strings.TrimSpace(s.SMTPHost),
		Port:     s.SMTPPort,
		User:     strings.TrimSpace(s.SMTPUser),
		Pass:     s.SMTPPass,
		From:     strings.TrimSpace(s.SMTPFrom),
		FromName: strings.TrimSpace(s.SMTPFromName),
		TLS:      NormalizeSMTPTLS(s.SMTPTLS),
	}
}

func (s Settings) SMTP() SMTPConfig { return smtpConfigFromSnapshot(s) }

// smtpReady 报告发信配置是否完整。缺任何一项都不算「可用」。
func (s Settings) SMTPReady() bool { return s.SMTP().Validate() == nil }

// emailResetAvailable 是前端看到的「邮箱找回是否真的能用」。
// 注意：它只代表系统层面可用，具体某个用户还要看他有没有绑定邮箱。
func (s Settings) EmailResetAvailable() bool {
	return s.EmailVerifyEnabled && s.SMTPReady()
}

// validate 返回第一条不满足的原因（中文，可直接给管理员看）。
func (c SMTPConfig) Validate() error {
	if c.Host == "" {
		return errors.New("请填写 SMTP 服务器地址")
	}
	if strings.ContainsAny(c.Host, " \t\r\n/") {
		return errors.New("SMTP 服务器地址不合法（只填域名或 IP，不要带协议/路径）")
	}
	if strings.Contains(c.Host, ":") && !strings.HasPrefix(c.Host, "[") {
		return errors.New("SMTP 服务器只填域名或 IP，端口要填在端口输入框里")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("SMTP 端口需在 1~65535 之间")
	}
	if !util.ValidEmail(c.From) {
		return errors.New("发信地址不是合法邮箱")
	}
	if util.UTF8Len(c.FromName) > 60 {
		return errors.New("发信人名称最多 60 个字符")
	}
	if strings.ContainsAny(c.FromName, "\r\n") {
		return errors.New("发信人名称不能包含换行")
	}
	if c.User == "" && c.Pass != "" {
		return errors.New("填了密码/授权码，就必须同时填写 SMTP 账号")
	}
	if c.User != "" && c.Pass == "" {
		return errors.New("请填写 SMTP 密码或授权码")
	}
	if util.UTF8Len(c.User) > util.EmailMaxLen {
		return errors.New("SMTP 账号过长")
	}
	return nil
}
