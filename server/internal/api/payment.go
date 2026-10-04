package api

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreatePaymentOrder 创建支付订单
func CreatePaymentOrder(c *gin.Context) {
	var req struct {
		OrderNo     string  `json:"order_no" binding:"required"`
		UserID      uint    `json:"user_id" binding:"required"`
		ProductName string  `json:"product_name" binding:"required"`
		Amount      float64 `json:"amount" binding:"required"`
		PayType     string  `json:"pay_type"`
		ReturnURL   string  `json:"return_url"`
		Remark      string  `json:"remark"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	if req.PayType == "" {
		req.PayType = "epay"
	}

	orderNo := req.OrderNo
	if orderNo == "" {
		orderNo = fmt.Sprintf("ORD%d%d", time.Now().Unix(), c.Request.NumericalPort())
	}

	order := models.PaymentOrder{
		OrderNo:     orderNo,
		UserID:      req.UserID,
		ProductName: req.ProductName,
		Amount:      req.Amount,
		PayType:     req.PayType,
		ReturnURL:   req.ReturnURL,
		Remark:      req.Remark,
		Status:      "pending",
	}

	if err := db.DB.Create(&order).Error; err != nil {
		response.Error(c, 500, "创建订单失败")
		return
	}

	// 生成支付链接
	payURL, err := generatePayURL(order)
	if err != nil {
		response.Error(c, 500, "生成支付链接失败")
		return
	}

	order.PayURL = payURL

	if err := db.DB.Save(&order).Error; err != nil {
		response.Error(c, 500, "保存订单失败")
		return
	}

	response.Success(c, order)
}

// GeneratePayURL 生成支付 URL（易支付）
func generatePayURL(order models.PaymentOrder) (string, error) {
	// 获取支付配置
	var config models.PayConfig
	if err := db.DB.Where("provider = 'epay' AND enabled = true").First(&config).Error; err != nil {
		return "", fmt.Errorf("未配置易支付")
	}

	// 签名参数
	tid := order.OrderNo
	name := order.ProductName
	money := fmt.Sprintf("%.2f", order.Amount)
	callback := order.NotifyURL
	_ = callback

	// 构建签名字符串
	signStr := fmt.Sprintf("pid=%s&type=%s&out_trade_no=%s&name=%s&money=%s&callback=%s&key=%s",
		config.PID, "alipay", tid, name, money, callback, config.Key)

	// MD5 签名
	sign := strings.ToLower(fmt.Sprintf("%x", md5.Sum([]byte(signStr))))

	// 构建支付 URL
	payURL := fmt.Sprintf("%s?pid=%s&type=%s&out_trade_no=%s&name=%s&money=%s&callback=%s&sign=%s&signtype=1",
		config.APIURL,
		config.PID,
		"alipay",
		tid,
		url.QueryEscape(name),
		money,
		callback,
		sign,
	)

	return payURL, nil
}

// PayCallback 支付回调接口
func PayCallback(c *gin.Context) {
	// 获取回调参数
	pid := c.DefaultQuery("pid", "")
	out_trade_no := c.DefaultQuery("out_trade_no", "")
_trade_no := c.DefaultQuery("trade_no", "")
	_ = _trade_no
	name := c.DefaultQuery("name", "")
	money := c.DefaultQuery("money", "")
	sign := c.DefaultQuery("sign", "")
	signtype := c.DefaultQuery("signtype", "1")
	trade_status := c.DefaultQuery("trade_status", "")

	// 获取商户密钥
	var config models.PayConfig
	if err := db.DB.Where("provider = 'epay' AND enabled = true").First(&config).Error; err != nil {
		c.String(http.StatusOK, "fail")
		return
	}

	// 验证签名
	signStr := fmt.Sprintf("trade_no=%s&trade_status=%s&test3=0&key=%s&money=%s&name=%s&out_trade_no=%s&pid=%s&sign=%s&signtype=%s",
		_trade_no, trade_status, config.Key, money, name, out_trade_no, pid, sign, signtype)

	calculatedSign := strings.ToLower(fmt.Sprintf("%x", md5.Sum([]byte(signStr))))

	if calculatedSign != sign {
		c.String(http.StatusOK, "fail")
		return
	}

	// 处理支付成功
	if trade_status == "TRADE_SUCCESS" {
		var order models.PaymentOrder
		if err := db.DB.Where("order_no = ?", out_trade_no).First(&order).Error; err != nil {
			c.String(http.StatusOK, "fail")
			return
		}

		if order.Status == "paid" {
			c.String(http.StatusOK, "success")
			return
		}

		order.Status = "paid"
		paidAt := time.Now().Format("2006-01-02 15:04:05")
		order.PaidAt = &paidAt

		if err := db.DB.Save(&order).Error; err != nil {
			c.String(http.StatusOK, "fail")
			return
		}

		// TODO: 处理支付成功后的业务逻辑（开通服务、发送通知等）
	}

	c.String(http.StatusOK, "success")
}

// GetPaymentOrders 获取用户订单列表
func GetPaymentOrders(c *gin.Context) {
	userID := c.GetUint("user_id")
	if userID == 0 {
		response.Error(c, 401, "未登录")
		return
	}

	var orders []models.PaymentOrder
	if err := db.DB.Where("user_id = ?", userID).Order("created_at DESC").Find(&orders).Error; err != nil {
		response.Error(c, 500, "获取订单列表失败")
		return
	}

	response.Success(c, orders)
}

// GetPaymentOrder 获取单个订单
func GetPaymentOrder(c *gin.Context) {
	userID := c.GetUint("user_id")
	orderNo := c.Param("order_no")

	var order models.PaymentOrder
	if err := db.DB.Where("order_no = ? AND user_id = ?", orderNo, userID).First(&order).Error; err != nil {
		response.Error(c, 404, "订单不存在")
		return
	}

	response.Success(c, order)
}

// GetPayConfig 获取支付配置
func GetPayConfig(c *gin.Context) {
	var config models.PayConfig
	if err := db.DB.Where("provider = 'epay'").First(&config).Error; err != nil {
		response.Success(c, gin.H{"enabled": false})
		return
	}

	response.Success(c, config)
}
