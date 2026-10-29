package models

import "time"

// User 用户模型
type User struct {
	ID            int64      `gorm:"primaryKey;column:id;autoIncrement:false"`
	Username      string     `gorm:"column:username;size:255;index:idx_username"`
	FirstName     string     `gorm:"column:first_name;size:255"`
	LastName      string     `gorm:"column:last_name;size:255"`
	IsBanned      bool       `gorm:"column:is_banned;default:false;index:idx_is_banned"`
	BanReason     string     `gorm:"column:ban_reason;size:500"`
	BannedAt      *time.Time `gorm:"column:banned_at"`
	SelfUnbanUsed bool       `gorm:"column:self_unban_used;default:false"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime;index:idx_created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;autoUpdateTime;index:idx_updated_at"`
}

func (User) TableName() string {
	return "users"
}
