package mail

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strconv"
	"strings"

	"lxdapi/internal/db"
	"lxdapi/models"
)

// Config SMTP 配置
type Config struct {
	Host     string
	Port     int
	User     string
	Pass     string
	From     string
	FromName string
	Enabled  bool
}

func get(key, def string) string {
	var s models.SystemSetting
	if err := db.DB.Where("key = ?", key).First(&s).Error; err != nil {
		return def
	}
	return s.Value
}

func set(key, value, group string) error {
	var s models.SystemSetting
	err := db.DB.Where("key = ?", key).First(&s).Error
	if err != nil {
		s = models.SystemSetting{Key: key, Value: value, Group: group}
		return db.DB.Create(&s).Error
	}
	return db.DB.Model(&s).Update("value", value).Error
}

// GetConfig 读取 SMTP 配置
func GetConfig() Config {
	port, _ := strconv.Atoi(get("smtp_port", "465"))
	if port == 0 {
		port = 465
	}
	return Config{
		Host:     get("smtp_host", ""),
		Port:     port,
		User:     get("smtp_user", ""),
		Pass:     get("smtp_pass", ""),
		From:     get("smtp_from", ""),
		FromName: get("smtp_from_name", "LXD 容器面板"),
		Enabled:  get("smtp_enabled", "false") == "true",
	}
}

// SaveConfig 保存 SMTP 配置
func SaveConfig(c Config) error {
	if c.Port == 0 {
		c.Port = 465
	}
	pairs := map[string]string{
		"smtp_enabled":   boolStr(c.Enabled),
		"smtp_host":      c.Host,
		"smtp_port":      strconv.Itoa(c.Port),
		"smtp_user":      c.User,
		"smtp_pass":      c.Pass,
		"smtp_from":      c.From,
		"smtp_from_name": c.FromName,
	}
	for k, v := range pairs {
		if err := set(k, v, "smtp"); err != nil {
			return err
		}
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// GetTemplate 读取邮件模板（不存在则使用默认）
func GetTemplate(key, defSubject, defBody string) (string, string) {
	subject := get(key+"_subject", defSubject)
	body := get(key+"_body", defBody)
	return subject, body
}

// SaveTemplate 保存邮件模板
func SaveTemplate(key, subject, body string) error {
	if err := set(key+"_subject", subject, "mail"); err != nil {
		return err
	}
	return set(key+"_body", body, "mail")
}

// Render 渲染模板：把 {{key}} 替换为 data 中对应值
func Render(tpl string, data map[string]string) string {
	out := tpl
	for k, v := range data {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// Send 发送邮件
func Send(to, subject, body string) error {
	cfg := GetConfig()
	if !cfg.Enabled || cfg.Host == "" {
		return fmt.Errorf("SMTP 未启用或未配置")
	}
	from := cfg.From
	if from == "" {
		from = cfg.User
	}
	msg := buildMessage(cfg, from, to, subject, body)
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)

	if cfg.Port == 465 {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
		if err != nil {
			return fmt.Errorf("连接 SMTP 失败: %v", err)
		}
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("创建 SMTP 客户端失败: %v", err)
		}
		defer client.Close()
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %v", err)
		}
		if err = client.Mail(from); err != nil {
			return err
		}
		if err = client.Rcpt(to); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err = w.Write([]byte(msg)); err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
		return client.Quit()
	}

	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
}

// SendTemplate 按模板发送，tplKey 如 mail_activate / mail_reset
func SendTemplate(to, tplKey, defSubject, defBody string, data map[string]string) error {
	subject, body := GetTemplate(tplKey, defSubject, defBody)
	return Send(to, Render(subject, data), Render(body, data))
}

func buildMessage(cfg Config, from, to, subject, body string) string {
	name := cfg.FromName
	if name == "" {
		name = "LXD 容器面板"
	}
	headers := map[string]string{
		"From":         fmt.Sprintf("=?UTF-8?B?%s?= <%s>", encodeBase64(name), from),
		"To":           to,
		"Subject":      fmt.Sprintf("=?UTF-8?B?%s?=", encodeBase64(subject)),
		"MIME-Version": "1.0",
		"Content-Type": "text/html; charset=UTF-8",
	}
	var sb strings.Builder
	for k, v := range headers {
		sb.WriteString(k + ": " + v + "\r\n")
	}
	sb.WriteString("\r\n" + body)
	return sb.String()
}

func encodeBase64(s string) string {
	return base64Encode(s)
}

func base64Encode(s string) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	data := []byte(s)
	for i := 0; i < len(data); i += 3 {
		var b [3]byte
		n := copy(b[:], data[i:])
		sb.WriteByte(chars[b[0]>>2])
		sb.WriteByte(chars[((b[0]&0x03)<<4)|(b[1]>>4)])
		if n > 1 {
			sb.WriteByte(chars[((b[1]&0x0f)<<2)|(b[2]>>6)])
		} else {
			sb.WriteByte('=')
		}
		if n > 2 {
			sb.WriteByte(chars[b[2]&0x3f])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}
