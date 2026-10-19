package models

import "gorm.io/gorm"

// PayConfig 支付网关配置（易支付 / 彩虹易支付等兼容平台）
type PayConfig struct {
	gorm.Model
	Provider    string `gorm:"size:50;not null"` // epay
	Enabled     bool   `gorm:"default:false"`
	APIURL      string `gorm:"size:255"` // 支付网关地址
	PID         string `gorm:"size:100"` // 商户 ID
	Key         string `gorm:"size:255"` // 商户密钥
	SignKey     string `gorm:"size:255"` // 通讯密钥（可选）
	CallbackURL string `gorm:"size:500"` // 异步通知地址（留空自动生成）
	ReturnURL   string `gorm:"size:500"` // 同步跳转地址
}
