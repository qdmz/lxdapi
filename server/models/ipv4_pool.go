package models

import "gorm.io/gorm"

type IPv4Pool struct {
	gorm.Model
	NodeID    uint   `gorm:"index" json:"node_id"` // 所属节点，0=全局/未分配
	IPAddress string `gorm:"uniqueIndex;size:50"`
	Interface string `gorm:"size:50"`
	Status    string `gorm:"size:50;default:'available'"`
	Note      string `gorm:"size:255"`
}
