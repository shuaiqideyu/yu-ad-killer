package models

import "time"

// Whitelist 用户白名单模型
type Whitelist struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id"`
	UserID    int64     `gorm:"column:user_id;not null;uniqueIndex:uk_whitelist_user_id"`
	AddedBy   int64     `gorm:"column:added_by;not null"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`

	// 非持久化字段(用于展示)
	Username  string `gorm:"-"`
	FirstName string `gorm:"-"`
}

func (Whitelist) TableName() string {
	return "whitelists"
}
