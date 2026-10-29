package models

import "time"

// KeywordType 关键词类型
type KeywordType string

const (
	KeywordTypeName     KeywordType = "name"
	KeywordTypeBio      KeywordType = "bio"
	KeywordTypeMsg      KeywordType = "msg"
	KeywordTypeDupWhite KeywordType = "dup_white" // 水群/刷屏检测白名单
	KeywordTypeForward  KeywordType = "forward"   // 转发来源屏蔽关键词
	KeywordTypeInvite   KeywordType = "invite"    // 邀请链接对应的群名
)

// Keyword 关键词模型
type Keyword struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id"`
	Type      BanType   `gorm:"column:type;not null;size:20"`
	Word      string    `gorm:"column:keyword;not null;size:500"`
	CreatedBy int64     `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`

	// 关联的创建者用户名（用于显示，不映射到数据库）
	CreatorUsername string `gorm:"-"`
}

func (Keyword) TableName() string {
	return "keywords"
}
