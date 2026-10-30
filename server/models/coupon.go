package models

import (
	"time"

	"gorm.io/gorm"
)

// Coupon 优惠券
type Coupon struct {
	gorm.Model
	Code         string     `gorm:"uniqueIndex;size:50;not null" json:"code"`         // 券码
	Name         string     `gorm:"size:100;not null" json:"name"`                    // 券名称
	Type         string     `gorm:"size:20;default:'fixed'" json:"type"`              // fixed=固定金额, percent=百分比
	Value        float64    `gorm:"type:decimal(10,2);default:0" json:"value"`        // 面值（fixed=元，percent=折扣率如80=8折）
	MinAmount    float64    `gorm:"type:decimal(10,2);default:0" json:"min_amount"`   // 最低订单金额
	MaxDiscount  float64    `gorm:"type:decimal(10,2);default:0" json:"max_discount"` // percent类型最高抵扣
	TotalCount   int        `gorm:"default:0" json:"total_count"`                     // 全站可领取总次数，0=不限
	UsedCount    int        `gorm:"default:0" json:"used_count"`                      // 已使用数量
	PerUserLimit int        `gorm:"default:1" json:"per_user_limit"`                  // 每用户可领取次数，0=不限；每条领取记录仅可核销一次
	StartAt      *time.Time `json:"start_at"`                                         // 生效时间
	EndAt        *time.Time `json:"end_at"`                                           // 过期时间
	Status       string     `gorm:"size:20;default:'active'" json:"status"`           // active, disabled, expired
	Remark       string     `gorm:"size:500" json:"remark"`
}

// UserCoupon 用户优惠券
type UserCoupon struct {
	gorm.Model
	UserID   uint       `gorm:"index;not null" json:"user_id"`
	CouponID uint       `gorm:"index;not null" json:"coupon_id"`
	Coupon   Coupon     `gorm:"foreignKey:CouponID" json:"coupon"`
	Status   string     `gorm:"size:20;default:'unused'" json:"status"` // unused, used, expired
	UsedAt   *time.Time `json:"used_at"`
	OrderID  uint       `gorm:"index" json:"order_id"` // 使用的订单
}
