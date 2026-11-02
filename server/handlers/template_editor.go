package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"lxdapi/pkg/utils"
)

// AdminTemplateEditor 在线模板编辑器页面（方案 C）
func AdminTemplateEditor(c *gin.Context) {
	c.HTML(http.StatusOK, "admin/template_editor.html", utils.MergeTemplateData(c, gin.H{
		"title":    "模板编辑器 - LXD API 管理",
		"username": "admin",
	}))
}
