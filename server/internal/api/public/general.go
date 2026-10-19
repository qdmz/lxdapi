package public

import (
	"github.com/gin-gonic/gin"
	"lxdapi/internal/settings"
	"lxdapi/pkg/response"
)

// GetGeneralSettings 获取公开的系统开关（注册是否开放 / 是否启用验证码）
func GetGeneralSettings(c *gin.Context) {
	response.Success(c, gin.H{
		"register_enabled": settings.RegisterEnabled(),
		"captcha_enabled":  settings.CaptchaEnabled(),
		"email_notify":     settings.EmailNotifyEnabled(),
	})
}
