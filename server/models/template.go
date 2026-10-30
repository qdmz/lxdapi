package models

import "gorm.io/gorm"

type Template struct {
	gorm.Model
	NodeID       uint   `gorm:"index;uniqueIndex:idx_template_node_fp" json:"node_id"` // 所属节点，0=全局/未分配
	Fingerprint  string `gorm:"uniqueIndex:idx_template_node_fp;size:255"`
	Alias        string `gorm:"index;size:255"`
	Architecture string `gorm:"size:50"`
	Description  string `gorm:"size:500"`
	OS           string `gorm:"size:100"`
	Release      string `gorm:"size:100"`
	Size         int64
	Public       bool
	AutoUpdate   bool
	UploadedAt   string `gorm:"size:100"`
	AllowedUsers string `gorm:"type:text"`
}

type TemplateListResponse struct {
	NodeID       uint   `json:"node_id"`
	Fingerprint  string `json:"fingerprint"`
	Alias        string `json:"alias"`
	Architecture string `json:"architecture"`
	Description  string `json:"description"`
	OS           string `json:"os"`
	Release      string `json:"release"`
	Size         int64  `json:"size"`
	SizeHuman    string `json:"size_human"`
	Public       bool   `json:"public"`
	AutoUpdate   bool   `json:"auto_update"`
	UploadedAt   string `json:"uploaded_at"`
	CreatedAt    string `json:"created_at"`
}

type BatchDeleteTemplateRequest struct {
	Fingerprints []string `json:"fingerprints" binding:"required"`
}
