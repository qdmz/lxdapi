package public

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/mojocn/base64Captcha"
	"lxdapi/pkg/response"
)

var captchaStore = base64Captcha.DefaultMemStore

// GetCaptcha 获取公开页面验证码（注册 / 找回密码）
// @Summary 获取公开验证码
// @Description 生成图片验证码用于注册和找回密码
// @Tags Public API
// @Accept json
// @Produce json
// @Success 200 {object} object{code=int,captcha_id=string,image=string} "生成成功"
// @Failure 500 {object} response.Response "生成失败"
// @Router /api/public/captcha [get]
func GetCaptcha(c *gin.Context) {
	driver := base64Captcha.NewDriverDigit(80, 240, 4, 0.7, 80)
	cap := base64Captcha.NewCaptcha(driver, captchaStore)
	id, b64s, _, err := cap.Generate()
	if err != nil {
		response.Error(c, 500, "生成验证码失败")
		return
	}
	session := sessions.Default(c)
	session.Set("public_captcha_id", id)
	session.Save()
	c.JSON(200, gin.H{
		"code":       200,
		"captcha_id": id,
		"image":      b64s,
	})
}
