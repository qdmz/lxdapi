package service

import (
	"fmt"
	"math/rand"
	"time"

	"gorm.io/gorm"

	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/logger"
)

// AutoRenewLead 自动续费提前触发窗口：到期前 N 小时内尝试续费
const AutoRenewLead = 24 * time.Hour

func genOrderNoSvc() string {
	return time.Now().Format("20060102150405") + fmt.Sprintf("%06d", rand.Intn(1000000))
}

func cyclePriceForProduct(p models.Product, cycle string) float64 {
	switch cycle {
	case "hourly":
		return p.PriceHourly
	case "quarterly":
		return p.PriceQuarterly
	case "yearly":
		return p.PriceYearly
	default:
		return p.PriceMonthly
	}
}

func renewExprSub(price float64) interface{} {
	return gorm.Expr("balance - ?", price)
}

// RunAutoRenewal 自动续费：扫描开启了自动续费的用户，对其即将到期（24h 内）的容器
// 创建续费订单并尝试用余额支付；余额不足则跳过并通知用户充值。
func RunAutoRenewal() {
	// 找到所有开启自动续费且状态正常的用户
	var users []models.User
	if err := db.DB.Where("auto_renew_flag = ? AND status = ?", true, "active").Find(&users).Error; err != nil {
		logger.Error("自动续费扫描用户失败: %v", err)
		return
	}

	for i := range users {
		processUserAutoRenewal(&users[i])
	}
}

func processUserAutoRenewal(user *models.User) {
	// 该用户所有 active 订单及其到期时间
	var orders []models.Order
	if err := db.DB.Where("user_id = ? AND status = ? AND expire_at IS NOT NULL", user.ID, "active").
		Find(&orders).Error; err != nil {
		return
	}

	now := time.Now()
	for i := range orders {
		o := &orders[i]
		if o.ExpireAt == nil {
			continue
		}
		// 仅处理到期前 24h 内的容器（避免重复续费）
		if o.ExpireAt.After(now.Add(AutoRenewLead)) {
			continue
		}
		// 同一容器已有 pending 续费单则跳过
		if hasPendingRenewal(user.ID, o.ContainerName) {
			continue
		}
		renewFromBalance(user, o)
	}
}

func hasPendingRenewal(userID uint, containerName string) bool {
	var count int64
	db.DB.Model(&models.Order{}).
		Where("user_id = ? AND renewal_for = ? AND status = ?", userID, containerName, "pending").
		Count(&count)
	return count > 0
}

// renewFromBalance 为指定订单创建续费单并用余额支付；余额不足则通知充值
func renewFromBalance(user *models.User, base *models.Order) {
	var product models.Product
	if err := db.DB.First(&product, base.ProductID).Error; err != nil || product.Status != "active" {
		notifyBalanceNotEnough(user, base)
		return
	}

	cycle := base.BillingCycle
	if cycle == "" {
		cycle = "monthly"
	}
	price := cyclePriceForProduct(product, cycle)
	if price <= 0 {
		return
	}

	renew := models.Order{
		OrderNo:      genOrderNoSvc(),
		UserID:       user.ID,
		Username:     user.Username,
		ProductID:    product.ID,
		ProductName:  product.Name + "（自动续费）",
		NodeID:       base.NodeID,
		NodeName:     base.NodeName,
		Image:        product.Image,
		CPU:          base.CPU,
		Memory:       base.Memory,
		Disk:         base.Disk,
		TrafficLimit: base.TrafficLimit,
		BillingCycle: cycle,
		Quantity:     1,
		Amount:       price,
		Status:       "pending",
		PayType:      "balance",
		PaySource:    "balance",
		RenewalFor:   base.ContainerName,
		Remark:       "自动续费容器: " + base.ContainerName,
	}

	// 余额不足：记录并通知用户充值
	if price > user.Balance {
		if err := db.DB.Create(&renew).Error; err != nil {
			return
		}
		notifyBalanceNotEnough(user, base)
		logger.Warn("自动续费余额不足: 用户 %s 容器 %s 需 %.2f 余额 %.2f",
			user.Username, base.ContainerName, price, user.Balance)
		return
	}

	if err := db.DB.Create(&renew).Error; err != nil {
		return
	}

	// 扣减余额
	result := db.DB.Model(&models.User{}).
		Where("id = ? AND balance >= ?", user.ID, price).
		Update("balance", renewExprSub(price))
	if result.Error != nil || result.RowsAffected == 0 {
		notifyBalanceNotEnough(user, base)
		return
	}

	now := time.Now()
	renew.Status = "paid"
	renew.PaidAt = &now
	db.DB.Save(&renew)

	// 触发续费延期
	go func(id uint) {
		if err := ProvisionOrder(id); err != nil {
			logger.Error("自动续费订单 %d 处理失败: %v", id, err)
		}
	}(renew.ID)

	logger.OK("自动续费成功: 用户 %s 容器 %s 续 %.2f 元", user.Username, base.ContainerName, price)
}

func notifyBalanceNotEnough(user *models.User, order *models.Order) {
	db.DB.Create(&models.Notification{
		UserID:      user.ID,
		Type:        "order",
		Title:       "自动续费失败：余额不足",
		Content:     fmt.Sprintf("您的容器 %s 即将到期，自动续费需要 %.2f 元，但当前余额不足。请及时充值以避免服务中断。",
			order.ContainerName, order.Amount),
		RelatedID:   order.ID,
		RelatedType: "order",
	})
}
