package models

import "time"

import "gorm.io/gorm"

type User struct {
	gorm.Model
	Username          string `gorm:"uniqueIndex;size:255"`
	APIKey            string `gorm:"uniqueIndex;size:255"`
	Status            string `gorm:"size:50;default:'active'"`
	CPUQuota          int
	MemoryQuota       int
	DiskQuota         int
	MaxCPUPerContainer int
	TrafficLimit      int
	TrafficUsed       float64 `gorm:"default:0"`
	TrafficLocked     bool    `gorm:"default:false"`
	IPv4PoolLimit     int
	IPv4MappingLimit  int
	IPv6PoolLimit     int
	IPv6MappingLimit  int
	ReverseProxyLimit int
	Ingress           int
	Egress            int
	CPUAllowance      int
	IORead            int
	IOWrite           int
	ProcessesLimit    int
	AllowNesting      bool
	MemorySwap        bool

	// 用户账号体系（注册 / 邮箱激活 / 找回密码）
	Email         string     `gorm:"size:255;index"`
	PasswordHash  string     `gorm:"size:255"`
	EmailVerified bool       `gorm:"default:false"`
	ActivateToken string     `gorm:"size:100;index"`
	ResetToken    string     `gorm:"size:100;index"`
	ResetExpire   *time.Time
	RegisteredIP  string     `gorm:"size:64"`
	LastLoginAt   *time.Time
}

