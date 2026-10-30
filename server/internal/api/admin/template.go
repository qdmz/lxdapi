package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/internal/service"
	"lxdapi/models"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/response"
)

// GetTemplateList 获取模板列表
// @Summary 获取模板列表
// @Description 获取所有容器模板
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Success 200 {object} response.Response "获取成功"
// @Failure 500 {object} response.Response "获取失败"
// @Security SessionAuth
// @Router /api/admin/templates [get]
func GetTemplateList(c *gin.Context) {
	svc := service.NewTemplateService()
	nodeID := parseNodeIDQuery(c)

	templates, err := svc.List(nodeID)
	if err != nil {
		logger.Error("获取模板列表失败: %v", err)
		response.Error(c, 500, "获取模板列表失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"templates": templates,
		"count":     len(templates),
	})
}

// SyncTemplates 同步模板
// @Summary 同步模板
// @Description 从LXD同步模板到数据库
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Success 200 {object} response.Response "同步成功"
// @Failure 500 {object} response.Response "同步失败"
// @Security SessionAuth
// @Router /api/admin/templates/sync [post]
func SyncTemplates(c *gin.Context) {
	svc := service.NewTemplateService()
	ctx := c.Request.Context()

	logger.Info("开始同步模板")

	nodeID := parseNodeIDQuery(c)
	added, updated, deleted, err := svc.SyncFromLXD(ctx, nodeID)
	if err != nil {
		logger.Error("同步模板失败: %v", err)
		response.Error(c, 500, "同步模板失败: "+err.Error())
		return
	}

	logger.OK("模板同步完成")
	response.Success(c, gin.H{
		"added":   added,
		"updated": updated,
		"deleted": deleted,
		"message": "模板同步完成",
	})
}

// GetRemoteImages 获取指定节点可从远程镜像仓库下载的系统镜像列表
// @Summary 远程可下载镜像列表
// @Description 按 node_id 路由到目标节点，列出其 LXD 远程源中可下载的系统（按发行版去重、易读名，每条带来源 remote）
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Param node_id query int false "节点ID（0/缺省=本机节点）"
// @Param remote query string false "远程源名，传了只查该源；缺省合并 images 与 ubuntu 两源"
// @Success 200 {object} response.Response "获取成功"
// @Failure 500 {object} response.Response "获取失败"
// @Security SessionAuth
// @Router /api/admin/remote-images [get]
func GetRemoteImages(c *gin.Context) {
	nodeID := parseNodeIDQuery(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 300*time.Second)
	defer cancel()

	svc := service.NewTemplateService()
	images, err := svc.ListRemoteImages(ctx, nodeID, c.Query("remote"))
	if err != nil {
		logger.Error("获取远程镜像列表失败: %v", err)
		response.Error(c, 500, "获取远程镜像列表失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"images": images,
		"count":  len(images),
	})
}

// DownloadRemoteImage 下载远程镜像到指定节点并登记为模板
// @Summary 下载远程镜像为模板
// @Description 把远程镜像仓库中的一个系统镜像下载到指定节点，完成后自动同步模板库
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Param request body object true "node_id 节点ID, remote 远程源名(传列表条目自带的 remote, 缺省images), image 远程别名(如 ubuntu/24.04), alias 落地模板别名(可空,自动推导)"
// @Success 200 {object} response.Response "下载成功"
// @Failure 400 {object} response.Response "参数错误"
// @Failure 500 {object} response.Response "下载失败"
// @Security SessionAuth
// @Router /api/admin/templates/download [post]
func DownloadRemoteImage(c *gin.Context) {
	var req struct {
		NodeID uint   `json:"node_id"`
		Remote string `json:"remote"`
		Image  string `json:"image" binding:"required"`
		Alias  string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 900*time.Second)
	defer cancel()

	svc := service.NewTemplateService()
	added, updated, deleted, err := svc.DownloadRemoteImage(ctx, req.NodeID, req.Remote, req.Image, req.Alias)
	if err != nil {
		logger.Error("下载远程镜像失败: %v", err)
		response.Error(c, 500, err.Error())
		return
	}

	logger.OK("远程镜像下载完成: %s (节点 %d)", req.Image, req.NodeID)
	response.Success(c, gin.H{
		"added":   added,
		"updated": updated,
		"deleted": deleted,
		"message": "镜像下载完成，已登记为模板",
	})
}

// DeleteTemplate 删除模板（统一接口）
// @Summary 删除模板
// @Description 删除指定模板，支持单个和批量删除
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Param fingerprint path string false "模板指纹（单个删除）"
// @Param fingerprints query string false "模板指纹列表，逗号分隔（批量删除）"
// @Success 200 {object} response.Response "删除成功"
// @Failure 400 {object} response.Response "缺少参数"
// @Failure 500 {object} response.Response "删除失败"
// @Security SessionAuth
// @Router /api/admin/templates/:fingerprint [delete]
func DeleteTemplate(c *gin.Context) {
	fingerprint := c.Param("fingerprint")

	if fingerprint != "" {
		svc := service.NewTemplateService()
		ctx := c.Request.Context()

		if err := svc.Delete(ctx, fingerprint); err != nil {
			logger.Error("删除模板失败: %v", err)
			response.Error(c, 500, "删除模板失败: "+err.Error())
			return
		}

		logger.OK("模板删除成功: %s", fingerprint)
		response.Success(c, "模板删除成功")
		return
	}

	fp := c.Query("fingerprint")
	if fp != "" {
		svc := service.NewTemplateService()
		ctx := c.Request.Context()

		if err := svc.Delete(ctx, fp); err != nil {
			logger.Error("删除模板失败: %v", err)
			response.Error(c, 500, "删除模板失败: "+err.Error())
			return
		}

		logger.OK("模板删除成功: %s", fp)
		response.Success(c, "模板删除成功")
		return
	}

	response.Error(c, 400, "缺少fingerprint参数")
}

// BatchDeleteTemplates 批量删除模板
// @Summary 批量删除模板
// @Description 批量删除多个模板
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Param request body models.BatchDeleteTemplateRequest true "模板指纹列表"
// @Success 200 {object} response.Response "删除成功"
// @Failure 400 {object} response.Response "参数错误"
// @Failure 500 {object} response.Response "删除失败"
// @Security SessionAuth
// @Router /api/admin/templates/batch-delete [post]
func BatchDeleteTemplates(c *gin.Context) {
	var req models.BatchDeleteTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}

	if len(req.Fingerprints) == 0 {
		response.Error(c, 400, "请选择要删除的模板")
		return
	}

	svc := service.NewTemplateService()
	ctx := c.Request.Context()

	logger.Info("批量删除模板，数量: %d", len(req.Fingerprints))

	success, failed, err := svc.BatchDelete(ctx, req.Fingerprints)
	if err != nil {
		logger.Error("批量删除模板失败: %v", err)
		response.Error(c, 500, "批量删除模板失败: "+err.Error())
		return
	}

	logger.OK("批量删除完成: 成功 %d, 失败 %d", success, failed)
	response.Success(c, gin.H{
		"deleted_count": success,
		"failed":        failed,
		"message":       "批量删除完成",
	})
}

// SetTemplatePermission 设置模板用户权限
// @Summary 设置模板用户权限
// @Description 设置哪些用户可以使用该模板
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Param fingerprint path string true "模板指纹"
// @Param request body object{allowed_users=[]string} true "允许使用的用户名列表"
// @Success 200 {object} response.Response "设置成功"
// @Failure 400 {object} response.Response "参数错误"
// @Failure 404 {object} response.Response "模板不存在"
// @Failure 500 {object} response.Response "设置失败"
// @Security SessionAuth
// @Router /api/admin/templates/:fingerprint/permission [put]
func SetTemplatePermission(c *gin.Context) {
	fingerprint := c.Param("fingerprint")
	if fingerprint == "" {
		response.Error(c, 400, "缺少模板指纹")
		return
	}

	var req struct {
		AllowedUsers []string `json:"allowed_users"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}

	var template models.Template
	if err := db.DB.Where("fingerprint = ?", fingerprint).First(&template).Error; err != nil {
		response.Error(c, 404, "模板不存在")
		return
	}

	allowedUsersJSON := ""
	if len(req.AllowedUsers) > 0 {
		jsonBytes, _ := json.Marshal(req.AllowedUsers)
		allowedUsersJSON = string(jsonBytes)
	}

	if err := db.DB.Model(&template).Update("allowed_users", allowedUsersJSON).Error; err != nil {
		logger.Error("设置模板权限失败: %v", err)
		response.Error(c, 500, "设置失败")
		return
	}

	logger.OK("模板权限设置成功: %s, 用户: %v", fingerprint, req.AllowedUsers)
	response.Success(c, gin.H{
		"fingerprint":   fingerprint,
		"allowed_users": req.AllowedUsers,
	})
}

// GetTemplatePermission 获取模板用户权限
// @Summary 获取模板用户权限
// @Description 获取模板的用户权限设置
// @Tags Admin API - 模板管理
// @Accept json
// @Produce json
// @Param fingerprint path string true "模板指纹"
// @Success 200 {object} response.Response "获取成功"
// @Failure 404 {object} response.Response "模板不存在"
// @Security SessionAuth
// @Router /api/admin/templates/:fingerprint/permission [get]
func GetTemplatePermission(c *gin.Context) {
	fingerprint := c.Param("fingerprint")
	if fingerprint == "" {
		response.Error(c, 400, "缺少模板指纹")
		return
	}

	var template models.Template
	if err := db.DB.Where("fingerprint = ?", fingerprint).First(&template).Error; err != nil {
		response.Error(c, 404, "模板不存在")
		return
	}

	var allowedUsers []string
	if template.AllowedUsers != "" {
		json.Unmarshal([]byte(template.AllowedUsers), &allowedUsers)
	}

	response.Success(c, gin.H{
		"fingerprint":   fingerprint,
		"allowed_users": allowedUsers,
	})
}
