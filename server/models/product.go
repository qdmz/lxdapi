package models

import "gorm.io/gorm"

type Product struct {
	gorm.Model
	Name        string `gorm:"uniqueIndex;size:255;not null"`
	Description string `gorm:"size:500"`
	Category    string `gorm:"size:50;index"` // kvm, lxc, vm
	Status      string `gorm:"size:20;default:'active'"` // active, inactive

	// 资源规格
	CPU     int `gorm:"default:0"`      // CPU 核心数
	Memory  int `gorm:"default:0"`      // 内存 (MB)
	Disk    int `gorm:"default:0"`      // 磁盘 (GB)
	Bandwidth int `gorm:"default:0"`    // 带宽 (Mbps)
	TrafficLimit int `gorm:"default:0"` // 流量 (GB)

	// 价格配置
	PriceHourly   float64 `gorm:"type:decimal(10,4);default:0"`
	PriceMonthly  float64 `gorm:"type:decimal(10,2);default:0"`
	PriceQuarterly float64 `gorm:"type:decimal(10,2);default:0"`
	PriceYearly   float64 `gorm:"type:decimal(10,2);default:0"`

	// 高级配置
	MaxContainers int `gorm:"default:0"`
	AllowNesting  bool `gorm:"default:false"`
	AllowPortForward bool `gorm:"default:false"`
	AllowDomainBinding bool `gorm:"default:false"`
	AllowSFTP bool `gorm:"default:false"`

	// 同步配置
	AutoSync     bool `gorm:"default:false"`
	LastSyncAt   *string
	SyncStatus   string `gorm:"size:20;default:'synced'"` // synced, pending, failed
}

type Plan struct {
	gorm.Model
	ProductID uint `gorm:"index;not null"`
	Product   Product `gorm:"foreignKey:ProductID"`

	Name        string `gorm:"size:255;not null"`
	Description string `gorm:"size:500"`

	// 资源规格
	CPU     int `gorm:"default:0"`
	Memory  int `gorm:"default:0"`
	Disk    int `gorm:"default:0"`
	Bandwidth int `gorm:"default:0"`
	TrafficLimit int `gorm:"default:0"`

	// 价格
	PriceHourly   float64 `gorm:"type:decimal(10,4);default:0"`
	PriceMonthly  float64 `gorm:"type:decimal(10,2);default:0"`
	PriceQuarterly float64 `gorm:"type:decimal(10,2);default:0"`
	PriceYearly   float64 `gorm:"type:decimal(10,2);default:0"`

	// 限制
	MaxContainers int `gorm:"default:0"`
	AllowNesting  bool
	AllowPortForward bool
	AllowDomainBinding bool
	AllowSFTP bool

	// 状态
	Status string `gorm:"size:20;default:'active'"` // active, inactive
}
