package models

import "time"

// CloneBot 主作者通过 /add 创建的克隆 Bot 注册信息
// 仅在主 Bot 数据库存在
type CloneBot struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id"`
	BotUserID    int64     `gorm:"column:bot_user_id;not null;uniqueIndex:uk_clone_bots_bot_user_id"`
	BotUsername  string    `gorm:"column:bot_username;size:255"`
	Token        string    `gorm:"column:token;not null;size:255"`
	LocalAdminID int64     `gorm:"column:local_admin_id;not null"`
	DBPath       string    `gorm:"column:db_path;not null;size:500"`
	Enabled      bool      `gorm:"column:enabled;not null;default:true"`
	CreatedBy    int64     `gorm:"column:created_by;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (CloneBot) TableName() string {
	return "clone_bots"
}
