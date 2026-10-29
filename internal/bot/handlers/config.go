package handlers

import (
	"context"
	"log"

	"telegram-ban-bot/internal/database/models"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// showConfigPanel 在 /admin 主面板里展示「检测开关」子面板
// 入口: admin_config 回调
func (h *Handler) showConfigPanel(query *telego.CallbackQuery) {
	text, keyboard := h.buildConfigPanel()
	if _, err := h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	}); err != nil {
		log.Printf("[处理器] admin_config 渲染面板失败: %v", err)
	}
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}

// buildConfigPanel 渲染当前检测开关状态及切换按钮
func (h *Handler) buildConfigPanel() (string, *telego.InlineKeyboardMarkup) {
	cfg, err := h.settingsRepo.Get()
	if err != nil {
		log.Printf("[处理器] 检测开关读取配置失败: %v", err)
	}
	waterDisabled := cfg != nil && cfg.WaterDetectDisabled
	spamDisabled := cfg != nil && cfg.SpamDetectDisabled
	forwardDisabled := cfg != nil && cfg.ForwardBurstDisabled
	botAction := models.NonAdminBotActionNone
	if cfg != nil {
		botAction = cfg.NonAdminBotAction
	}

	text := "🛡️ <b>检测开关</b>\n\n"
	text += "• 水群检测: " + statusBadge(!waterDisabled) + "\n"
	text += "• 刷屏检测: " + statusBadge(!spamDisabled) + "\n"
	text += "• 并发转发: " + statusBadge(!forwardDisabled) + "\n"
	text += "• 非管理员Bot: " + nonAdminBotActionBadge(botAction) + "\n\n"
	text += "点击按钮即可切换状态;切换后立即生效。"

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton(toggleButtonLabel("水群", !waterDisabled)).WithCallbackData("admin_cfg_toggle_water"),
				tu.InlineKeyboardButton(toggleButtonLabel("刷屏", !spamDisabled)).WithCallbackData("admin_cfg_toggle_spam"),
			},
			{
				tu.InlineKeyboardButton(toggleButtonLabel("并发转发", !forwardDisabled)).WithCallbackData("admin_cfg_toggle_burst"),
			},
			{
				tu.InlineKeyboardButton(nonAdminBotActionButtonLabel(botAction)).WithCallbackData("admin_cfg_toggle_bot"),
			},
			{
				tu.InlineKeyboardButton("🔄 刷新").WithCallbackData("admin_cfg_refresh"),
				tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_main"),
				tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
			},
		},
	}
	return text, keyboard
}

func statusBadge(enabled bool) string {
	if enabled {
		return "✅ 已开启"
	}
	return "⛔ 已关闭"
}

// toggleButtonLabel 开关按钮文案: emoji 直接反映当前状态,点击即切换
func toggleButtonLabel(name string, enabled bool) string {
	if enabled {
		return "✅ " + name
	}
	return "⛔ " + name
}

// handleConfigToggle 处理检测开关切换/刷新; 由 admin 面板分发
func (h *Handler) handleConfigToggle(query *telego.CallbackQuery) {
	cfg, err := h.settingsRepo.Get()
	if err != nil {
		log.Printf("[处理器] admin_cfg 读取配置失败: %v", err)
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 读取配置失败",
			ShowAlert:       true,
		})
		return
	}

	var toast string
	switch query.Data {
	case "admin_cfg_toggle_water":
		newDisabled := !cfg.WaterDetectDisabled
		if err := h.settingsRepo.SetWaterDetectDisabled(newDisabled); err != nil {
			log.Printf("[处理器] admin_cfg_toggle_water 写入失败: %v", err)
			h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "❌ 切换失败",
				ShowAlert:       true,
			})
			return
		}
		if newDisabled {
			toast = "⛔ 水群检测已关闭"
		} else {
			toast = "✅ 水群检测已开启"
		}
	case "admin_cfg_toggle_spam":
		newDisabled := !cfg.SpamDetectDisabled
		if err := h.settingsRepo.SetSpamDetectDisabled(newDisabled); err != nil {
			log.Printf("[处理器] admin_cfg_toggle_spam 写入失败: %v", err)
			h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "❌ 切换失败",
				ShowAlert:       true,
			})
			return
		}
		if newDisabled {
			toast = "⛔ 刷屏检测已关闭"
		} else {
			toast = "✅ 刷屏检测已开启"
		}
	case "admin_cfg_toggle_burst":
		newDisabled := !cfg.ForwardBurstDisabled
		if err := h.settingsRepo.SetForwardBurstDisabled(newDisabled); err != nil {
			log.Printf("[处理器] admin_cfg_toggle_burst 写入失败: %v", err)
			h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "❌ 并发转发开关没有写入",
				ShowAlert:       true,
			})
			return
		}
		if newDisabled {
			toast = "⛔ 并发转发已关闭"
		} else {
			toast = "✅ 并发转发已开启"
		}
	case "admin_cfg_toggle_bot":
		newAction := nextNonAdminBotAction(cfg.NonAdminBotAction)
		if err := h.settingsRepo.SetNonAdminBotAction(newAction); err != nil {
			log.Printf("[处理器] admin_cfg_toggle_bot 写入失败: %v", err)
			h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "❌ 切换失败",
				ShowAlert:       true,
			})
			return
		}
		toast = "🤖 非管理员Bot: " + nonAdminBotActionBadge(newAction)
	case "admin_cfg_refresh":
		toast = "✅ 已刷新"
	default:
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "未知操作",
		})
		return
	}

	text, keyboard := h.buildConfigPanel()
	if _, err := h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	}); err != nil {
		log.Printf("[处理器] admin_cfg 渲染面板失败: %v", err)
	}

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            toast,
	})
}

// duplicateDetectionFlags 读取检测开关, 返回 (spamEnabled, waterEnabled)
// 读取失败时按"开启"兜底, 避免误关线上检测
func (h *Handler) duplicateDetectionFlags() (bool, bool) {
	if h.settingsRepo == nil {
		return true, true
	}
	cfg, err := h.settingsRepo.Get()
	if err != nil {
		log.Printf("[处理器] 读取检测开关失败,按默认开启处理: %v", err)
		return true, true
	}
	return !cfg.SpamDetectDisabled, !cfg.WaterDetectDisabled
}

// nonAdminBotActionBadge 显示非管理员 Bot 处置动作的状态文案
func nonAdminBotActionBadge(action models.NonAdminBotAction) string {
	switch action {
	case models.NonAdminBotActionDelete:
		return "🗑 仅删除"
	case models.NonAdminBotActionBan:
		return "⛔ 删除+拉黑"
	default:
		return "⚪ 不处理"
	}
}

// nonAdminBotActionButtonLabel 开关按钮文案: 展示当前状态,点击切换到下一状态
func nonAdminBotActionButtonLabel(action models.NonAdminBotAction) string {
	switch action {
	case models.NonAdminBotActionDelete:
		return "🤖 非管理员Bot: 🗑 仅删除"
	case models.NonAdminBotActionBan:
		return "🤖 非管理员Bot: ⛔ 删除+拉黑"
	default:
		return "🤖 非管理员Bot: ⚪ 不处理"
	}
}

// nextNonAdminBotAction 三态循环: 不处理 → 仅删除 → 删除+拉黑 → 不处理
func nextNonAdminBotAction(current models.NonAdminBotAction) models.NonAdminBotAction {
	switch current {
	case models.NonAdminBotActionNone:
		return models.NonAdminBotActionDelete
	case models.NonAdminBotActionDelete:
		return models.NonAdminBotActionBan
	default:
		return models.NonAdminBotActionNone
	}
}
