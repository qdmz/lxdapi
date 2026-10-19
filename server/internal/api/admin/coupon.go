package admin

import (
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

// CouponStats 优惠券发放/使用统计
func CouponStats(c *gin.Context) {
	var total, used int64
	db.DB.Model(&models.UserCoupon{}).Where("coupon_id = ?", c.Param("id")).Count(&total)
	db.DB.Model(&models.UserCoupon{}).Where("coupon_id = ? AND status = 'used'", c.Param("id")).Count(&used)
	response.Success(c, gin.H{"total": total, "used": used})
}
