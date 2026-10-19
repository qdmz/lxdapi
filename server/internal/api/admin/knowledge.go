package admin

import (
	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// GetKnowledgeArticles 知识库文章列表（后台）
func GetKnowledgeArticles(c *gin.Context) {
	var articles []models.KnowledgeArticle
	if err := db.DB.Order("sort asc, id desc").Find(&articles).Error; err != nil {
		response.Error(c, 500, "获取失败")
		return
	}
	response.Success(c, articles)
}

// CreateKnowledgeArticle 创建知识库文章
func CreateKnowledgeArticle(c *gin.Context) {
	var a models.KnowledgeArticle
	if err := c.ShouldBindJSON(&a); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if a.Title == "" {
		response.Error(c, 400, "标题不能为空")
		return
	}
	if a.Status == "" {
		a.Status = "published"
	}
	if err := db.DB.Create(&a).Error; err != nil {
		response.Error(c, 500, "创建失败")
		return
	}
	response.Success(c, a)
}

// UpdateKnowledgeArticle 更新知识库文章
func UpdateKnowledgeArticle(c *gin.Context) {
	var a models.KnowledgeArticle
	if err := db.DB.First(&a, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "文章不存在")
		return
	}
	var input models.KnowledgeArticle
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	a.Title = input.Title
	a.Category = input.Category
	a.Content = input.Content
	a.Sort = input.Sort
	a.Status = input.Status
	if err := db.DB.Save(&a).Error; err != nil {
		response.Error(c, 500, "更新失败")
		return
	}
	response.Success(c, a)
}

// DeleteKnowledgeArticle 删除知识库文章
func DeleteKnowledgeArticle(c *gin.Context) {
	if err := db.DB.Delete(&models.KnowledgeArticle{}, c.Param("id")).Error; err != nil {
		response.Error(c, 500, "删除失败")
		return
	}
	response.Success(c, nil)
}
