package models

import "gorm.io/gorm"

// SystemSetting 系统级配置（SMTP、邮件模板、支付等），KV 存储
type SystemSetting struct {
	gorm.Model
	Key   string `gorm:"uniqueIndex;size:100"`
	Value string `gorm:"type:text"`
	Group string `gorm:"size:50;default:'general'"`
	Desc  string `gorm:"size:255"`
}
