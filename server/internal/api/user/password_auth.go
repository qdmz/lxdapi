package user

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/mail"
	"lxdapi/pkg/response"
)

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}

func publicURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

func siteName() string {
	return "LXD 容器面板"
}

// Register 用户注册（注册后需邮箱激活）
func Register(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required,min=3,max=32"`
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=6,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误：用户名 3-32 位、邮箱格式正确、密码至少 6 位")
		return
	}

	var exists models.User
	if db.DB.Where("username = ?", req.Username).First(&exists).Error == nil {
		response.Error(c, 400, "用户名已被注册")
		return
	}
	if db.DB.Where("email = ?", req.Email).First(&exists).Error == nil {
		response.Error(c, 400, "邮箱已被注册")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		response.Error(c, 500, "密码加密失败")
		return
	}

	u := models.User{
		Username:      req.Username,
		Email:         req.Email,
		PasswordHash:  string(hash),
		APIKey:        "uk-" + randomToken(16),
		Status:        "pending",
		EmailVerified: false,
		ActivateToken: randomToken(24),
		RegisteredIP:  c.ClientIP(),
		CPUQuota:      0,
		MemoryQuota:   0,
		DiskQuota:     0,
		TrafficLimit:  0,
	}
	if err := db.DB.Create(&u).Error; err != nil {
		response.Error(c, 500, "创建用户失败: "+err.Error())
		return
	}

	link := publicURL(c) + "/api/user/activate?token=" + u.ActivateToken
	defSubject := "【{{site}}】请激活你的账号"
	defBody := `<div style="font-family:sans-serif;line-height:1.8"><h3>你好，{{username}}</h3><p>感谢注册 {{site}}，请点击下面的链接激活账号：</p><p><a href="{{link}}" style="background:#2563eb;color:#fff;padding:10px 18px;border-radius:6px;text-decoration:none">立即激活账号</a></p><p>如果按钮无法点击，请复制以下链接到浏览器打开：<br><span style="color:#666">{{link}}</span></p><p style="color:#999;font-size:12px">本邮件由系统自动发送，请勿回复。</p></div>`
	data := map[string]string{"site": siteName(), "username": u.Username, "email": u.Email, "link": link}

	if err := mail.SendTemplate(u.Email, "mail_activate", defSubject, defBody, data); err != nil {
		logger.Warn("发送激活邮件失败: %v", err)
		response.Error(c, 500, "注册成功但激活邮件发送失败，请联系管理员（SMTP 可能未配置）")
		return
	}

	logger.OK("新用户注册: %s (%s)", u.Username, u.Email)
	response.Success(c, gin.H{"registered": true, "mail_sent": true, "msg": "注册成功，请到邮箱点击激活链接"})
}

// Activate 邮箱激活
func Activate(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.Redirect(302, "/user/login?activate_error=empty")
		return
	}
	var u models.User
	if err := db.DB.Where("activate_token = ?", token).First(&u).Error; err != nil {
		c.Redirect(302, "/user/login?activate_error=invalid")
		return
	}
	u.EmailVerified = true
	u.Status = "active"
	u.ActivateToken = ""
	if err := db.DB.Save(&u).Error; err != nil {
		c.Redirect(302, "/user/login?activate_error=failed")
		return
	}
	logger.OK("用户邮箱激活成功: %s", u.Username)
	c.Redirect(302, "/user/login?activated=1")
}

// ForgotPassword 忘记密码：发送重置邮件
func ForgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "请填写正确的邮箱")
		return
	}

	// 无论用户是否存在都返回成功，避免邮箱探测
	var u models.User
	if err := db.DB.Where("email = ?", req.Email).First(&u).Error; err != nil {
		response.Success(c, gin.H{"msg": "如果该邮箱已注册，重置链接将发送到邮箱"})
		return
	}

	expire := time.Now().Add(24 * time.Hour)
	u.ResetToken = randomToken(24)
	u.ResetExpire = &expire
	if err := db.DB.Save(&u).Error; err != nil {
		response.Error(c, 500, "生成重置链接失败")
		return
	}

	link := publicURL(c) + "/user/reset?token=" + u.ResetToken
	defSubject := "【{{site}}】重置密码"
	defBody := `<div style="font-family:sans-serif;line-height:1.8"><h3>你好，{{username}}</h3><p>我们收到了重置密码的请求，请点击下面的链接设置新密码（24 小时内有效）：</p><p><a href="{{link}}" style="background:#2563eb;color:#fff;padding:10px 18px;border-radius:6px;text-decoration:none">重置密码</a></p><p>如果这不是你本人操作，请忽略此邮件。</p><p style="color:#999;font-size:12px">本邮件由系统自动发送，请勿回复。</p></div>`
	data := map[string]string{"site": siteName(), "username": u.Username, "email": u.Email, "link": link}

	if err := mail.SendTemplate(u.Email, "mail_reset", defSubject, defBody, data); err != nil {
		logger.Warn("发送重置密码邮件失败: %v", err)
		response.Error(c, 500, "邮件发送失败，请联系管理员（SMTP 可能未配置）")
		return
	}
	response.Success(c, gin.H{"msg": "重置链接已发送，请查收邮箱"})
}

// ResetPassword 用重置 token 设置新密码
func ResetPassword(c *gin.Context) {
	var req struct {
		Token    string `json:"token" binding:"required"`
		Password string `json:"password" binding:"required,min=6,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误：token 必填，密码至少 6 位")
		return
	}

	var u models.User
	if err := db.DB.Where("reset_token = ?", req.Token).First(&u).Error; err != nil {
		response.Error(c, 400, "重置链接无效")
		return
	}
	if u.ResetExpire == nil || u.ResetExpire.Before(time.Now()) {
		response.Error(c, 400, "重置链接已过期，请重新申请")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		response.Error(c, 500, "密码加密失败")
		return
	}
	u.PasswordHash = string(hash)
	u.ResetToken = ""
	u.ResetExpire = nil
	if err := db.DB.Save(&u).Error; err != nil {
		response.Error(c, 500, "重置失败: "+err.Error())
		return
	}
	logger.OK("用户重置密码成功: %s", u.Username)
	response.Success(c, gin.H{"msg": "密码已重置，请使用新密码登录"})
}

// passwordLogin 注册用户的密码登录（邮箱或用户名 + 密码），成功返回用户
func passwordLogin(account, password string) (*models.User, bool) {
	var u models.User
	if err := db.DB.Where("username = ? OR email = ?", account, account).First(&u).Error; err != nil {
		return nil, false
	}
	if u.PasswordHash == "" {
		return nil, false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, false
	}
	now := time.Now()
	u.LastLoginAt = &now
	db.DB.Model(&u).Update("last_login_at", now)
	return &u, true
}
