package admin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/internal/service"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// GetOrderList 订单列表
func GetOrderList(c *gin.Context) {
	query := db.DB.Model(&models.Order{})
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	if kw := c.Query("keyword"); kw != "" {
		like := "%" + kw + "%"
		query = query.Where("order_no LIKE ? OR username LIKE ? OR product_name LIKE ?", like, like, like)
	}

	var total int64
	query.Count(&total)

	page := atoiDefault(c.DefaultQuery("page", "1"), 1)
	size := atoiDefault(c.DefaultQuery("page_size", "20"), 20)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}

	var orders []models.Order
	if err := query.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&orders).Error; err != nil {
		response.Error(c, 500, "获取订单列表失败")
		return
	}
	response.Success(c, gin.H{"list": orders, "total": total, "page": page, "page_size": size})
}

// GetOrderDetail 订单详情
func GetOrderDetail(c *gin.Context) {
	var order models.Order
	if err := db.DB.First(&order, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	response.Success(c, order)
}

// MarkOrderPaid 手动标记已支付并开通
func MarkOrderPaid(c *gin.Context) {
	var order models.Order
	if err := db.DB.First(&order, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	if order.Status == "active" {
		response.Error(c, 400, "订单已开通")
		return
	}
	now := time.Now()
	order.Status = "paid"
	order.PaidAt = &now
	db.DB.Save(&order)

	go service.ProvisionOrder(order.ID)
	response.Success(c, gin.H{"msg": "已标记支付，正在开通"})
}

// RetryProvisionOrder 重新开通
func RetryProvisionOrder(c *gin.Context) {
	var order models.Order
	if err := db.DB.First(&order, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	order.ProvisionStatus = "none"
	db.DB.Save(&order)
	go service.ProvisionOrder(order.ID)
	response.Success(c, gin.H{"msg": "已提交开通"})
}

// CloseOrder 关闭订单
func CloseOrder(c *gin.Context) {
	var order models.Order
	if err := db.DB.First(&order, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	if order.Status == "active" {
		response.Error(c, 400, "已开通的订单无法关闭")
		return
	}
	order.Status = "cancelled"
	db.DB.Save(&order)
	response.Success(c, order)
}
