package models

import "time"

// BotSettings 单个 Bot 的运行时配置
// 每个 Bot 数据库内固定存一行 (ID=1)
//
// WaterDetectDisabled / SpamDetectDisabled / ForwardBurstDisabled 反向命名:
// 零值(false) 即"默认开启", AutoMigrate 给老库新增字段时不会自动关闭线上检测.
// NonAdminBotAction 非管理员 Bot 发言的处置动作
type NonAdminBotAction string

const (
	NonAdminBotActionNone   NonAdminBotAction = ""       // 不处理(默认)
	NonAdminBotActionDelete NonAdminBotAction = "delete" // 仅删除消息
	NonAdminBotActionBan    NonAdminBotAction = "ban"    // 删除消息 + 永久拉黑
)

type BotSettings struct {
	ID                   int64             `gorm:"primaryKey;column:id"`
	WorkGroupID          int64             `gorm:"column:work_group_id;not null;default:0"`
	BroadcastChatID      int64             `gorm:"column:broadcast_chat_id;not null;default:0"`
	WaterDetectDisabled  bool              `gorm:"column:water_detect_disabled;not null;default:false"`
	SpamDetectDisabled   bool              `gorm:"column:spam_detect_disabled;not null;default:false"`
	ForwardBurstDisabled bool              `gorm:"column:forward_burst_disabled;not null;default:false"`
	NonAdminBotAction    NonAdminBotAction `gorm:"column:non_admin_bot_action;size:20;not null;default:''"`
	UpdatedAt            time.Time         `gorm:"column:updated_at;autoUpdateTime"`
}

func (BotSettings) TableName() string {
	return "bot_settings"
}
