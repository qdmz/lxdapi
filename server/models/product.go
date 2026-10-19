package models

import "gorm.io/gorm"

type Product struct {
	gorm.Model
	Name        string `gorm:"uniqueIndex;size:255;not null" json:"name"`
	Description string `gorm:"size:500" json:"description"`
	Category    string `gorm:"size:50;index" json:"category"` // kvm, lxc, vm
	Status      string `gorm:"size:20;default:'active'" json:"status"`

	// 所属节点 / 镜像 / 机房
	NodeID   uint   `gorm:"index" json:"node_id"`
	RegionID uint   `gorm:"index;default:0" json:"region_id"` // 所属机房（随节点同步）
	Image  string `gorm:"size:255" json:"image"`    // 开通使用的镜像，留空则用节点默认镜像
	Stock  int    `gorm:"default:-1" json:"stock"`  // 库存，-1 表示不限
	Sort   int    `gorm:"default:0" json:"sort"`    // 排序

	// 资源规格
	CPU          int `gorm:"default:0" json:"cpu"`             // CPU 核心数
	Memory       int `gorm:"default:0" json:"memory"`          // 内存 (MB)
	Disk         int `gorm:"default:0" json:"disk"`            // 磁盘 (GB)
	Bandwidth    int `gorm:"default:0" json:"bandwidth"`       // 带宽 (Mbps)
	TrafficLimit int `gorm:"default:0" json:"traffic_limit"`   // 流量 (GB)

	// 价格配置
	PriceHourly    float64 `gorm:"type:decimal(10,4);default:0" json:"price_hourly"`
	PriceMonthly   float64 `gorm:"type:decimal(10,2);default:0" json:"price_monthly"`
	PriceQuarterly float64 `gorm:"type:decimal(10,2);default:0" json:"price_quarterly"`
	PriceYearly    float64 `gorm:"type:decimal(10,2);default:0" json:"price_yearly"`

	// 高级配置
	MaxContainers      int  `gorm:"default:0" json:"max_containers"`
	AllowNesting       bool `gorm:"default:false" json:"allow_nesting"`
	AllowPortForward   bool `gorm:"default:false" json:"allow_port_forward"`
	AllowDomainBinding bool `gorm:"default:false" json:"allow_domain_binding"`
	AllowSFTP          bool `gorm:"default:false" json:"allow_sftp"`

	// 购买限制与费用
	MaxPerUser int     `gorm:"default:0" json:"max_per_user"`   // 每用户限购数量，0=不限
	AllowFree  bool    `gorm:"default:false" json:"allow_free"` // 是否允许0元开通
	SetupFee   float64 `gorm:"type:decimal(10,2);default:0" json:"setup_fee"` // 一次性设置费

	// 同步配置
	AutoSync   bool   `gorm:"default:false" json:"auto_sync"`
	LastSyncAt *string `json:"last_sync_at"`
	SyncStatus string `gorm:"size:20;default:'synced'" json:"sync_status"` // synced, pending, failed
}

type Plan struct {
	gorm.Model
	ProductID uint    `gorm:"index;not null"`
	Product   Product `gorm:"foreignKey:ProductID"`

	Name        string `gorm:"size:255;not null"`
	Description string `gorm:"size:500"`

	// 资源规格
	CPU          int `gorm:"default:0"`
	Memory       int `gorm:"default:0"`
	Disk         int `gorm:"default:0"`
	Bandwidth    int `gorm:"default:0"`
	TrafficLimit int `gorm:"default:0"`

	// 价格
	PriceHourly    float64 `gorm:"type:decimal(10,4);default:0"`
	PriceMonthly   float64 `gorm:"type:decimal(10,2);default:0"`
	PriceQuarterly float64 `gorm:"type:decimal(10,2);default:0"`
	PriceYearly    float64 `gorm:"type:decimal(10,2);default:0"`

	// 限制
	MaxContainers      int `gorm:"default:0"`
	AllowNesting       bool
	AllowPortForward   bool
	AllowDomainBinding bool
	AllowSFTP          bool

	// 状态
	Status string `gorm:"size:20;default:'active'"` // active, inactive
}
