package notification

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// AdminGetTickets 管理员获取工单列表（支持按状态筛选）
func AdminGetTickets(c *gin.Context) {
	status := c.Query("status")

	query := db.DB.Model(&models.Ticket{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	query.Count(&total)

	var tickets []models.Ticket
	query.Order("created_at DESC").Find(&tickets)

	response.Success(c, gin.H{"list": tickets, "total": total})
}

// AdminGetTicket 管理员获取工单详情
func AdminGetTicket(c *gin.Context) {
	id := c.Param("id")
	var ticket models.Ticket
	if err := db.DB.First(&ticket, id).Error; err != nil {
		response.Error(c, 404, "工单不存在")
		return
	}

	var messages []models.TicketMessage
	db.DB.Where("ticket_id = ?", ticket.ID).Order("created_at ASC").Find(&messages)

	response.Success(c, gin.H{"ticket": ticket, "messages": messages})
}

// AdminAddTicketMessage 管理员回复工单
func AdminAddTicketMessage(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	var ticket models.Ticket
	if err := db.DB.First(&ticket, id).Error; err != nil {
		response.Error(c, 404, "工单不存在")
		return
	}

	msg := models.TicketMessage{
		TicketID: ticket.ID,
		UserID:   ticket.UserID,
		Message:  req.Message,
		IsAdmin:  true,
	}
	if err := db.DB.Create(&msg).Error; err != nil {
		response.Error(c, 500, "回复失败")
		return
	}

	// 工单标记为 pending（待用户确认）
	ticket.Status = "pending"
	db.DB.Save(&ticket)

	// 站内通知用户
	db.DB.Create(&models.Notification{
		UserID:      ticket.UserID,
		Type:        "ticket",
		Title:       "工单有新回复",
		Content:     req.Message,
		RelatedID:   ticket.ID,
		RelatedType: "ticket",
		IsRead:      false,
	})

	response.Success(c, msg)
}

// AdminCloseTicket 管理员关闭工单
func AdminCloseTicket(c *gin.Context) {
	id := c.Param("id")
	var ticket models.Ticket
	if err := db.DB.First(&ticket, id).Error; err != nil {
		response.Error(c, 404, "工单不存在")
		return
	}

	closedAt := time.Now().Format("2006-01-02 15:04:05")
	ticket.Status = "closed"
	ticket.ClosedAt = &closedAt
	if err := db.DB.Save(&ticket).Error; err != nil {
		response.Error(c, 500, "关闭失败")
		return
	}

	response.Success(c, ticket)
}

// AdminGetNotifications 管理员查看通知列表
func AdminGetNotifications(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var total int64
	db.DB.Model(&models.Notification{}).Count(&total)

	var notifications []models.Notification
	db.DB.Order("created_at DESC").Limit(pageSize).Offset((page-1)*pageSize).Find(&notifications)

	response.Success(c, gin.H{"list": notifications, "total": total, "page": page, "size": pageSize})
}

// AdminCreateNotification 管理员向单个用户发送通知
func AdminCreateNotification(c *gin.Context) {
	var req struct {
		UserID      uint   `json:"user_id" binding:"required"`
		Type        string `json:"type"`
		Title       string `json:"title" binding:"required"`
		Content     string `json:"content" binding:"required"`
		RelatedID   uint   `json:"related_id"`
		RelatedType string `json:"related_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	if req.Type == "" {
		req.Type = "system"
	}

	notification := models.Notification{
		UserID:      req.UserID,
		Type:        req.Type,
		Title:       req.Title,
		Content:     req.Content,
		RelatedID:   req.RelatedID,
		RelatedType: req.RelatedType,
		IsRead:      false,
	}
	if err := db.DB.Create(&notification).Error; err != nil {
		response.Error(c, 500, "创建通知失败")
		return
	}
	response.Success(c, notification)
}
