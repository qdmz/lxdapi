package models

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
}

