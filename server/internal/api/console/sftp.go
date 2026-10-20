package console

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/console"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/auth"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/response"
)

// CreateSFTPToken 创建 SFTP 访问令牌
// @Summary 创建 SFTP 访问令牌
// @Description 为容器 SFTP 文件管理创建一次性的 WebSocket 访问令牌
// @Tags Console API
// @Accept json
// @Produce json
// @Param request body object{hostname=string} true "容器主机名"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response "参数错误"
// @Failure 403 {object} response.Response "无权访问"
// @Failure 500 {object} response.Response "生成失败"
// @Router /api/console/sftp-token [post]
func CreateSFTPToken(c *gin.Context) {
	var req struct {
		Hostname string `json:"hostname" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	// 令牌只在用户自己的容器上有效，防止越权访问其他用户的文件。
	// 容器级认证（X-Container-Hash）下校验 hostname 与凭证容器一致；
	// 用户级认证下校验 hostname 属于该用户；管理员级放行。
	level, _ := c.Get("auth_level")
	if lvl, isLevel := level.(auth.APILevel); isLevel && lvl == auth.LevelAdmin {
		// 管理员可访问任意容器
	} else if name, ok := c.Get("container_name"); ok {
		if cname, isStr := name.(string); isStr && cname != "" {
			if req.Hostname != cname {
				logger.Warn("SFTP 令牌请求越权: container=%s hostname=%s", cname, req.Hostname)
				response.Error(c, 403, "无权访问该容器")
				return
			}
		}
	} else {
		userID, _ := c.Get("user_id")
		var container models.Container
		var where string
		var args []interface{}
		if uid, ok := userID.(uint); ok {
			where = "name = ? AND user_id = ?"
			args = append(args, req.Hostname, fmt.Sprint(uid))
		} else if uname, ok := c.Get("username"); ok {
			if unameStr, isStr := uname.(string); isStr {
				where = "name = ? AND user_id = ?"
				args = append(args, req.Hostname, unameStr)
			} else {
				response.Error(c, 401, "缺少用户上下文")
				return
			}
		} else {
			response.Error(c, 401, "缺少用户上下文")
			return
		}
		if err := db.DB.Where(where, args...).First(&container).Error; err != nil {
			logger.Warn("SFTP 令牌请求越权: user_id=%s hostname=%s", userID, req.Hostname)
			response.Error(c, 403, "无权访问该容器")
			return
		}
	}

	token, err := console.GenerateTokenWithEndpoint(req.Hostname, EndpointForContainer(req.Hostname))
	if err != nil {
		response.Error(c, 500, "生成令牌失败")
		return
	}

	response.Success(c, gin.H{
		"token": token,
	})
}

// HandleSFTP WebSocket SFTP 文件管理
// @Summary WebSocket SFTP 文件管理连接
// @Description 建立 WebSocket 连接用于容器 SFTP 文件管理（列举、上传、下载、删除、编辑）
// @Tags Console API
// @Accept json
// @Produce json
// @Param token query string true "访问令牌"
// @Router /api/console/sftp/ws [get]
func HandleSFTP(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "缺少令牌参数",
		})
		return
	}

	containerName, endpoint, valid, msg := console.ValidateToken(token)
	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  msg,
		})
		return
	}

	logger.Info("SFTP 连接请求: %s", containerName)

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("WebSocket 升级失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "WebSocket 升级失败: " + err.Error(),
		})
		return
	}
	defer conn.Close()

	// 令牌一次性使用，避免被重放。
	console.ValidateAndConsume(token)

	if err := console.HandleSFTPWithEndpoint(conn, containerName, endpoint); err != nil {
		logger.Error("SFTP 会话异常: %v", err)
	}

	logger.Info("SFTP 会话结束: %s", containerName)
}
