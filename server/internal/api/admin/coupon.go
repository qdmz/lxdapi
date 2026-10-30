package admin

import (
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// ListCoupons 优惠券列表
func ListCoupons(c *gin.Context) {
	var coupons []models.Coupon
	if err := db.DB.Order("id DESC").Find(&coupons).Error; err != nil {
		response.Error(c, 500, "获取失败")
		return
	}
	response.Success(c, coupons)
}

// CreateCoupon 创建优惠券
func CreateCoupon(c *gin.Context) {
	var coupon models.Coupon
	if err := c.ShouldBindJSON(&coupon); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if coupon.Code == "" || coupon.Name == "" {
		response.Error(c, 400, "券码和名称必填")
		return
	}
	if err := db.DB.Create(&coupon).Error; err != nil {
		response.Error(c, 500, "创建失败（券码可能重复）")
		return
	}
	response.Success(c, coupon)
}

// UpdateCoupon 更新优惠券
func UpdateCoupon(c *gin.Context) {
	var coupon models.Coupon
	if err := db.DB.First(&coupon, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "优惠券不存在")
		return
	}
	var req models.Coupon
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	req.ID = coupon.ID
	if err := db.DB.Save(&req).Error; err != nil {
		response.Error(c, 500, "更新失败")
		return
	}
	response.Success(c, req)
}

// DeleteCoupon 删除优惠券
func DeleteCoupon(c *gin.Context) {
	if err := db.DB.Delete(&models.Coupon{}, c.Param("id")).Error; err != nil {
		response.Error(c, 500, "删除失败")
		return
	}
	response.Success(c, gin.H{"message": "已删除"})
}

// 优惠券次数语义（务必区分，避免把两个限额混为一谈）：
//   - Coupon.TotalCount：整张券在全站可被领取的总次数上限，0=不限；
//     领取动作在 user.ClaimCoupon 里按全站已领取记录数校验。
//   - Coupon.PerUserLimit：同一用户可领取该券的次数上限（默认 1），0=不限；
//     领取时按该用户已领取记录数校验。
//   - 每条领取记录（UserCoupon）只能核销一次：下单核销后该记录置 used 并
//     绑定订单，不可再次使用。因此「每人可用次数 = 每人可领取次数」，
//     「全站可用次数 = 全站可领取总次数」；不存在一张记录多人/多次共用的情况。

// CouponStats 优惠券发放/使用统计
// GET /api/admin/coupons/:id/stats
// 返回：claimed 领取总数、unused 未使用数、used 已使用数、expired 已过期数、
// used_users 实际使用过的不同用户数、total_discount 累计抵扣金额（元，
// 按订单表统计，不限订单状态）、total_count/per_user_limit 券本身的限额配置。
func CouponStats(c *gin.Context) {
	var coupon models.Coupon
	if err := db.DB.First(&coupon, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "优惠券不存在")
		return
	}
	countByStatus := func(status string) int64 {
		var n int64
		q := db.DB.Model(&models.UserCoupon{}).Where("coupon_id = ?", coupon.ID)
		if status != "" {
			q = q.Where("status = ?", status)
		}
		q.Count(&n)
		return n
	}
	claimed := countByStatus("")
	unused := countByStatus("unused")
	used := countByStatus("used")
	expired := countByStatus("expired")

	var usedUsers int64
	db.DB.Raw("SELECT COUNT(DISTINCT user_id) FROM user_coupons WHERE coupon_id = ? AND status = 'used' AND deleted_at IS NULL", coupon.ID).Scan(&usedUsers)

	var totalDiscount float64
	db.DB.Model(&models.Order{}).Where("coupon_id = ?", coupon.ID).
		Select("COALESCE(SUM(discount_amount), 0)").Scan(&totalDiscount)

	response.Success(c, gin.H{
		"claimed":        claimed,
		"total":          claimed, // 兼容旧字段：领取总数
		"unused":         unused,
		"used":           used,
		"expired":        expired,
		"used_users":     usedUsers,
		"total_discount": totalDiscount,
		"total_count":    coupon.TotalCount,
		"per_user_limit": coupon.PerUserLimit,
	})
}

// CouponRecord 优惠券领取/使用记录（后台明细）
type CouponRecord struct {
	ID             uint       `json:"id"`
	UserID         uint       `json:"user_id"`
	Username       string     `json:"username"`
	Status         string     `json:"status"` // unused, used, expired
	CreatedAt      time.Time  `json:"created_at"`
	UsedAt         *time.Time `json:"used_at"`
	OrderID        uint       `json:"order_id"`
	OrderNo        string     `json:"order_no"`
	DiscountAmount float64    `json:"discount_amount"`
}

// CouponRecords 优惠券全部领取/使用记录（按 id 倒序）
// GET /api/admin/coupons/:id/records
// 每行对应一条领取记录：谁领的、何时领的、是否已用、何时用的、用在哪个订单、抵扣了多少。
func CouponRecords(c *gin.Context) {
	var coupon models.Coupon
	if err := db.DB.First(&coupon, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "优惠券不存在")
		return
	}
	records := make([]CouponRecord, 0)
	err := db.DB.Table("user_coupons AS uc").
		Select("uc.id AS id, uc.user_id AS user_id, COALESCE(u.username, '') AS username, "+
			"uc.status AS status, uc.created_at AS created_at, uc.used_at AS used_at, "+
			"uc.order_id AS order_id, COALESCE(o.order_no, '') AS order_no, "+
			"COALESCE(o.discount_amount, 0) AS discount_amount").
		Joins("LEFT JOIN users u ON u.id = uc.user_id").
		Joins("LEFT JOIN orders o ON o.id = uc.order_id").
		Where("uc.coupon_id = ? AND uc.deleted_at IS NULL", coupon.ID).
		Order("uc.id DESC").
		Scan(&records).Error
	if err != nil {
		response.Error(c, 500, "获取记录失败")
		return
	}
	response.Success(c, records)
}
