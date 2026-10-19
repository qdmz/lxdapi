package models

import "gorm.io/gorm"

// KnowledgeArticle 知识库文章
type KnowledgeArticle struct {
	gorm.Model
	Title    string `gorm:"size:200;not null" json:"title"`
	Category string `gorm:"size:100;index" json:"category"`
	Content  string `gorm:"type:text" json:"content"`
	Sort     int    `gorm:"default:0" json:"sort"`
	Status   string `gorm:"size:20;default:'published'" json:"status"` // published / draft
}
