package public

import (
	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// GetKnowledgeArticles 知识库文章列表（公开，只返回已发布的）
func GetKnowledgeArticles(c *gin.Context) {
	var articles []models.KnowledgeArticle
	query := db.DB.Where("status = ?", "published").Order("sort asc, id desc")
	if cat := c.Query("category"); cat != "" {
		query = query.Where("category = ?", cat)
	}
	if err := query.Find(&articles).Error; err != nil {
		response.Error(c, 500, "获取失败")
		return
	}
	// 不返回全文，只返回摘要
	type summary struct {
		ID       uint   `json:"id"`
		Title    string `json:"title"`
		Category string `json:"category"`
		CreatedAt interface{} `json:"created_at"`
	}
	var list []summary
	for _, a := range articles {
		list = append(list, summary{ID: a.ID, Title: a.Title, Category: a.Category, CreatedAt: a.CreatedAt})
	}
	response.Success(c, list)
}

// GetKnowledgeArticle 知识库文章详情（公开）
func GetKnowledgeArticle(c *gin.Context) {
	var a models.KnowledgeArticle
	if err := db.DB.Where("id = ? AND status = ?", c.Param("id"), "published").First(&a).Error; err != nil {
		response.Error(c, 404, "文章不存在")
		return
	}
	response.Success(c, a)
}
