package api

import (
	"strconv"
	"time"

	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreateTicket 创建工单
func CreateTicket(c *gin.Context) {
	var req struct {
		Title    string `json:"title" binding:"required"`
		Content  string `json:"content" binding:"required"`
		Type     string `json:"type"`
		Priority string `json:"priority"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	userID, _ := c.GetUint("user_id")

	ticket := models.Ticket{
		UserID:   userID,
		Title:    req.Title,
		Content:  req.Content,
		Type:     req.Type,
		Priority: req.Priority,
		Status:   "open",
	}

	if ticket.Priority == "" {
		ticket.Priority = "normal"
	}

	if err := db.DB.Create(&ticket).Error; err != nil {
		response.Error(c, 500, "创建工单失败")
		return
	}

	response.Success(c, ticket)
}

// GetTickets 获取工单列表
func GetTickets(c *gin.Context) {
	userID, _ := c.GetUint("user_id")

	var tickets []models.Ticket
	if err := db.DB.Where("user_id = ?", userID).Order("created_at DESC").Find(&tickets).Error; err != nil {
		response.Error(c, 500, "获取工单列表失败")
		return
	}

	response.Success(c, tickets)
}

// GetTicket 获取单个工单
func GetTicket(c *gin.Context) {
	userID, _ := c.GetUint("user_id")
	id := c.Param("id")

	var ticket models.Ticket
	if err := db.DB.Where("id = ? AND user_id = ?", id, userID).First(&ticket).Error; err != nil {
		response.Error(c, 404, "工单不存在")
		return
	}

	response.Success(c, ticket)
}

// CloseTicket 关闭工单
func CloseTicket(c *gin.Context) {
	userID, _ := c.GetUint("user_id")
	id := c.Param("id")

	ticket := models.Ticket{}
	if err := db.DB.Where("id = ? AND user_id = ?", id, userID).First(&ticket).Error; err != nil {
		response.Error(c, 404, "工单不存在")
		return
	}

	ticket.Status = "closed"
	closedAt := time.Now().Format("2006-01-02 15:04:05")
	ticket.ClosedAt = &closedAt

	if err := db.DB.Save(&ticket).Error; err != nil {
		response.Error(c, 500, "关闭工单失败")
		return
	}

	response.Success(c, ticket)
}

// AddTicketMessage 添加工单消息
func AddTicketMessage(c *gin.Context) {
	userID, _ := c.GetUint("user_id")
	id := c.Param("id")

	var req struct {
		Message string `json:"message" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}

	var ticket models.Ticket
	if err := db.DB.Where("id = ?", id).First(&ticket).Error; err != nil {
		response.Error(c, 404, "工单不存在")
		return
	}

	message := models.TicketMessage{
		TicketID: ticket.ID,
		UserID:   userID,
		Message:  req.Message,
		IsAdmin:  false,
	}

	if err := db.DB.Create(&message).Error; err != nil {
		response.Error(c, 500, "发送消息失败")
		return
	}

	response.Success(c, message)
}

// GetTicketMessages 获取工单消息列表
func GetTicketMessages(c *gin.Context) {
	id := c.Param("id")

	var messages []models.TicketMessage
	if err := db.DB.Where("ticket_id = ?", id).Order("created_at ASC").Find(&messages).Error; err != nil {
		response.Error(c, 500, "获取消息列表失败")
		return
	}

	response.Success(c, messages)
}

// CreateNotification 创建通知（管理员使用）
func CreateNotification(c *gin.Context) {
	var req struct {
		UserID      uint   `json:"user_id"`
		Type        string `json:"type" binding:"required"`
		Title       string `json:"title" binding:"required"`
		Content     string `json:"content" binding:"required"`
		RelatedID   uint   `json:"related_id"`
		RelatedType string `json:"related_type"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
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

// GetNotifications 获取用户通知列表
func GetNotifications(c *gin.Context) {
	userID, _ := c.GetUint("user_id")

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	typeFilter := c.Query("type")

	query := db.DB.Where("user_id = ?", userID)
	if typeFilter != "" {
		query = query.Where("type = ?", typeFilter)
	}

	var notifications []models.Notification
	var total int64

	query.Count(&total)
	query.Order("created_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&notifications)

	response.Success(c, gin.H{
		"list":  notifications,
		"total": total,
		"page":  page,
		"size":  pageSize,
	})
}

// GetUnreadCount 获取未读通知数量
func GetUnreadCount(c *gin.Context) {
	userID, _ := c.GetUint("user_id")

	var count int64
	db.DB.Model(&models.Notification{}).Where("user_id = ? AND is_read = ?", userID, false).Count(&count)

	response.Success(c, gin.H{"count": count})
}

// MarkAsRead 标记通知为已读
func MarkAsRead(c *gin.Context) {
	userID, _ := c.GetUint("user_id")
	id := c.Param("id")

	if err := db.DB.Model(&models.Notification{}).Where("id = ? AND user_id = ?", id, userID).Update("is_read", true).Error; err != nil {
		response.Error(c, 500, "操作失败")
		return
	}

	response.Success(c, nil)
}

// MarkAllAsRead 全部标记为已读
func MarkAllAsRead(c *gin.Context) {
	userID, _ := c.GetUint("user_id")

	db.DB.Model(&models.Notification{}).Where("user_id = ? AND is_read = ?", userID, false).Update("is_read", true)

	response.Success(c, nil)
}

// DeleteNotification 删除通知
func DeleteNotification(c *gin.Context) {
	userID, _ := c.GetUint("user_id")
	id := c.Param("id")

	if err := db.DB.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Notification{}).Error; err != nil {
		response.Error(c, 500, "删除失败")
		return
	}

	response.Success(c, nil)
}
