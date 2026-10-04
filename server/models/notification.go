package models

import "gorm.io/gorm"

type Ticket struct {
	gorm.Model
	UserID   uint   `gorm:"index;not null"`
	User     User   `gorm:"foreignKey:UserID"`
	Title    string `gorm:"size:255;not null"`
	Content  string `gorm:"type:text;not null"`
	Status   string `gorm:"size:20;default:'open'"` // open, closed, pending
	Priority string `gorm:"size:20;default:'normal'"` // low, normal, high, urgent
	Type     string `gorm:"size:50"` // billing, technical, other

	// 响应信息
	Reply    string `gorm:"type:text"`
	RepliedAt *string
	ClosedAt *string
}

type TicketMessage struct {
	gorm.Model
	TicketID uint   `gorm:"index;not null"`
	UserID   uint   `gorm:"index;not null"`
	User     User   `gorm:"foreignKey:UserID"`
	Message  string `gorm:"type:text;not null"`
	IsAdmin  bool   `gorm:"default:false"`
	Files    string `gorm:"size:1000"` // 附件列表
}

type Notification struct {
	gorm.Model
	UserID   uint   `gorm:"index;not null"`
	User     User   `gorm:"foreignKey:UserID"`
	Type     string `gorm:"size:50"` // system, ticket, order, promotion
	Title    string `gorm:"size:200;not null"`
	Content  string `gorm:"type:text;not null"`
	IsRead   bool   `gorm:"default:false"`
	RelatedID   uint   // 关联 ID（工单/订单等）
	RelatedType string `gorm:"size:50"` // ticket, order, service
}
