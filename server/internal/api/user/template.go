package user

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	noderesolver "lxdapi/internal/node"
	"lxdapi/internal/service"
	"lxdapi/models"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/response"
)

// GetTemplateList 获取模板列表
// @Summary 获取模板列表
// @Description 获取当前用户可用的容器模板列表。可传 container=<容器名>（或 node_id）按容器
// 所属节点过滤：重装系统时必须用容器所在节点的镜像，混用其它节点镜像必然开通/重装失败。
// 该节点在 templates 表里没有记录时，自动从节点 LXD 同步一次；仍为空则直接读节点实时镜像列表。
// @Tags User API - 模板管理
// @Accept json
// @Produce json
// @Success 200 {object} response.Response "获取成功"
// @Failure 500 {object} response.Response "获取失败"
// @Security UserSession
// @Router /api/user/templates [get]
func GetTemplateList(c *gin.Context) {
	username, _ := c.Get("username")

	// 解析目标节点：优先按容器归属，其次显式 node_id；都没传则保持旧行为（全部模板）。
	nodeID := uint(0)
	hasNode := false
	if name := c.Query("container"); name != "" {
		var container models.Container
		if err := db.DB.Where("name = ?", name).First(&container).Error; err == nil {
			if container.UserID != username.(string) {
				response.Error(c, 403, "无权访问该容器")
				return
			}
			nodeID = container.NodeID
			hasNode = true
		}
	} else if v := c.Query("node_id"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			nodeID = uint(n)
			hasNode = true
		}
	}

	queryTemplates := func() []models.Template {
		q := db.DB.Order("created_at DESC")
		if hasNode {
			if nodeID <= 1 {
				q = q.Where("node_id IN (0, 1)")
			} else {
				q = q.Where("node_id = ?", nodeID)
			}
		}
		var templates []models.Template
		if err := q.Find(&templates).Error; err != nil {
			logger.Error("获取模板列表失败: %v", err)
			return nil
		}
		return templates
	}

	templates := queryTemplates()

	// 指定了节点但该节点还没有模板记录：先从该节点 LXD 自动同步一次再查。
	if hasNode && len(templates) == 0 {
		svc := service.NewTemplateService()
		if _, _, _, err := svc.SyncFromLXD(c.Request.Context(), nodeID); err != nil {
			logger.Warn("按需同步节点 %d 模板失败（将回退实时列表）: %v", nodeID, err)
		} else {
			templates = queryTemplates()
		}
	}

	allowed := func(t models.Template) bool {
		if t.AllowedUsers == "" || t.AllowedUsers == "[]" {
			return true
		}
		var users []string
		if err := json.Unmarshal([]byte(t.AllowedUsers), &users); err != nil || len(users) == 0 {
			return true
		}
		for _, u := range users {
			if u == username.(string) {
				return true
			}
		}
		return false
	}

	result := make([]map[string]interface{}, 0)
	for _, t := range templates {
		if !allowed(t) {
			continue
		}
		display := t.Alias
		if display == "" && t.OS != "" {
			display = strings.TrimSpace(t.OS + " " + t.Release)
		}
		if display == "" {
			display = t.Description
		}
		result = append(result, map[string]interface{}{
			"node_id":      t.NodeID,
			"fingerprint":  t.Fingerprint,
			"alias":        t.Alias,
			"display_name": display,
			"architecture": t.Architecture,
			"description":  t.Description,
			"os":           t.OS,
			"release":      t.Release,
			"size":         t.Size,
			"public":       t.Public,
			"auto_update":  t.AutoUpdate,
			"uploaded_at":  t.UploadedAt,
		})
	}

	// 同步后仍为空：直接读该节点的实时镜像列表兜底，保证重装永远有该节点的真实可用镜像。
	if hasNode && len(result) == 0 {
		client := noderesolver.ClientForNode(nodeID)
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
		defer cancel()
		if images, err := client.ListImages(ctx); err == nil {
			for _, img := range images {
				alias := ""
				if len(img.Aliases) > 0 {
					alias = img.Aliases[0].Name
				}
				prop := func(k string) string {
					if v, ok := img.Properties[k].(string); ok {
						return v
					}
					return ""
				}
				result = append(result, map[string]interface{}{
					"node_id":      nodeID,
					"fingerprint":  img.Fingerprint,
					"alias":        alias,
					"display_name": img.DisplayName,
					"architecture": img.Architecture,
					"description":  prop("description"),
					"os":           prop("os"),
					"release":      prop("release"),
					"size":         img.Size,
					"public":       img.Public,
					"auto_update":  img.AutoUpdate,
					"uploaded_at":  img.UploadedAt,
				})
			}
		} else {
			logger.Warn("读取节点 %d 实时镜像失败: %v", nodeID, err)
		}
	}

	response.Success(c, result)
}
