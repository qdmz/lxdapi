package settings

import (
	"strings"

	"github.com/gin-gonic/gin"

	"lxdapi/internal/db"
	"lxdapi/models"
)

// Get 读取系统 KV 开关（缺失时返回默认值）
func Get(key, def string) string {
	var s models.SystemSetting
	if err := db.DB.Where("key = ?", key).First(&s).Error; err != nil {
		return def
	}
	return s.Value
}

// Set 写入系统 KV 开关
func Set(key, value, group string) error {
	var s models.SystemSetting
	if err := db.DB.Where("key = ?", key).First(&s).Error; err != nil {
		s = models.SystemSetting{Key: key, Value: value, Group: group}
		return db.DB.Create(&s).Error
	}
	return db.DB.Model(&s).Update("value", value).Error
}

// 管理员可配置开关（默认值即兜底）
const (
	DefaultRegisterEnabled = "true"
	DefaultCaptchaEnabled  = "true"
	DefaultEmailNotifyOn   = "false"
)

func RegisterEnabled() bool { return Get("register_enabled", DefaultRegisterEnabled) == "true" }

// CaptchaEnabled 注册 / 忘记密码 / 重置密码 是否需要图形验证码
func CaptchaEnabled() bool { return Get("captcha_enabled", DefaultCaptchaEnabled) == "true" }

// EmailNotifyEnabled 是否启用邮件通知（关闭时跳过所有出站邮件）
func EmailNotifyEnabled() bool {
	return Get("email_notify_enabled", DefaultEmailNotifyOn) == "true"
}

// SiteURL 管理员配置的站点地址（对外公网地址，如 https://panel.example.com）；
// 返回时去掉空白与尾部斜杠；为空表示未配置，链接按请求自动拼。
func SiteURL() string {
	return strings.TrimRight(strings.TrimSpace(Get("site_url", "")), "/")
}

// PublicBaseURL 对外访问基地址（无尾部斜杠），用于激活 / 重置 / 支付回调等链接：
// 优先管理员配置的站点地址，其次反代头 X-Forwarded-Host，最后回退到请求 Host。
func PublicBaseURL(c *gin.Context) string {
	if u := SiteURL(); u != "" {
		return u
	}
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	return scheme + "://" + host
}
