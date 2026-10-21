package payment

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"lxdapi/internal/db"
	"lxdapi/internal/service"
	"lxdapi/internal/settings"
	"lxdapi/models"
	"lxdapi/pkg/epay"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/mail"
	"lxdapi/pkg/response"
)

func publicBaseURL(c *gin.Context) string {
	return settings.PublicBaseURL(c)
}

func currentUsername(c *gin.Context) (string, *models.User, bool) {
	username, ok := c.Get("username")
	if !ok {
		response.Error(c, 401, "未登录")
		return "", nil, false
	}
	name := username.(string)
	user, err := service.GetUserByUsername(name)
	if err != nil {
		response.Error(c, 404, "用户不存在")
		return "", nil, false
	}
	return name, user, true
}

func genOrderNo() string {
	return time.Now().Format("20060102150405") + fmt.Sprintf("%06d", rand.Intn(1000000))
}

func cyclePrice(p models.Product, cycle string) float64 {
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

// CreateOrder 用户下单：创建订单并返回支付地址
func CreateOrder(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}

	var req struct {
		ProductID    uint   `json:"product_id" binding:"required"`
		BillingCycle string `json:"billing_cycle"`
		Quantity     int    `json:"quantity"`
		PayType      string `json:"pay_type"`
		CouponCode   string `json:"coupon_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if req.BillingCycle == "" {
		req.BillingCycle = "monthly"
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}
	if req.PayType == "" {
		req.PayType = "alipay"
	}

	var product models.Product
	if err := db.DB.First(&product, req.ProductID).Error; err != nil {
		response.Error(c, 404, "产品不存在")
		return
	}
	if product.Status != "active" {
		response.Error(c, 400, "该产品已下架")
		return
	}
	if product.Stock == 0 {
		response.Error(c, 400, "该产品已售罄")
		return
	}

	price := cyclePrice(product, req.BillingCycle)
	if price < 0 {
		response.Error(c, 400, "该计费周期价格配置异常")
		return
	}

	// 每用户限购检查
	if product.MaxPerUser > 0 {
		var bought int64
		db.DB.Model(&models.Order{}).Where("user_id = ? AND product_id = ? AND status IN ('paid','active','provisioning')", user.ID, product.ID).Count(&bought)
		if int(bought)+req.Quantity > product.MaxPerUser {
			response.Error(c, 400, "该产品每用户限购，超出购买上限")
			return
		}
	}

	// 订单金额 = 产品价格*数量 + 一次性设置费
	orderAmount := price*float64(req.Quantity) + product.SetupFee

	// 0元订单检查
	if orderAmount <= 0 && !product.AllowFree {
		response.Error(c, 400, "该产品不允许0元开通")
		return
	}

	// 优惠券验证与折扣计算
	var couponID uint
	var discountAmount float64
	var userCouponID uint
	if req.CouponCode != "" {
		var uc models.UserCoupon
		if err := db.DB.Preload("Coupon").Where("user_id = ? AND status = 'unused'", user.ID).
			Joins("JOIN coupons ON coupons.id = user_coupons.coupon_id").
			Where("coupons.code = ?", req.CouponCode).First(&uc).Error; err != nil {
			response.Error(c, 400, "优惠券无效或已使用")
			return
		}
		cp := uc.Coupon
		now := time.Now()
		if cp.Status != "active" {
			response.Error(c, 400, "优惠券已停用")
			return
		}
		if cp.StartAt != nil && now.Before(*cp.StartAt) {
			response.Error(c, 400, "优惠券尚未生效")
			return
		}
		if cp.EndAt != nil && now.After(*cp.EndAt) {
			response.Error(c, 400, "优惠券已过期")
			return
		}
		if cp.MinAmount > 0 && orderAmount < cp.MinAmount {
			response.Error(c, 400, "订单金额未达到优惠券使用门槛")
			return
		}
		if cp.Type == "percent" {
			discountAmount = orderAmount * cp.Value / 100
			if cp.MaxDiscount > 0 && discountAmount > cp.MaxDiscount {
				discountAmount = cp.MaxDiscount
			}
		} else {
			discountAmount = cp.Value
		}
		if discountAmount > orderAmount {
			discountAmount = orderAmount
		}
		couponID = cp.ID
		userCouponID = uc.ID
	}

	nodeName := ""
	if product.NodeID != 0 {
		var node models.Node
		if err := db.DB.First(&node, product.NodeID).Error; err == nil {
			nodeName = node.Name
		}
	}

	order := models.Order{
		OrderNo:      genOrderNo(),
		UserID:       user.ID,
		Username:     user.Username,
		ProductID:    product.ID,
		ProductName:  product.Name,
		NodeID:       product.NodeID,
		NodeName:     nodeName,
		Image:        product.Image,
		CPU:          product.CPU,
		Memory:       product.Memory,
		Disk:         product.Disk,
		TrafficLimit: product.TrafficLimit,
		BillingCycle: req.BillingCycle,
		Quantity:     req.Quantity,
		Amount:       orderAmount - discountAmount,
		Status:       "pending",
		PayType:      req.PayType,
		CouponID:     couponID,
		CouponCode:   req.CouponCode,
		DiscountAmount: discountAmount,
	}

	tx := db.DB.Begin()
	if err := tx.Create(&order).Error; err != nil {
		tx.Rollback()
		response.Error(c, 500, "创建订单失败")
		return
	}
	// 优惠券核销
	if userCouponID != 0 {
		now := time.Now()
		if err := tx.Model(&models.UserCoupon{}).Where("id = ? AND status = 'unused'", userCouponID).
			Updates(map[string]interface{}{"status": "used", "used_at": now, "order_id": order.ID}).Error; err != nil {
			tx.Rollback()
			response.Error(c, 500, "优惠券核销失败")
			return
		}
		tx.Model(&models.Coupon{}).Where("id = ?", couponID).Update("used_count", gorm.Expr("used_count + 1"))
	}
	tx.Commit()

	// 0元订单（免费产品或优惠券全额抵扣）：直接标记已支付并触发自动开通，无需走支付流程
	if order.Amount <= 0 {
		now := time.Now()
		order.Status = "paid"
		order.TradeNo = "FREE"
		order.PaidAt = &now
		db.DB.Save(&order)
		go func(id uint) {
			if err := service.ProvisionOrder(id); err != nil {
				logger.Error("0元订单自动开通失败: %v", err)
			}
		}(order.ID)
		response.Success(c, gin.H{"order": order})
		return
	}

	// 余额支付：无需生成第三方支付链接，订单保持 pending，由 pay-with-balance 完成
	if order.PayType == "balance" {
		response.Success(c, gin.H{"order": order})
		return
	}

	payURL, err := buildPayURL(c, &order)
	if err != nil {
		response.Error(c, 500, "生成支付链接失败: "+err.Error())
		return
	}
	order.PayURL = payURL
	db.DB.Save(&order)

	response.Success(c, gin.H{"order": order, "pay_url": payURL})
}

func buildPayURL(c *gin.Context, order *models.Order) (string, error) {
	var config models.PayConfig
	if err := db.DB.Where("provider = ? AND enabled = ?", "epay", true).First(&config).Error; err != nil {
		return "", fmt.Errorf("支付功能未开启")
	}

	base := publicBaseURL(c)
	notifyURL := config.CallbackURL
	if notifyURL == "" {
		notifyURL = base + "/api/payment/notify"
	}
	returnURL := base + "/api/payment/return?order_no=" + order.OrderNo

	params := map[string]string{
		"pid":          config.PID,
		"type":         order.PayType,
		"out_trade_no": order.OrderNo,
		"notify_url":   notifyURL,
		"return_url":   returnURL,
		"name":         order.ProductName,
		"money":        fmt.Sprintf("%.2f", order.Amount),
	}

	return epay.BuildPayURL(config.APIURL, params, config.Key)
}

// Notify 支付异步回调
func Notify(c *gin.Context) {
	_ = c.Request.ParseForm()
	params := map[string]string{}
	for k := range c.Request.Form {
		params[k] = c.Request.Form.Get(k)
	}
	for k := range c.Request.URL.Query() {
		if _, ok := params[k]; !ok {
			params[k] = c.Query(k)
		}
	}

	var config models.PayConfig
	if err := db.DB.Where("provider = ? AND enabled = ?", "epay", true).First(&config).Error; err != nil {
		c.String(http.StatusOK, "fail")
		return
	}

	if !epay.Verify(params, config.Key) {
		logger.Warn("支付回调签名校验失败: %v", params)
		c.String(http.StatusOK, "fail")
		return
	}
	if params["pid"] != config.PID {
		c.String(http.StatusOK, "fail")
		return
	}

	orderNo := params["out_trade_no"]
	var order models.Order
	if err := db.DB.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		c.String(http.StatusOK, "fail")
		return
	}

	if order.Status == "active" || order.Status == "provisioning" || order.ProvisionStatus == "success" {
		c.String(http.StatusOK, "success")
		return
	}

	if !isPaidStatus(params["trade_status"]) {
		c.String(http.StatusOK, "success")
		return
	}

	money, _ := strconv.ParseFloat(params["money"], 64)
	if money > 0 && money+0.01 < order.Amount {
		logger.Warn("支付金额不匹配: 订单 %s 应付 %.2f 实付 %.2f", orderNo, order.Amount, money)
		c.String(http.StatusOK, "fail")
		return
	}

	now := time.Now()
	order.Status = "paid"
	order.TradeNo = params["trade_no"]
	order.PaidAt = &now
	db.DB.Save(&order)

	// 充值单：支付成功直接为用户增加余额，不触发容器开通
	if order.RechargeAmount > 0 {
		creditRecharge(&order)
		c.String(http.StatusOK, "success")
		return
	}

	// 异步自动开通
	go func(id uint) {
		if err := service.ProvisionOrder(id); err != nil {
			logger.Error("订单自动开通失败: %v", err)
		}
	}(order.ID)

	c.String(http.StatusOK, "success")
}

// creditRecharge 充值单入账：增加用户余额并发送通知
func creditRecharge(order *models.Order) {
	var user models.User
	if err := db.DB.First(&user, order.UserID).Error; err != nil {
		logger.Error("充值入账失败，用户不存在: %d", order.UserID)
		return
	}
	result := db.DB.Model(&models.User{}).Where("id = ?", user.ID).
		Update("balance", gorm.Expr("balance + ?", order.RechargeAmount))
	if result.Error != nil {
		logger.Error("充值入账失败: %v", result.Error)
		return
	}
	// 更新内存用户余额供通知使用
	db.DB.First(&user, user.ID)
	logger.OK("用户 %s 充值 %.2f 入账成功，当前余额 %.2f", user.Username, order.RechargeAmount, user.Balance)

	db.DB.Create(&models.Notification{
		UserID:      user.ID,
		Type:        "balance",
		Title:       "充值成功",
		Content:     fmt.Sprintf("您已成功充值 %.2f 元，当前余额 %.2f 元。", order.RechargeAmount, user.Balance),
		RelatedID:   order.ID,
		RelatedType: "order",
	})

	if user.Email != "" {
		defSubject := "【LXD 容器面板】充值成功"
		defBody := `<div style="font-family:sans-serif;line-height:1.8"><h3>充值成功</h3><p>您已成功充值 <strong>{{amount}}</strong> 元，当前余额 <strong>{{balance}}</strong> 元。</p><p style="color:#999;font-size:12px">本邮件由系统自动发送，请勿回复。</p></div>`
		mail.SendTemplate(user.Email, "mail_recharge", defSubject, defBody, map[string]string{
			"username": user.Username,
			"email":    user.Email,
			"amount":   fmt.Sprintf("%.2f", order.RechargeAmount),
			"balance":  fmt.Sprintf("%.2f", user.Balance),
		})
	}
}

func isPaidStatus(s string) bool {
	switch strings.ToUpper(s) {
	case "TRADE_SUCCESS", "SUCCESS", "TRADE_FINISHED":
		return true
	}
	return false
}

// Return 支付同步跳转
func Return(c *gin.Context) {
	orderNo := c.Query("order_no")
	if orderNo == "" {
		c.Redirect(http.StatusFound, "/user/orders")
		return
	}
	c.Redirect(http.StatusFound, "/user/orders?paid="+orderNo)
}

// GetOrders 用户订单列表
func GetOrders(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}
	var orders []models.Order
	if err := db.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&orders).Error; err != nil {
		response.Error(c, 500, "获取订单列表失败")
		return
	}
	response.Success(c, orders)
}

// GetOrder 单个订单
func GetOrder(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}
	orderNo := c.Param("order_no")
	var order models.Order
	if err := db.DB.Where("order_no = ? AND user_id = ?", orderNo, user.ID).First(&order).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	response.Success(c, order)
}

// RepayOrder 重新获取支付地址
func RepayOrder(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}
	orderNo := c.Param("order_no")
	var order models.Order
	if err := db.DB.Where("order_no = ? AND user_id = ?", orderNo, user.ID).First(&order).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	if order.Status != "pending" {
		response.Error(c, 400, "订单状态不允许支付")
		return
	}
	payURL, err := buildPayURL(c, &order)
	if err != nil {
		response.Error(c, 500, "生成支付链接失败: "+err.Error())
		return
	}
	order.PayURL = payURL
	db.DB.Save(&order)
	response.Success(c, gin.H{"pay_url": payURL})
}

// RenewOrder 续费：基于已开通容器所在产品，创建续费订单
// 入参：container_name（必填，定位原订单产品）、billing_cycle（月/季/年，默认 monthly）、pay_type
// 返回：续费订单 + 支付地址
func RenewOrder(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}

	var req struct {
		ContainerName string `json:"container_name" binding:"required"`
		BillingCycle  string `json:"billing_cycle"`
		PayType       string `json:"pay_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if req.BillingCycle == "" {
		req.BillingCycle = "monthly"
	}
	if req.PayType == "" {
		req.PayType = "alipay"
	}

	// 定位该容器对应的最近一条订单，取产品
	var order models.Order
	if err := db.DB.Where("container_name = ? AND user_id = ?", req.ContainerName, user.ID).
		Order("created_at DESC").First(&order).Error; err != nil {
		response.Error(c, 404, "未找到该容器对应的订单，请先到「选购」下单开通")
		return
	}

	var product models.Product
	if err := db.DB.First(&product, order.ProductID).Error; err != nil {
		response.Error(c, 404, "产品不存在")
		return
	}
	if product.Status != "active" {
		response.Error(c, 400, "该产品已下架，无法续费")
		return
	}

	price := cyclePrice(product, req.BillingCycle)
	if price <= 0 {
		response.Error(c, 400, "该计费周期不可续费")
		return
	}

	renew := models.Order{
		OrderNo:      genOrderNo(),
		UserID:       user.ID,
		Username:     user.Username,
		ProductID:    product.ID,
		ProductName:  product.Name + "（续费）",
		NodeID:       order.NodeID,
		NodeName:     order.NodeName,
		Image:        product.Image,
		CPU:          order.CPU,
		Memory:       order.Memory,
		Disk:         order.Disk,
		TrafficLimit: order.TrafficLimit,
		BillingCycle: req.BillingCycle,
		Quantity:     1,
		Amount:       price,
		Status:       "pending",
		PayType:      req.PayType,
		RenewalFor:   req.ContainerName,
		Remark:       "续费容器: " + req.ContainerName,
	}

	if err := db.DB.Create(&renew).Error; err != nil {
		response.Error(c, 500, "创建续费订单失败")
		return
	}

	// 余额支付：无需生成第三方支付链接，由 pay-with-balance 完成
	if renew.PayType == "balance" {
		response.Success(c, gin.H{"order": renew})
		return
	}

	payURL, err := buildPayURL(c, &renew)
	if err != nil {
		response.Error(c, 500, "生成支付链接失败: "+err.Error())
		return
	}
	renew.PayURL = payURL
	db.DB.Save(&renew)

	response.Success(c, gin.H{"order": renew, "pay_url": payURL})
}

// GetPayConfigPublic 前台获取支付可用状态
func GetPayConfigPublic(c *gin.Context) {
	var config models.PayConfig
	if err := db.DB.Where("provider = ?", "epay").First(&config).Error; err != nil {
		response.Success(c, gin.H{"enabled": false})
		return
	}
	response.Success(c, gin.H{"enabled": config.Enabled})
}

// CreateRechargeOrder 余额充值下单：创建充值单并返回支付地址（充值单支付成功后增加用户余额）
// 入参：amount（充值金额，必填 > 0）、pay_type
func CreateRechargeOrder(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}

	var req struct {
		Amount  float64 `json:"amount" binding:"required"`
		PayType string  `json:"pay_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if req.Amount < 1 || req.Amount > 100000 {
		response.Error(c, 400, "充值金额需在 1 ~ 100000 元之间")
		return
	}
	if req.PayType == "" {
		req.PayType = "alipay"
	}

	order := models.Order{
		OrderNo:        genOrderNo(),
		UserID:         user.ID,
		Username:       user.Username,
		ProductName:    "账户余额充值",
		RechargeAmount: req.Amount,
		Amount:         req.Amount,
		Status:         "pending",
		PayType:        req.PayType,
		PaySource:      "third",
		Remark:         "充值金额: " + fmt.Sprintf("%.2f", req.Amount),
	}

	if err := db.DB.Create(&order).Error; err != nil {
		response.Error(c, 500, "创建充值单失败")
		return
	}

	payURL, err := buildPayURL(c, &order)
	if err != nil {
		// 支付未开启时仍保留订单，用户可稍后用第三方开通后充值；此处返回提示
		response.Error(c, 500, "支付功能未开启："+err.Error())
		return
	}
	order.PayURL = payURL
	db.DB.Save(&order)

	response.Success(c, gin.H{"order": order, "pay_url": payURL})
}

// PayWithBalance 余额抵扣支付：用用户余额直接支付某个订单（订单/续费/充值抵扣不适用充值本身）
// 入参：order_no（必填）
func PayWithBalance(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}

	var req struct {
		OrderNo string `json:"order_no" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	var order models.Order
	if err := db.DB.Where("order_no = ? AND user_id = ?", req.OrderNo, user.ID).First(&order).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}
	if order.Status != "pending" {
		response.Error(c, 400, "订单状态不允许支付")
		return
	}
	if order.RechargeAmount > 0 {
		response.Error(c, 400, "充值单无法用余额抵扣")
		return
	}
	if order.Amount > user.Balance {
		response.Error(c, 400, fmt.Sprintf("余额不足：需 %.2f，当前 %.2f", order.Amount, user.Balance))
		return
	}

	// 扣减余额（行级锁防止并发超扣）
	result := db.DB.Model(&models.User{}).
		Where("id = ? AND balance >= ?", user.ID, order.Amount).
		Update("balance", gorm.Expr("balance - ?", order.Amount))
	if result.Error != nil || result.RowsAffected == 0 {
		response.Error(c, 400, "余额扣减失败，请刷新后重试")
		return
	}

	now := time.Now()
	order.Status = "paid"
	order.PaySource = "balance"
	order.PaidAt = &now
	db.DB.Save(&order)

	// 异步开通 / 续费
	go func(id uint) {
		if err := service.ProvisionOrder(id); err != nil {
			logger.Error("订单 %d 余额支付后开通失败: %v", id, err)
		}
	}(order.ID)

	response.Success(c, gin.H{"order": order, "balance": user.Balance - order.Amount})
}

// GetBalance 获取用户余额
func GetBalance(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}
	response.Success(c, gin.H{"balance": user.Balance, "auto_renew": user.AutoRenewEnabled})
}

// SetAutoRenew 设置用户自动续费开关
func SetAutoRenew(c *gin.Context) {
	_, user, ok := currentUsername(c)
	if !ok {
		return
	}

	var req struct {
		AutoRenew *bool `json:"auto_renew" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	db.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("auto_renew_flag", *req.AutoRenew)
	response.Success(c, gin.H{"auto_renew": *req.AutoRenew})
}
