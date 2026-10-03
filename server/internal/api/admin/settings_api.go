package admin

import (
	"github.com/gin-gonic/gin"
	"lxdapi/pkg/mail"
	"lxdapi/pkg/response"
)

// GetSMTPSettings 获取 SMTP 配置与邮件模板
func GetSMTPSettings(c *gin.Context) {
	cfg := mail.GetConfig()
	as, ab := mail.GetTemplate("mail_activate", "", "")
	rs, rb := mail.GetTemplate("mail_reset", "", "")
	response.Success(c, gin.H{
		"smtp": cfg,
		"templates": gin.H{
			"activate_subject": as,
			"activate_body":    ab,
			"reset_subject":    rs,
			"reset_body":       rb,
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
