package models

import "gorm.io/gorm"

type StoragePool struct {
	gorm.Model
	NodeID      uint   `gorm:"index;uniqueIndex:idx_pool_node_name" json:"node_id"` // 所属节点，0=全局/未分配
	Name        string `gorm:"size:100;uniqueIndex:idx_pool_node_name"`             // 与 NodeID 复合唯一：不同节点可有同名池（如 default）
	Driver      string `gorm:"size:50"`
	Description string `gorm:"size:255"`
	Status      string `gorm:"size:50"`
	UsedBy      int
	TotalSpace  int64
	UsedSpace   int64
	Priority    int `gorm:"default:0"` // 0=禁用, 数字越小优先级越高
}
