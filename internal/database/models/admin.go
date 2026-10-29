package models

import "time"

// AdminRole 管理员角色
type AdminRole string

const (
	RoleAuthor AdminRole = "author" // 作者(超级管理员)
	RoleConfig AdminRole = "config" // 配置管理员
)

// Admin 管理员模型
type Admin struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id"`
	UserID    int64     `gorm:"column:user_id;not null;uniqueIndex:uk_user_id"`
	Role      AdminRole `gorm:"column:role;not null;size:20"`
	CreatedBy int64     `gorm:"column:created_by;default:null"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名
func (Admin) TableName() string {
	return "admins"
}
