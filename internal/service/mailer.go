package service

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"wxcode/internal/model"
	"wxcode/internal/store"
	"wxcode/internal/util"
)

const smtpDialTimeout = 10 * time.Second

const smtpIOTimeout = 30 * time.Second

const EmailPurposeReset = "reset"

const EmailPurposeRegister = "register"

// 这些文案同时出现在接口错误与前端页面里，改一处要同步前端。
var ErrEmailDisabled = errors.New("管理员未开启邮箱找回密码，请联系管理员重置密码")

var ErrEmailNotReady = errors.New("管理员尚未完成发信配置，暂时无法通过邮箱找回，请联系管理员重置密码")

var ErrEmailNoBound = errors.New("该账号未绑定邮箱，无法通过邮箱找回，请联系管理员重置密码")

var ErrEmailSendFail = errors.New("验证码邮件发送失败，请稍后重试或联系管理员")

var ErrEmailSame = errors.New("该邮箱已经是当前账号的绑定邮箱")

var ErrEmailTaken = errors.New("该邮箱已被其他账号绑定")

var ErrEmailFormat = errors.New("邮箱格式不正确")

var ErrEmailPassWrong = errors.New("密码不正确，无法解绑")

var ErrRegisterMailOff = errors.New("管理员未开启邮箱注册验证，无需发送邮箱验证码")

var ErrRegisterMailNotReady = errors.New("管理员已开启邮箱注册验证，但发信配置不完整，暂时无法注册，请稍后重试或联系管理员")

var ErrRegisterMailNeeded = errors.New("请填写邮箱并完成邮箱验证后再注册")

var MailSender = sendMail

// ---------- 发信 ----------

// sendMail 通过 SMTP 发送一封纯文本邮件。
//
// 三种加密方式：
//   - ssl：先建 TLS 再说话（465 端口常见）
//   - starttls：明文连上后 STARTTLS 升级（587 / 25 端口）
//   - none：全程明文（只建议内网中继；配置页会提示风险）
func sendMail(cfg model.SMTPConfig, to, subject, text string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	to = util.NormalizeEmail(to)
	if !util.ValidEmail(to) {
		return ErrEmailFormat
	}
	subject = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(subject, "\r", " "), "\n", " "))
	msg := buildMailMessage(cfg, to, subject, text)

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: smtpDialTimeout}

	var conn net.Conn
	var err error
	if cfg.TLS == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器 %s 失败：%v", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(smtpIOTimeout))
	defer conn.Close()

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP 握手失败：%v", err)
	}
	defer c.Close()

	if cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器不支持 STARTTLS，请改用 SSL（通常 465 端口）或 none")
		}
		if err := c.StartTLS(&tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return fmt.Errorf("STARTTLS 失败：%v", err)
		}
	}

	if cfg.User != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("SMTP 服务器不支持账号认证，请把账号/密码留空后重试")
		}
		if err := c.Auth(plainAuth{user: cfg.User, pass: cfg.Pass}); err != nil {
			return fmt.Errorf("SMTP 认证失败（账号或授权码不正确）：%v", err)
		}
	}

	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("SMTP 拒绝发信地址 %s：%v", cfg.From, err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP 拒绝收信地址 %s：%v", to, err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP 准备邮件内容失败：%v", err)
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("SMTP 写入邮件失败：%v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("SMTP 提交邮件失败：%v", err)
	}
	_ = c.Quit()
	return nil
}

// plainAuth 是自实现的 AUTH PLAIN。
//
// 不用 smtp.PlainAuth：它硬性拒绝在「未加密连接 + 非 localhost」上发送凭据，
// 而内网中继（tls=none）恰恰是这个场景；是否加密由管理员在配置页选择。
type plainAuth struct {
	user, pass string
}

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("SMTP 服务器返回了不支持的多轮认证")
	}
	return nil, nil
}

// buildMailMessage 拼一封 UTF-8 纯文本邮件。
//
// 正文用 base64：彻底避开点号开头的行被 SMTP 截断（dot-stuffing）、
// 超长行被拒、以及 8bit 内容被中继改写的问题。
func buildMailMessage(cfg model.SMTPConfig, to, subject, text string) []byte {
	from := cfg.From
	if cfg.FromName != "" {
		from = mime.QEncoding.Encode("UTF-8", cfg.FromName) + " <" + cfg.From + ">"
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	if encoded != "" {
		b.WriteString(encoded + "\r\n")
	}
	return []byte(b.String())
}

// emailSubjectKey 把用户 id 变成验证码的「归属键」。
// 注册时还没有用户 id，用 emailAddrKey 以邮箱作为归属键。
func EmailSubjectKey(userID int64) string { return strconv.FormatInt(userID, 10) }

func EmailAddrKey(email string) string { return "email:" + util.NormalizeEmail(email) }

// ---------- 邮件正文 ----------

func ResetPasswordMailText(siteName, username, code string) string {
	if strings.TrimSpace(siteName) == "" {
		siteName = "本站"
	}
	return "您正在重置 " + siteName + " 的账号密码。\r\n\r\n" +
		"用户名：" + username + "\r\n" +
		"验证码：" + code + "\r\n" +
		"有效期：" + strconv.Itoa(int(store.EmailCodeTTL.Minutes())) + " 分钟\r\n\r\n" +
		"如果这不是您本人的操作，请忽略本邮件，您的密码不会被修改。\r\n" +
		"请勿把验证码告诉任何人。\r\n"
}

func RegisterMailText(siteName, code string) string {
	if strings.TrimSpace(siteName) == "" {
		siteName = "本站"
	}
	return "您正在注册 " + siteName + " 账号，本次注册需要验证邮箱。\r\n\r\n" +
		"验证码：" + code + "\r\n" +
		"有效期：" + strconv.Itoa(int(store.EmailCodeTTL.Minutes())) + " 分钟\r\n\r\n" +
		"如果这不是您本人的操作，请忽略本邮件，不会有账号被创建。\r\n" +
		"请勿把验证码告诉任何人。\r\n"
}

func BindEmailMailText(siteName, username, code string) string {
	if strings.TrimSpace(siteName) == "" {
		siteName = "本站"
	}
	return "您正在为 " + siteName + " 的账号绑定邮箱。\r\n\r\n" +
		"用户名：" + username + "\r\n" +
		"验证码：" + code + "\r\n" +
		"有效期：" + strconv.Itoa(int(store.EmailCodeTTL.Minutes())) + " 分钟\r\n\r\n" +
		"如果这不是您本人的操作，请忽略本邮件，该邮箱不会被绑定。\r\n"
}
