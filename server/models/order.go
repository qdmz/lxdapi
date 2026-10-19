package models

import (
	"time"

	"gorm.io/gorm"
)

// Order 订单：一次产品订购对应一条订单，支付成功后自动开通容器
type Order struct {
	gorm.Model
	OrderNo     string `gorm:"uniqueIndex;size:64;not null" json:"order_no"` // 订单号（对账 / 支付单号）
	UserID      uint   `gorm:"index" json:"user_id"`
	Username    string `gorm:"size:255;index" json:"username"` // 下单用户名
	ProductID   uint   `gorm:"index" json:"product_id"`
	ProductName string `gorm:"size:255" json:"product_name"`
	NodeID      uint   `json:"node_id"`
	NodeName    string `gorm:"size:100" json:"node_name"`
	Image       string `gorm:"size:255" json:"image"` // 下单时的镜像快照

	// 规格快照
	CPU          int `json:"cpu"`
	Memory       int `json:"memory"` // MB
	Disk         int `json:"disk"`   // GB
	TrafficLimit int `json:"traffic_limit"` // GB

	BillingCycle string  `gorm:"size:20;default:'monthly'" json:"billing_cycle"` // hourly, monthly, quarterly, yearly
	Quantity     int     `gorm:"default:1" json:"quantity"`
	Amount       float64 `gorm:"type:decimal(10,2);not null" json:"amount"`

	Status  string `gorm:"size:20;default:'pending'" json:"status"` // pending, paid, provisioning, active, failed, cancelled, expired
	PayType string `gorm:"size:20;default:'alipay'" json:"pay_type"`
	PayURL  string `gorm:"size:1000" json:"pay_url"`
	TradeNo string `gorm:"size:100" json:"trade_no"` // 第三方支付流水号

	ContainerName   string `gorm:"size:255" json:"container_name"`       // 开通后的容器名
	ProvisionStatus string `gorm:"size:20;default:'none'" json:"provision_status"` // none, pending, success, failed
	ProvisionError  string `gorm:"size:500" json:"provision_error"`      // 开通失败原因

	// 续费目标容器：非空表示该订单是续费，支付成功后延长该容器的到期时间，不再新建容器
	RenewalFor string `gorm:"size:255" json:"renewal_for"`

	// 充值：非空表示该订单是余额充值单，支付成功后直接增加用户余额
	RechargeAmount float64 `gorm:"type:decimal(10,2);default:0" json:"recharge_amount"`

	// 支付方式来源：third(第三方网关) / balance(余额抵扣)
	PaySource string `gorm:"size:20;default:'third'" json:"pay_source"`

	PaidAt   *time.Time `json:"paid_at"`
	ExpireAt *time.Time `json:"expire_at"`
	Remark   string `gorm:"size:500" json:"remark"`
}
