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
		c.Redirect(http.StatusFound, "/user/login")
		return
	}
	// 无 token 参数时：已通过 UserPageAuth（会话已登录，如模拟登录/正常登录），
	// 直接进用户首页（容器列表），不要再踢回登录页
	c.Redirect(http.StatusFound, "/user/containers")
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

// UserShop 产品购买页
func UserShop(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_shop.html", utils.MergeTemplateData(c, gin.H{
		"title": "购买服务 - LXD API",
	}))
}

// UserOrders 我的订单页
func UserOrders(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_orders.html", utils.MergeTemplateData(c, gin.H{
		"title": "我的订单 - LXD API",
	}))
}

// UserTickets 工单列表页
func UserTickets(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_tickets.html", utils.MergeTemplateData(c, gin.H{
		"title": "工单中心 - LXD API",
	}))
}

// UserTicketDetail 工单详情页
func UserTicketDetail(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_ticket_detail.html", utils.MergeTemplateData(c, gin.H{
		"title": "工单详情 - LXD API",
		"id":    c.Param("id"),
	}))
}

// UserCoupons 我的优惠券页
func UserCoupons(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_coupons.html", utils.MergeTemplateData(c, gin.H{
		"title": "我的优惠券 - LXD API",
	}))
}

// UserNotifications 站内消息页
func UserNotifications(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_notifications.html", utils.MergeTemplateData(c, gin.H{
		"title": "站内消息 - LXD API",
	}))
}

func UserProfile(c *gin.Context) {
	c.HTML(http.StatusOK, "user/user_profile.html", utils.MergeTemplateData(c, gin.H{
		"title": "个人资料 - LXD API",
	}))
}
