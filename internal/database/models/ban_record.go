package models

import "time"

// BanType 封禁类型
type BanType string

const (
	BanTypeName          BanType = "name"
	BanTypeBio           BanType = "bio"
	BanTypeMsg           BanType = "msg"
	BanTypeExternalReply BanType = "external_reply"
	BanTypeManual        BanType = "manual"
	BanTypeFCommand      BanType = "f_command"
	BanTypeMute          BanType = "mute"
	BanTypeContact       BanType = "contact"
	BanTypeNonAdminBot   BanType = "non_admin_bot"
	BanTypeForward       BanType = "forward"
	BanTypeInvite        BanType = "invite"
)

// BanRecord 封禁记录
type BanRecord struct {
	ID               int64      `gorm:"primaryKey;autoIncrement;column:id"`
	UserID           int64      `gorm:"column:user_id;not null;index:idx_ban_records_user_id"`
	BanType          BanType    `gorm:"column:ban_type;not null;size:20"`
	TriggeredKeyword string     `gorm:"column:triggered_keyword;size:500"`
	MessageText      string     `gorm:"column:message_text;type:text"`
	BannedBy         int64      `gorm:"column:banned_by"`
	ChatID           int64      `gorm:"column:chat_id;index:idx_chat_id"`
	BannedAt         time.Time  `gorm:"column:banned_at;autoCreateTime;index:idx_banned_at"`
	UntilDate        *time.Time `gorm:"column:until_date"`      // 临时处罚到期时间 NULL=永久
	RevokedAt        *time.Time `gorm:"column:revoked_at"`      // 手动解除时间 NULL=未解除
	Reason           string     `gorm:"column:reason;size:500"` // 操作原因(拉黑/禁言文字说明)

	// 关联的用户信息（用于显示）
	Username  string `gorm:"-"` // 不映射到数据库
	FirstName string `gorm:"-"`
	LastName  string `gorm:"-"`
	ChatTitle string `gorm:"-"`
}

// IsActive 记录当前是否仍处于生效中
//   - 未手动解除 (RevokedAt 为 nil)
//   - 未到期 (UntilDate 为 nil 或仍在未来)
func (r *BanRecord) IsActive(now time.Time) bool {
	if r == nil {
		return false
	}
	if r.RevokedAt != nil {
		return false
	}
	if r.UntilDate != nil && !r.UntilDate.After(now) {
		return false
	}
	return true
}

func (BanRecord) TableName() string {
	return "ban_records"
}
