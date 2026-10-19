package user

import (
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// MyCoupons 我的优惠券
func MyCoupons(c *gin.Context) {
	userID := c.GetUint("user_id")
	var list []models.UserCoupon
	db.DB.Preload("Coupon").Where("user_id = ?", userID).Order("id DESC").Find(&list)
	// 标记过期的
	now := time.Now()
	for i := range list {
		if list[i].Status == "unused" && list[i].Coupon.EndAt != nil && now.After(*list[i].Coupon.EndAt) {
			list[i].Status = "expired"
			db.DB.Model(&list[i]).Update("status", "expired")
		}
	}
	response.Success(c, list)
}

// ClaimCoupon 领取优惠券（通过券码）
func ClaimCoupon(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "请输入券码")
		return
	}
	var coupon models.Coupon
	if err := db.DB.Where("code = ? AND status = 'active'", req.Code).First(&coupon).Error; err != nil {
		response.Error(c, 404, "券码无效或已停用")
		return
	}
	now := time.Now()
	if coupon.StartAt != nil && now.Before(*coupon.StartAt) {
		response.Error(c, 400, "优惠券尚未生效")
		return
	}
	if coupon.EndAt != nil && now.After(*coupon.EndAt) {
		response.Error(c, 400, "优惠券已过期")
		return
	}
	// 检查每用户限领
	if coupon.PerUserLimit > 0 {
		var cnt int64
		db.DB.Model(&models.UserCoupon{}).Where("user_id = ? AND coupon_id = ?", userID, coupon.ID).Count(&cnt)
		if cnt >= int64(coupon.PerUserLimit) {
			response.Error(c, 400, "已达领取上限")
			return
		}
	}
	// 检查总量
	if coupon.TotalCount > 0 {
		var cnt int64
		db.DB.Model(&models.UserCoupon{}).Where("coupon_id = ?", coupon.ID).Count(&cnt)
		if cnt >= int64(coupon.TotalCount) {
			response.Error(c, 400, "优惠券已领完")
			return
		}
	}
	uc := models.UserCoupon{UserID: userID, CouponID: coupon.ID, Status: "unused"}
	if err := db.DB.Create(&uc).Error; err != nil {
		response.Error(c, 500, "领取失败")
		return
	}
	db.DB.Preload("Coupon").First(&uc, uc.ID)
	response.Success(c, uc)
}

// AvailableCoupons 下单时可用的优惠券
func AvailableCoupons(c *gin.Context) {
	userID := c.GetUint("user_id")
	amount := c.Query("amount")
	var list []models.UserCoupon
	db.DB.Preload("Coupon").Where("user_id = ? AND status = 'unused'", userID).Find(&list)
	// 过滤：未过期、满足最低金额
	now := time.Now()
	var result []models.UserCoupon
	for _, uc := range list {
		cp := uc.Coupon
		if cp.Status != "active" {
			continue
		}
		if cp.EndAt != nil && now.After(*cp.EndAt) {
			continue
		}
		if cp.StartAt != nil && now.Before(*cp.StartAt) {
			continue
		}
		// TODO: 按 amount 过滤最低金额
		_ = amount
		result = append(result, uc)
	}
	response.Success(c, result)
}
