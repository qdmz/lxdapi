package models

import "gorm.io/gorm"

type PaymentOrder struct {
	gorm.Model
	OrderNo     string `gorm:"uniqueIndex;size:50;not null"` // 订单号
	UserID      uint   `gorm:"index;not null"`
	ProductName string `gorm:"size:255"`
	Amount      float64 `gorm:"type:decimal(10,2);not null"`
	PayType     string `gorm:"size:20;default:'epay'"` // epay, alipay, wechat
	Status      string `gorm:"size:20;default:'pending'"` // pending, paid, expired, cancelled
	PayURL      string `gorm:"size:500"`
	NotifyURL   string `gorm:"size:500"`
	ReturnURL   string `gorm:"size:500"`
	PaidAt      *string
	Remark      string `gorm:"size:500"`
}

type PayConfig struct {
	gorm.Model
	Provider   string `gorm:"size:50;not null"` // epay, alipay, wechat
	Enabled    bool   `gorm:"default:false"`
	APIURL     string `gorm:"size:255"`
	PID        string `gorm:"size:100"`
	Key        string `gorm:"size:255"`
	SignKey    string `gorm:"size:255"`
	CallbackURL string `gorm:"size:500"`
}
