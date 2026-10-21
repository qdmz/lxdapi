package admin

import (
	"strings"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/internal/settings"
	"lxdapi/models"
	"lxdapi/pkg/mail"
	"lxdapi/pkg/response"
)

// GetGeneralSettings 获取通用开关（注册 / 验证码 / 邮件通知 / 站点地址）
func GetGeneralSettings(c *gin.Context) {
	response.Success(c, gin.H{
		"register_enabled": settings.RegisterEnabled(),
		"captcha_enabled":  settings.CaptchaEnabled(),
		"email_notify":     settings.EmailNotifyEnabled(),
		"site_url":         settings.SiteURL(),
	})
}

// SaveGeneralSettings 保存通用开关
func SaveGeneralSettings(c *gin.Context) {
	var req struct {
		RegisterEnabled *bool   `json:"register_enabled"`
		CaptchaEnabled  *bool   `json:"captcha_enabled"`
		EmailNotify     *bool   `json:"email_notify"`
		SiteURL         *string `json:"site_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}
	if req.RegisterEnabled != nil {
		_ = settings.Set("register_enabled", boolStr(*req.RegisterEnabled), "general")
	}
	if req.CaptchaEnabled != nil {
		_ = settings.Set("captcha_enabled", boolStr(*req.CaptchaEnabled), "general")
	}
	if req.EmailNotify != nil {
		_ = settings.Set("email_notify_enabled", boolStr(*req.EmailNotify), "general")
	}
	if req.SiteURL != nil {
		// 站点地址允许留空（= 按请求域名自动拼链接）
		_ = settings.Set("site_url", strings.TrimSpace(*req.SiteURL), "general")
	}
	response.Success(c, gin.H{
		"register_enabled": settings.RegisterEnabled(),
		"captcha_enabled":  settings.CaptchaEnabled(),
		"email_notify":     settings.EmailNotifyEnabled(),
		"site_url":         settings.SiteURL(),
	})
}

// SendBroadcast 管理员向全体用户发送广播（站内通知 + 可选邮件）
func SendBroadcast(c *gin.Context) {
	var req struct {
		Title    string `json:"title" binding:"required"`
		Content  string `json:"content" binding:"required"`
		SendMail bool   `json:"send_mail"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}

	var users []models.User
	db.DB.Select("id, email").Find(&users)

	broadcast := req.Title
	for _, u := range users {
		db.DB.Create(&models.Notification{
			UserID:      u.ID,
			Type:        "promotion",
			Title:       broadcast,
			Content:     req.Content,
			IsRead:      false,
			RelatedType: "broadcast",
		})
		if req.SendMail && u.Email != "" && settings.EmailNotifyEnabled() {
			_ = mail.Send(u.Email, "【LXD 容器面板】"+broadcast, `<div style="font-family:sans-serif;line-height:1.8"><h3>`+broadcast+`</h3><p>`+req.Content+`</p></div>`)
		}
	}

	response.Success(c, gin.H{"sent": len(users)})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// GetSMTPSettings 获取 SMTP 配置与邮件模板
func GetSMTPSettings(c *gin.Context) {
	cfg := mail.GetConfig()
	as, ab := mail.GetTemplate("mail_activate", "", "")
	rs, rb := mail.GetTemplate("mail_reset", "", "")
	os, ob := mail.GetTemplate("mail_order_paid", "", "")
	response.Success(c, gin.H{
		"smtp": cfg,
		"templates": gin.H{
			"activate_subject":   as,
			"activate_body":      ab,
			"reset_subject":      rs,
			"reset_body":         rb,
			"order_subject":      os,
			"order_body":         ob,
		},
	})
}

// SaveSMTPSettings 保存 SMTP 配置与邮件模板（模板留空则使用系统默认）
func SaveSMTPSettings(c *gin.Context) {
	var req struct {
		Enabled         bool   `json:"enabled"`
		Host            string `json:"host"`
		Port            int    `json:"port"`
		User            string `json:"user"`
		Pass            string `json:"pass"`
		From            string `json:"from"`
		FromName        string `json:"from_name"`
		ActivateSubject string `json:"activate_subject"`
		ActivateBody    string `json:"activate_body"`
		ResetSubject    string `json:"reset_subject"`
		ResetBody       string `json:"reset_body"`
		OrderSubject    string `json:"order_subject"`
		OrderBody       string `json:"order_body"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}

	if err := mail.SaveConfig(mail.Config{
		Enabled:  req.Enabled,
		Host:     req.Host,
		Port:     req.Port,
		User:     req.User,
		Pass:     req.Pass,
		From:     req.From,
		FromName: req.FromName,
	}); err != nil {
		response.Error(c, 500, "保存 SMTP 配置失败: "+err.Error())
		return
	}

	if req.ActivateSubject != "" || req.ActivateBody != "" {
		_ = mail.SaveTemplate("mail_activate", req.ActivateSubject, req.ActivateBody)
	}
	if req.ResetSubject != "" || req.ResetBody != "" {
		_ = mail.SaveTemplate("mail_reset", req.ResetSubject, req.ResetBody)
	}
	if req.OrderSubject != "" || req.OrderBody != "" {
		_ = mail.SaveTemplate("mail_order_paid", req.OrderSubject, req.OrderBody)
	}

	response.Success(c, gin.H{"msg": "已保存"})
}

// TestSMTP 发送测试邮件
func TestSMTP(c *gin.Context) {
	var req struct {
		To string `json:"to" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "请填写正确的收件邮箱")
		return
	}
	body := `<div style="font-family:sans-serif;line-height:1.8"><h3>SMTP 配置正常</h3><p>这是来自 LXD 容器面板的测试邮件，收到即表示邮件服务配置成功。</p></div>`
	if err := mail.Send(req.To, "【LXD 容器面板】SMTP 测试邮件", body); err != nil {
		response.Error(c, 500, "发送失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"msg": "测试邮件已发送，请查收"})
}
