package handlers

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"lxdapi/internal/service"
	"lxdapi/pkg/utils"
	"net/http"
)

func UserLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_login.html", utils.MergeTemplateData(c, gin.H{}))
}

func UserDashboard(c *gin.Context) {
	token := c.Query("token")
	if token != "" {
		accessToken, err := service.ValidateAccessToken(token)
		if err == nil && accessToken.Type == "user" {
			user, err := service.GetUserByUsername(accessToken.Target)
			if err == nil {
				session := sessions.Default(c)
				session.Set("user_logged_in", true)
				session.Set("user_username", user.Username)
				session.Set("user_id", user.ID)
				session.Save()
				c.Redirect(http.StatusFound, "/user/containers")
				return
			}
		}
	}
	c.Redirect(http.StatusFound, "/user/login")
}

func UserContainers(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_containers.html", utils.MergeTemplateData(c, gin.H{
		"title":    "我的容器 - LXD API",
		"username": "User",
	}))
}

func UserContainerDetail(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_container_detail.html", utils.MergeTemplateData(c, gin.H{
		"title":    "容器详情 - LXD API",
		"username": "User",
		"name":     c.Query("name"),
	}))
}

// UserRegisterPage 用户注册页
func UserRegisterPage(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_register.html", utils.MergeTemplateData(c, gin.H{
		"title": "注册账号",
	}))
}

// UserForgotPage 忘记密码页
func UserForgotPage(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_forgot.html", utils.MergeTemplateData(c, gin.H{
		"title": "找回密码",
	}))
}

// UserResetPage 重置密码页（携带 token）
func UserResetPage(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_reset.html", utils.MergeTemplateData(c, gin.H{
		"title": "设置新密码",
		"token": c.Query("token"),
	}))
}
