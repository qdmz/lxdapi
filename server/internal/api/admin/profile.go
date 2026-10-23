package admin

import (
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"lxdapi/internal/core"
	"lxdapi/pkg/response"
)

// GetAdminProfile 获取当前管理员资料
// 管理员身份存放在配置文件（admin.username），没有独立的资料表，
// 因此管理员“资料”仅包含登录用户名。
func GetAdminProfile(c *gin.Context) {
	response.Success(c, gin.H{
		"username": core.GlobalConfig.Admin.Username,
	})
}

// UpdateAdminProfile 修改管理员用户名，并写回配置文件（重启后生效）
func UpdateAdminProfile(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	username := strings.TrimSpace(req.Username)
	if len(username) < 2 || len(username) > 64 || strings.ContainsAny(username, " \t\r\n") {
		response.Error(c, 400, "用户名长度须为 2-64 个字符且不能包含空白")
		return
	}
	if username == core.GlobalConfig.Admin.Username {
		response.Success(c, gin.H{"username": username})
		return
	}
	if err := core.UpdateAdminCredentials(username, ""); err != nil {
		response.Error(c, 500, "保存失败: "+err.Error())
		return
	}
	// 同步当前会话里的用户名，顶栏展示立即刷新
	session := sessions.Default(c)
	session.Set("admin_username", username)
	session.Save()
	response.Success(c, gin.H{"username": username})
}

// ChangeAdminPassword 修改管理员密码（需验证旧密码），新密码以 bcrypt 哈希写回配置文件
func ChangeAdminPassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if len(req.NewPassword) < 6 {
		response.Error(c, 400, "新密码至少 6 位")
		return
	}
	if !checkAdminPassword(core.GlobalConfig.Admin.Username, req.OldPassword) {
		response.Error(c, 400, "旧密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		response.Error(c, 500, "密码加密失败")
		return
	}
	if err := core.UpdateAdminCredentials("", string(hash)); err != nil {
		response.Error(c, 500, "保存失败: "+err.Error())
		return
	}
	response.Success(c, "密码修改成功，下次登录请使用新密码")
}
