package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/mail"
)

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// refundOrderToBalance 已支付订单退款到余额并把订单置为 refunded；0 元订单不处理。
// 订单状态翻转、余额到账、退款流水写入在同一个 DB 事务里完成：任何一步失败整体回滚，
// 不存在"订单已标退款但余额没到账"的 crash 中间态；以订单状态条件更新 + 退款流水去重保证幂等。
func refundOrderToBalance(o *models.Order, remark string) (float64, error) {
	amt := round2(o.Amount)
	if amt <= 0 {
		return 0, nil // 0 元订单无需退款
	}
	var after float64
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		// 1. 订单 paid → refunded（条件更新：并发下只有一方能推进）
		res := tx.Model(&models.Order{}).Where("id = ? AND status = ?", o.ID, "paid").Update("status", "refunded")
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // 未支付或已退款：幂等直接返回
		}
		// 2. 退款流水去重（同一订单绝不重复入账）
		var exists int64
		if err := tx.Model(&models.BalanceLog{}).Where("order_no = ? AND type = ?", o.OrderNo, "refund").Count(&exists).Error; err != nil {
			return err
		}
		if exists > 0 {
			return nil
		}
		// 3. 余额到账 + 写流水（同一事务）
		res = tx.Model(&models.User{}).Where("id = ?", o.UserID).UpdateColumn("balance", gorm.Expr("balance + ?", amt))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("用户不存在")
		}
		var u models.User
		if err := tx.Select("id", "balance").First(&u, o.UserID).Error; err != nil {
			return err
		}
		after = round2(u.Balance)
		return tx.Create(&models.BalanceLog{UserID: o.UserID, Amount: amt, Balance: after, Type: "refund", Remark: remark, OrderNo: o.OrderNo}).Error
	})
	if err != nil {
		return 0, err
	}
	return after, nil
}

// ProvisionOrder 支付成功后的自动开通：按订单规格创建容器
func ProvisionOrder(orderID uint) error {
	var order models.Order
	if err := db.DB.First(&order, orderID).Error; err != nil {
		return fmt.Errorf("订单不存在: %v", err)
	}
	if order.ProvisionStatus == "success" && order.ContainerName != "" {
		return nil
	}

	// 续费订单：不新建容器，仅延长目标容器所在订单的到期时间
	if order.RenewalFor != "" {
		return renewOrder(&order)
	}

	order.ProvisionStatus = "pending"
	order.Status = "provisioning"
	db.DB.Save(&order)

	err := provisionOrderInternal(&order)

	if err != nil {
		order.ProvisionStatus = "failed"
		order.Status = "failed"
		order.ProvisionError = err.Error()
		db.DB.Save(&order)
		logger.Error("订单 %s 自动开通失败: %v", order.OrderNo, err)
		// 开通失败自动退款到余额（事务+幂等）
		if _, rerr := refundOrderToBalance(&order, fmt.Sprintf("实例开通失败退款 %s", order.OrderNo)); rerr != nil {
			logger.Error("订单 %s 退款失败: %v", order.OrderNo, rerr)
		}
		notifyOrder(&order, "开通失败", "您的订单 "+order.OrderNo+" 已完成支付，但服务器开通失败："+err.Error()+"，已自动退款到余额，请联系客服处理。")
		return err
	}

	now := time.Now()
	order.ProvisionStatus = "success"
	order.Status = "active"
	order.ProvisionError = ""
	order.ExpireAt = calcExpireAt(now, order.BillingCycle, order.Quantity)
	db.DB.Save(&order)

	logger.OK("订单 %s 自动开通成功，容器: %s", order.OrderNo, order.ContainerName)

	// 扣减库存
	if order.ProductID != 0 {
		var product models.Product
		if err := db.DB.First(&product, order.ProductID).Error; err == nil && product.Stock > 0 {
			db.DB.Model(&product).Update("stock", product.Stock-order.Quantity)
		}
	}

	notifyOrder(&order, "服务已开通", fmt.Sprintf("您的订单 %s 已开通成功，服务器名称：%s，规格：%d核 / %dMB 内存 / %dGB 磁盘。",
		order.OrderNo, order.ContainerName, order.CPU, order.Memory, order.Disk))
	return nil
}

func provisionOrderInternal(order *models.Order) error {
	var user models.User
	if err := db.DB.First(&user, order.UserID).Error; err != nil {
		return fmt.Errorf("下单用户不存在")
	}

	image := order.Image
	if image == "" {
		var node models.Node
		if order.NodeID != 0 {
			if err := db.DB.First(&node, order.NodeID).Error; err == nil {
				image = node.Image
			}
		}
	}
	if image == "" {
		image = "debian-12-net"
	}

	name := order.ContainerName
	if name == "" {
		name = generateContainerName(user.Username)
	}

	req := &models.CreateContainerRequest{
		Name:         name,
		Image:        image,
		CPU:          order.CPU,
		Memory:       order.Memory,
		Disk:         order.Disk * 1024, // GB -> MB
		TrafficLimit: order.TrafficLimit,
		Username:     user.Username,
		NodeID:       order.NodeID, // 节点路由：容器创建在订单归属节点上
	}

	svc := NewContainerService()
	if err := svc.Create(context.Background(), req); err != nil {
		return err
	}

	order.ContainerName = name
	return nil
}

// renewOrder 续费：延长目标容器对应订单的到期时间
func renewOrder(order *models.Order) error {
	// 找到目标容器最近一条 active 订单，在其到期时间基础上累加
	var base models.Order
	if err := db.DB.Where("container_name = ? AND user_id = ? AND status = ?", order.RenewalFor, order.UserID, "active").
		Order("created_at DESC").First(&base).Error; err != nil {
		// 即使没有原 active 订单，也按当前时间计算
		now := time.Now()
		newExpire := calcExpireAt(now, order.BillingCycle, order.Quantity)
		order.Status = "active"
		order.ProvisionStatus = "success"
		order.ContainerName = order.RenewalFor
		order.ExpireAt = newExpire
		db.DB.Save(order)
		notifyOrder(order, "续费成功", "您的容器 "+order.RenewalFor+" 续费成功，已延长使用期限。")
		return nil
	}

	baseExpire := time.Now()
	if base.ExpireAt != nil && base.ExpireAt.After(time.Now()) {
		baseExpire = *base.ExpireAt
	}
	newExpire := calcExpireAt(baseExpire, order.BillingCycle, order.Quantity)
	order.Status = "active"
	order.ProvisionStatus = "success"
	order.ContainerName = order.RenewalFor
	order.ExpireAt = newExpire
	db.DB.Save(order)

	// 同步延长原订单的到期时间
	base.ExpireAt = newExpire
	db.DB.Save(&base)

	notifyOrder(order, "续费成功", "您的容器 "+order.RenewalFor+" 续费成功，到期时间已延长至 "+newExpire.Format("2006-01-02 15:04")+"。")
	return nil
}

func generateContainerName(username string) string {
	prefix := sanitizeName(username)
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	if prefix == "" {
		prefix = "svc"
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%100000)
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	return strings.Trim(sb.String(), "-")
}

func calcExpireAt(from time.Time, cycle string, quantity int) *time.Time {
	if quantity <= 0 {
		quantity = 1
	}
	var d time.Duration
	switch cycle {
	case "hourly":
		d = time.Duration(quantity) * time.Hour
	case "quarterly":
		d = time.Duration(quantity) * 90 * 24 * time.Hour
	case "yearly":
		d = time.Duration(quantity) * 365 * 24 * time.Hour
	default: // monthly
		d = time.Duration(quantity) * 30 * 24 * time.Hour
	}
	t := from.Add(d)
	return &t
}

func notifyOrder(order *models.Order, title, content string) {
	db.DB.Create(&models.Notification{
		UserID:      order.UserID,
		Type:        "order",
		Title:       title,
		Content:     content,
		RelatedID:   order.ID,
		RelatedType: "order",
	})

	var user models.User
	if err := db.DB.First(&user, order.UserID).Error; err == nil && user.Email != "" {
		defSubject := "【{{site}}】" + title
		defBody := `<div style="font-family:sans-serif;line-height:1.8"><h3>{{title}}</h3><p>{{content}}</p><p style="color:#999;font-size:12px">本邮件由系统自动发送，请勿回复。</p></div>`
		data := map[string]string{
			"site":      "LXD 容器面板",
			"username":  user.Username,
			"email":     user.Email,
			"order_no":  order.OrderNo,
			"product":   order.ProductName,
			"container": order.ContainerName,
			"amount":    fmt.Sprintf("%.2f", order.Amount),
			"title":     title,
			"content":   content,
		}
		if err := mail.SendTemplate(user.Email, "mail_order_paid", defSubject, defBody, data); err != nil {
			logger.Warn("订单通知邮件发送失败: %v", err)
		}
	}
}
