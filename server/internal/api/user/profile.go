package user

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// GetProfile 获取当前用户资料
func GetProfile(c *gin.Context) {
	uid, _ := c.Get("user_id")
	var u models.User
	if err := db.DB.First(&u, uid).Error; err != nil {
		response.Error(c, 404, "用户不存在")
		return
	}
	response.Success(c, gin.H{
		"id":       u.ID,
		"username": u.Username,
		"nickname": u.Nickname,
		"email":    u.Email,
		"status":   u.Status,
	})
}

// UpdateProfile 修改昵称/邮箱
func UpdateProfile(c *gin.Context) {
	uid, _ := c.Get("user_id")
	var req struct {
		Nickname string `json:"nickname"`
		Email    string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	updates := map[string]interface{}{}
	if req.Nickname != "" {
		updates["nickname"] = req.Nickname
	}
	if req.Email != "" {
		// 邮箱唯一性检查
		var cnt int64
		db.DB.Model(&models.User{}).Where("email = ? AND id != ?", req.Email, uid).Count(&cnt)
		if cnt > 0 {
			response.Error(c, 400, "该邮箱已被使用")
			return
		}
		updates["email"] = req.Email
	}
	if len(updates) == 0 {
		response.Error(c, 400, "没有要修改的内容")
		return
	}
	if err := db.DB.Model(&models.User{}).Where("id = ?", uid).Updates(updates).Error; err != nil {
		response.Error(c, 500, "保存失败")
		return
	}
	response.Success(c, "保存成功")
}

// ChangePassword 修改登录密码（需验证旧密码）
func ChangePassword(c *gin.Context) {
	uid, _ := c.Get("user_id")
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
	var u models.User
	if err := db.DB.First(&u, uid).Error; err != nil {
		response.Error(c, 404, "用户不存在")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)) != nil {
		response.Error(c, 400, "旧密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		response.Error(c, 500, "密码加密失败")
		return
	}
	db.DB.Model(&models.User{}).Where("id = ?", uid).Update("password_hash", string(hash))
	response.Success(c, "密码修改成功")
}
