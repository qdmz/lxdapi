package middleware

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"lxdapi/internal/core"
	"lxdapi/internal/service"
	"lxdapi/pkg/auth"
	"lxdapi/pkg/response"
)

func SystemAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiHash := c.GetHeader("X-API-Hash")
		if apiHash != core.GlobalConfig.System.Security.APIHash {
			response.Error(c, 401, "系统级认证失败")
			c.Abort()
			return
		}
		c.Set("auth_level", auth.LevelSystem)
		c.Next()
	}
}

func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		loggedIn := session.Get("admin_logged_in")

		if loggedIn == nil || loggedIn != true {
			response.Error(c, 401, "未登录或登录已过期，请重新登录")
			c.Abort()
			return
		}

		username := session.Get("admin_username")
		c.Set("auth_level", auth.LevelAdmin)
		c.Set("admin_username", username)
		c.Next()
	}
}

func AdminPageAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		loggedIn := session.Get("admin_logged_in")

		if loggedIn == nil || loggedIn != true {
			// 系统级 access-token 免密跳转（/api/system/admin/access-token 签发）：
			// 此前 token 校验写在页面 handler 里，但未登录请求先被本中间件
			// 重定向到登录页，handler 永远执行不到，jump_url 实际不可用。
			// 改为在中间件里校验 token 并建立会话。
			if token := c.Query("token"); token != "" {
				if at, err := service.ValidateAccessToken(token); err == nil && at.Type == "admin" {
					session.Set("admin_logged_in", true)
					session.Set("admin_username", at.Target)
					_ = session.Save()
					c.Next()
					return
				}
			}
			c.Redirect(302, "/admin/login")
			c.Abort()
			return
		}

		c.Next()
	}
}

func UserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		loggedIn := session.Get("user_logged_in")

		if loggedIn == nil || loggedIn != true {
			response.Error(c, 401, "未登录或登录已过期，请重新登录")
			c.Abort()
			return
		}

		username := session.Get("user_username")
		if username == nil {
			response.Error(c, 401, "会话无效，请重新登录")
			c.Abort()
			return
		}

		user, err := service.GetUserByUsername(username.(string))
		if err != nil || user.Status == "disabled" {
			session.Clear()
			session.Save()
			response.Error(c, 403, "账户已被禁用")
			c.Abort()
			return
		}

		c.Set("auth_level", auth.LevelUser)
		c.Set("username", username.(string))
		c.Set("user_id", user.ID)
		c.Next()
	}
}

func UserBasicAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		username := c.GetHeader("X-User-Username")
		password := c.GetHeader("X-User-Password")

		if username == "" || password == "" {
			response.Error(c, 401, "缺少用户认证信息")
			c.Abort()
			return
		}

		user, err := service.GetUserByUsernameAndAPIKey(username, password)
		if err != nil {
			response.Error(c, 401, err.Error())
			c.Abort()
			return
		}

		c.Set("auth_level", auth.LevelUser)
		c.Set("username", user.Username)
		c.Next()
	}
}

func UserAuthOrBasic() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-User-Username") != "" {
			UserBasicAuth()(c)
		} else {
			UserAuth()(c)
		}
	}
}

func UserPageAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		loggedIn := session.Get("user_logged_in")

		if loggedIn == nil || loggedIn != true {
			// 同 AdminPageAuth：支持系统级 user access-token 免密跳转建会话
			if token := c.Query("token"); token != "" {
				if at, err := service.ValidateAccessToken(token); err == nil && at.Type == "user" {
					session.Set("user_logged_in", true)
					session.Set("user_username", at.Target)
					_ = session.Save()
					loggedIn = true
				}
			}
		}
		if loggedIn == nil || loggedIn != true {
			c.Redirect(302, "/user/login")
			c.Abort()
			return
		}

		username := session.Get("user_username")
		if username != nil {
			user, err := service.GetUserByUsername(username.(string))
			if err != nil || user.Status == "disabled" {
				session.Clear()
				session.Save()
				c.Redirect(302, "/user/login?error=disabled")
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

func ContainerAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		containerHash := c.GetHeader("X-Container-Hash")
		if containerHash == "" {
			response.Error(c, 401, "缺少容器凭证")
			c.Abort()
			return
		}

		cred, err := service.GetContainerByHash(containerHash)
		if err != nil {
			response.Error(c, 401, "容器级认证失败")
			c.Abort()
			return
		}

		c.Set("auth_level", auth.LevelContainer)
		c.Set("container_name", cred.ContainerName)
		c.Next()
	}
}
