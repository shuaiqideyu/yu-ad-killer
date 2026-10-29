package handlers

import (
	"context"
	"fmt"
	"telegram-ban-bot/internal/database/models"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// handleAddAdmin 处理添加管理员请求
func (h *Handler) handleAddAdmin(query *telego.CallbackQuery) {
	// 只有作者可以添加管理员
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以添加管理员",
			ShowAlert:       true,
		})
		return
	}

	// 设置会话状态为添加管理员模式(5分钟过期)
	h.SetSessionState(query.From.ID, "adding_admin", 5*time.Minute)

	// 发送提示消息
	text := "➕ 添加管理员\n\n请发送要添加的用户ID或用户名\n\n示例:\n• 用户ID: 123456789\n• 用户名: @username\n• 或直接发送: username\n\n发送 /cancel 取消操作"

	h.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID: tu.ID(query.Message.GetChat().ID),
		Text:   text,
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "请在私聊中发送用户信息",
	})
}

// handleAdminRoleSelection 处理管理员角色选择
func (h *Handler) handleAdminRoleSelection(query *telego.CallbackQuery, userID int64, role string) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以添加管理员",
			ShowAlert:       true,
		})
		return
	}

	if h.IsAdmin(userID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 该用户已是管理员",
			ShowAlert:       true,
		})
		return
	}

	var adminRole models.AdminRole
	if role == "author" {
		adminRole = models.RoleAuthor
	} else {
		adminRole = models.RoleConfig
	}

	err := h.adminSvc.AddAdmin(userID, adminRole, query.From.ID)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            fmt.Sprintf("❌ 添加失败: %v", err),
			ShowAlert:       true,
		})
		return
	}

	user, _ := h.userRepo.GetByID(userID)
	roleText := "配置管理员"
	if adminRole == models.RoleAuthor {
		roleText = "作者"
	}

	userInfo := fmt.Sprintf("ID: %d", userID)
	if user != nil {
		if user.Username != "" {
			userInfo = fmt.Sprintf("@%s (ID: %d)", user.Username, userID)
		} else if user.FirstName != "" {
			userInfo = fmt.Sprintf("%s (ID: %d)", user.FirstName, userID)
		}
	}

	successText := fmt.Sprintf("✅ 添加成功!\n\n用户: %s\n角色: %s", userInfo, roleText)

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_admins"),
				tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
			},
		},
	}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        successText,
		ReplyMarkup: keyboard,
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 添加成功",
	})

	// 清除会话状态
	h.ClearSessionState(query.From.ID)
}

// handleDeleteAdmin 处理删除管理员请求
func (h *Handler) handleDeleteAdmin(query *telego.CallbackQuery, userID int64) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以删除管理员",
			ShowAlert:       true,
		})
		return
	}

	if userID == query.From.ID {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 不能删除自己",
			ShowAlert:       true,
		})
		return
	}

	user, _ := h.userRepo.GetByID(userID)
	userInfo := fmt.Sprintf("ID: %d", userID)
	if user != nil {
		if user.Username != "" {
			userInfo = fmt.Sprintf("@%s (ID: %d)", user.Username, userID)
		} else if user.FirstName != "" {
			userInfo = fmt.Sprintf("%s (ID: %d)", user.FirstName, userID)
		}
	}

	text := fmt.Sprintf("⚠️ 确认删除管理员?\n\n用户: %s\n\n此操作不可恢复", userInfo)

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton("✅ 确认删除").WithCallbackData(fmt.Sprintf("admin_del_confirm_%d", userID)),
				tu.InlineKeyboardButton("❌ 取消").WithCallbackData("admin_list_admins"),
			},
		},
	}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// handleDeleteAdminConfirm 处理确认删除管理员
func (h *Handler) handleDeleteAdminConfirm(query *telego.CallbackQuery, userID int64) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以删除管理员",
			ShowAlert:       true,
		})
		return
	}

	err := h.adminSvc.RemoveAdmin(userID, query.From.ID)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            fmt.Sprintf("❌ 删除失败: %v", err),
			ShowAlert:       true,
		})
		return
	}

	successText := "✅ 已删除管理员"

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_list_admins"),
				tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
			},
		},
	}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        successText,
		ReplyMarkup: keyboard,
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 删除成功",
	})
}
