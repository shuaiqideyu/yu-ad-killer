package utils

import (
	"fmt"
	"html"
	"telegram-ban-bot/internal/database/models"
)

// FormatUserInfoForPrivateChat 格式化用户信息(私信查询专用)
// 返回丰富 emoji 格式的用户信息文本 (HTML格式)
func FormatUserInfoForPrivateChat(user *models.User, record *models.BanRecord, operatorUser *models.User) string {
	if user == nil {
		return "⚠️ 该用户未在系统中"
	}

	// 格式化用户名
	username := "无"
	if user.Username != "" {
		username = "@" + user.Username
	}

	// 构建基础信息(使用 HTML 标签)
	text := fmt.Sprintf("👤 <b>用户名</b>: %s\n🆔 <b>用户ID</b>: <code>%d</code>\n", username, user.ID)

	// 添加状态信息
	if user.IsBanned {
		text += "❌ <b>状态</b>: 已封禁\n"

		// 添加封禁时间
		if user.BannedAt != nil {
			text += fmt.Sprintf("⏰ <b>封禁时间</b>: <code>%s</code>\n", user.BannedAt.Format("2006-01-02 15:04:05"))
		}

		// 添加封禁原因
		// user.BanReason 已包含完整信息(包括操作人),直接显示即可
		if user.BanReason != "" {
			text += fmt.Sprintf("⚠️ <b>封禁原因</b>:\n%s", html.EscapeString(user.BanReason))
		}
	} else {
		text += "✅ <b>状态</b>: 正常"
	}

	return text
}

