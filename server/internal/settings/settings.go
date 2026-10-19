package settings

import (
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
