package models

import "gorm.io/gorm"

// Node 节点（机房 / 宿主机），产品归属到节点，下单开通时按节点默认参数创建容器
type Node struct {
	gorm.Model
	Name        string `gorm:"uniqueIndex;size:100;not null" json:"name"`
	Region      string `gorm:"size:100" json:"region"`        // 机房名称（与 RegionID 同步，兼容旧数据）
	RegionID    uint   `gorm:"index;default:0" json:"region_id"` // 所属机房
	Host        string `gorm:"size:255" json:"host"`
	APIPort     int    `gorm:"default:0" json:"api_port"`
	Image       string `gorm:"size:255" json:"image"`
	StoragePool string `gorm:"size:100" json:"storage_pool"`
	Network     string `gorm:"size:100" json:"network"`
	Status      string `gorm:"size:20;default:'online'" json:"status"`
	Sort        int    `gorm:"default:0" json:"sort"`
	Remark      string `gorm:"size:500" json:"remark"`
}
