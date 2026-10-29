package handlers

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/pkg/utils"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

func boolPtr(b bool) *bool {
	return &b
}

// 兼容别名:关键词列表统一使用 listPageSize/listCols (定义于 keyboard.go)
const keywordListPageSize = listPageSize

// HandleCallback 处理回调查询
func (h *Handler) HandleCallback(query *telego.CallbackQuery) {
	data := query.Data

	// 处理自助解封(不需要管理员权限)
	if data == "self_unban" {
		h.handleSelfUnban(query)
		return
	}

	// 其他回调需要管理员权限
	if !h.IsAdmin(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "无权限",
		})
		return
	}

	// 处理关键词列表翻页
	if strings.HasPrefix(data, "kw_list_") {
		h.handleKeywordListPage(query)
		return
	}

	// 处理 /no list 翻页
	if strings.HasPrefix(data, "nlw:page:") {
		h.handleDuplicateWhitelistPage(query)
		return
	}

	// 处理删除关键词
	if strings.HasPrefix(data, "del_kw_") {
		h.handleDeleteKeyword(query)
		return
	}

	if strings.HasPrefix(data, "del_no_") {
		h.handleDeleteDuplicateWhitelist(query)
		return
	}

	// 处理白名单删除
	if strings.HasPrefix(data, "del_wl_") {
		h.handleDeleteWhitelistUser(query)
		return
	}

	// 处理白名单翻页
	if strings.HasPrefix(data, "wlp:page:") {
		h.handleWhitelistPage(query)
		return
	}

	// 关闭面板:删除消息(无权删除时静默忽略)
	if data == "close" {
		h.handleClosePanel(query)
		return
	}

	// 处理 noop（无操作）
	if data == "noop" {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		})
		return
	}

	if strings.HasPrefix(data, "kw_") {
		h.handleKeywordAction(query)
		return
	}

	if strings.HasPrefix(data, "ban_") || strings.HasPrefix(data, "unban_") {
		h.handleBanAction(query)
		return
	}

	// 刷新用户状态
	if strings.HasPrefix(data, "refresh_user_") {
		h.handleRefreshUserStatus(query)
		return
	}

	// 管理员面板回调
	if strings.HasPrefix(data, "admin_") {
		h.handleAdminPanelCallback(query)
		return
	}

	// 克隆 Bot 启停回调(仅主 Bot 作者可用)
	if strings.HasPrefix(data, "clone_enable_") || strings.HasPrefix(data, "clone_disable_") {
		h.handleCloneToggle(query)
		return
	}
}

func keywordTypeCode(keywordType models.KeywordType) string {
	return string(keywordType)
}

func keywordTypeName(keywordType models.KeywordType) string {
	switch keywordType {
	case models.KeywordTypeName:
		return "昵称"
	case models.KeywordTypeBio:
		return "简介"
	case models.KeywordTypeMsg:
		return "消息"
	case models.KeywordTypeForward:
		return "转发来源"
	case models.KeywordTypeInvite:
		return "群名"
	default:
		return "未知"
	}
}

func parseKeywordTypeCode(typeCode string) (models.KeywordType, bool) {
	switch typeCode {
	case "name":
		return models.KeywordTypeName, true
	case "bio":
		return models.KeywordTypeBio, true
	case "msg":
		return models.KeywordTypeMsg, true
	case "forward":
		return models.KeywordTypeForward, true
	case "invite":
		return models.KeywordTypeInvite, true
	default:
		return "", false
	}
}

func (h *Handler) buildKeywordListPage(keywordType models.KeywordType, page int) (string, *telego.InlineKeyboardMarkup, int, int64, error) {
	if page < 1 {
		page = 1
	}

	// 先取 count 确定 totalPages,再按 listPageSize 拉本页
	offset := (page - 1) * listPageSize
	keywords, total, err := h.keywordSvc.GetKeywordsPaginated(keywordType, offset, listPageSize)
	if err != nil {
		return "", nil, 0, 0, err
	}
	if total == 0 {
		return "", nil, 0, 0, nil
	}

	page, totalPages := clampPage(int(total), page, listPageSize)
	// 收敛后 page 可能改变,需要重拉
	if newOffset := (page - 1) * listPageSize; newOffset != offset {
		keywords, total, err = h.keywordSvc.GetKeywordsPaginated(keywordType, newOffset, listPageSize)
		if err != nil {
			return "", nil, 0, 0, err
		}
	}

	buttons := make([]telego.InlineKeyboardButton, 0, len(keywords))
	for _, kw := range keywords {
		// callback 带上 page,删除后能停在当前页
		buttons = append(buttons, tu.InlineKeyboardButton(kw.Word).WithCallbackData(
			fmt.Sprintf("del_kw_%s_%d:%d", keywordTypeCode(keywordType), kw.ID, page),
		))
	}
	rows := chunkButtonsToRows(buttons, listCols)

	if nav := pageNavRow(page, totalPages, "kw_list_"+keywordTypeCode(keywordType)); nav != nil {
		rows = append(rows, nav)
	}
	rows = append(rows, closeRow())

	text := fmt.Sprintf("📋 <b>%s关键词</b> · 第 %d/%d 页 · 共 %d 个\n点击关键词即可删除", keywordTypeName(keywordType), page, totalPages, total)
	return text, &telego.InlineKeyboardMarkup{InlineKeyboard: rows}, totalPages, total, nil
}

func (h *Handler) renderKeywordListPage(chatID int64, messageID int, keywordType models.KeywordType, page int) error {
	text, keyboard, _, total, err := h.buildKeywordListPage(keywordType, page)
	if err != nil {
		return err
	}

	if total == 0 {
		text = fmt.Sprintf("📋 <b>%s关键词</b>\n\n暂无关键词", keywordTypeName(keywordType))
		keyboard = nil
	}

	_, err = h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})
	return err
}

// handleKeywordListPage 处理关键词列表翻页
func (h *Handler) handleKeywordListPage(query *telego.CallbackQuery) {
	data := query.Data
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID, Text: "数据格式错误"})
		return
	}

	typeStr := strings.TrimPrefix(parts[0], "kw_list_")
	page, _ := strconv.Atoi(parts[2])

	keywordType, ok := parseKeywordTypeCode(typeStr)
	if !ok {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID, Text: "类型错误"})
		return
	}

	err := h.renderKeywordListPage(query.Message.GetChat().ID, query.Message.GetMessageID(), keywordType, page)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID, Text: "刷新失败", ShowAlert: true})
		return
	}

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}

// handleDeleteKeyword 处理删除关键词
//
// callback data 兼容两种格式:
//
//	旧版: del_kw_<type>_<id>          → 删除后回到第 1 页
//	新版: del_kw_<type>_<id>:<page>   → 删除后停在 <page>
func (h *Handler) handleDeleteKeyword(query *telego.CallbackQuery) {
	parts := strings.Split(query.Data, "_")
	if len(parts) < 4 {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 参数错误",
			ShowAlert:       true,
		})
		return
	}

	keywordType := models.KeywordType(parts[2])

	idAndPage := parts[3]
	page := 1
	if idx := strings.Index(idAndPage, ":"); idx > 0 {
		if p, err := strconv.Atoi(idAndPage[idx+1:]); err == nil && p > 0 {
			page = p
		}
		idAndPage = idAndPage[:idx]
	}
	keywordID, err := strconv.ParseInt(idAndPage, 10, 64)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ ID解析失败",
			ShowAlert:       true,
		})
		return
	}

	// 先获取关键词信息(用于跨 Bot 同步删除)
	kw, _ := h.keywordRepo.GetByID(keywordID)

	if err := h.keywordSvc.DeleteKeyword(keywordID); err != nil {
		log.Printf("[Callback] Failed to delete keyword: %v", err)
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 删除失败",
			ShowAlert:       true,
		})
		return
	}

	if kw != nil && h.registry != nil {
		go h.registry.SyncKeywordDelete(string(kw.Type), kw.Word)
	}

	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()
	if err := h.renderKeywordListPage(chatID, messageID, keywordType, page); err != nil {
		log.Printf("[Callback] Failed to render keyword list after delete: %v", err)
	}

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 已删除",
	})
}

// handleKeywordAction 处理关键词操作
func (h *Handler) handleKeywordAction(query *telego.CallbackQuery) {
	idStr := strings.TrimPrefix(query.Data, "kw_")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return
	}

	// 获取关键词信息
	keyword, err := h.keywordRepo.GetByID(id)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "关键词不存在",
		})
		return
	}

	// 显示操作按钮
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("删除").WithCallbackData(fmt.Sprintf("del_kw_%d", id)),
		),
	)

	text := fmt.Sprintf("关键词: %s\n类型: %s", keyword.Word, keyword.Type)

	chatID := query.Message.GetChat().ID
	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// handleBanAction 处理封禁/解封操作
func (h *Handler) handleBanAction(query *telego.CallbackQuery) {
	parts := strings.Split(query.Data, "_")
	if len(parts) < 2 {
		return
	}

	action := parts[0]
	userIDStr := parts[1]
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return
	}

	var resultText string
	if action == "ban" {
		err = h.banService.BanUser(0, userID, "manual", "管理员手动封禁", "", "", query.From.ID, 0, false)
		resultText = "✅ 已封禁"
	} else {
		err = h.banService.UnbanUser(userID)
		resultText = "✅ 已解封"
	}

	if err != nil {
		log.Printf("[处理器] 封禁/解封错误: %v", err)
		resultText = "❌ 操作失败"
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            resultText,
			ShowAlert:       true,
		})
		return
	}

	// 操作成功后,更新原消息显示最新状态(直接查询数据库,无需清除缓存)
	user, record, _ := h.banService.GetUserStatus(userID)

	// 查询操作人信息(如果有封禁记录)
	var operatorUser *models.User
	if record != nil && record.BannedBy != 0 {
		operatorUser, _ = h.userRepo.GetByID(record.BannedBy)
	}

	// 使用统一的格式化函数构建消息
	text := utils.FormatUserInfoForPrivateChat(user, record, operatorUser)

	// 根据新状态显示对应的按钮(添加刷新按钮)
	var keyboard *telego.InlineKeyboardMarkup
	if user != nil && user.IsBanned {
		// 已封禁 - 显示解封和刷新按钮
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("🔓 解封用户").WithCallbackData(fmt.Sprintf("unban_%d", userID)),
				tu.InlineKeyboardButton("🔄 刷新状态").WithCallbackData(fmt.Sprintf("refresh_user_%d", userID)),
			),
		)
	} else {
		// 正常 - 显示封禁和刷新按钮
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("🔒 封禁用户").WithCallbackData(fmt.Sprintf("ban_%d", userID)),
				tu.InlineKeyboardButton("🔄 刷新状态").WithCallbackData(fmt.Sprintf("refresh_user_%d", userID)),
			),
		)
	}

	// 更新原消息
	chatID := query.Message.GetChat().ID
	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	// 显示操作结果提示
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            resultText,
	})
}

// handleRefreshUserStatus 处理刷新用户状态
func (h *Handler) handleRefreshUserStatus(query *telego.CallbackQuery) {
	// 解析: refresh_user_12345
	parts := strings.Split(query.Data, "_")
	if len(parts) < 3 {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 参数错误",
			ShowAlert:       true,
		})
		return
	}

	userID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 用户ID解析失败",
			ShowAlert:       true,
		})
		return
	}

	// 获取最新用户状态
	user, record, _ := h.banService.GetUserStatus(userID)

	// 查询操作人信息(如果有封禁记录)
	var operatorUser *models.User
	if record != nil && record.BannedBy != 0 {
		operatorUser, _ = h.userRepo.GetByID(record.BannedBy)
	}

	// 使用统一的格式化函数构建消息
	text := utils.FormatUserInfoForPrivateChat(user, record, operatorUser)

	// 根据状态显示对应的按钮
	var keyboard *telego.InlineKeyboardMarkup
	if user != nil && user.IsBanned {
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("🔓 解封用户").WithCallbackData(fmt.Sprintf("unban_%d", userID)),
				tu.InlineKeyboardButton("🔄 刷新状态").WithCallbackData(fmt.Sprintf("refresh_user_%d", userID)),
			),
		)
	} else {
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("🔒 封禁用户").WithCallbackData(fmt.Sprintf("ban_%d", userID)),
				tu.InlineKeyboardButton("🔄 刷新状态").WithCallbackData(fmt.Sprintf("refresh_user_%d", userID)),
			),
		)
	}

	// 更新消息
	chatID := query.Message.GetChat().ID
	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	// 显示刷新成功提示
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 已刷新",
	})
}

// handleSelfUnban 处理自助解除(永封/临时拉黑/禁言 统一入口)
func (h *Handler) handleSelfUnban(query *telego.CallbackQuery) {
	userID := query.From.ID

	// 获取用户当前状态(快照,用于后续工作群通知)
	user, _, err := h.banService.GetUserStatus(userID)
	if err != nil || user == nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 获取用户信息失败",
			ShowAlert:       true,
		})
		return
	}

	// 若非永久封禁, 抓取活跃记录用于展示
	var activeRecord *models.BanRecord
	if !user.IsBanned {
		activeRecord, _ = h.banService.GetLatestActiveRecord(userID,
			models.BanTypeManual, models.BanTypeFCommand, models.BanTypeMute)
	}

	// 执行自助解除(综合处理永封/临时拉黑/禁言)
	releaseType, err := h.banService.SelfRelease(userID)
	if err != nil {
		log.Printf("[Callback] 自助解除失败: userID=%d err=%v", userID, err)
		msg := err.Error()
		if !strings.HasPrefix(msg, "您") {
			msg = "解除失败,请稍后再试"
		}
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ " + msg,
			ShowAlert:       true,
		})
		return
	}

	// 根据解除类型给出成功文案
	var title, toast string
	switch releaseType {
	case "mute":
		title = "✅ 自助解除禁言成功"
		toast = "✅ 已解除禁言"
	case "temp_ban":
		title = "✅ 自助解除临时拉黑成功"
		toast = "✅ 已解除拉黑"
	default:
		title = "✅ 自助解封成功"
		toast = "✅ 解封成功"
	}

	successText := title + "\n\n"
	successText += "您已恢复正常,请谨慎发言,避免再次违规。\n"
	successText += "⚠️ 您已使用自助解除机会,后续违规将无法再次自助解除。"

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:    tu.ID(query.Message.GetChat().ID),
		MessageID: query.Message.GetMessageID(),
		Text:      successText,
		ParseMode: "HTML",
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            toast,
	})

	// 工作群通知(仅告知管理员用户已自助解除, 不含原封禁广播那么详细的信息)
	h.sendSelfReleaseNotification(user, activeRecord, releaseType)
}

// sendSelfReleaseNotification 发送自助解除通知到工作群(简短版)
func (h *Handler) sendSelfReleaseNotification(user *models.User, activeRecord *models.BanRecord, releaseType string) {
	var title string
	switch releaseType {
	case "mute":
		title = "🔊 用户自助解除禁言"
	case "temp_ban":
		title = "🔓 用户自助解除临时拉黑"
	default:
		title = "🔓 用户自助解封"
	}

	text := title + "\n\n"
	text += fmt.Sprintf("用户ID: <code>%d</code>\n", user.ID)
	if user.Username != "" {
		text += fmt.Sprintf("用户名: @%s\n", user.Username)
	}
	if user.FirstName != "" {
		name := user.FirstName
		if user.LastName != "" {
			name += " " + user.LastName
		}
		text += fmt.Sprintf("姓名: %s\n", htmlEscape(name))
	}

	// 展示原处罚原因
	switch releaseType {
	case "mute", "temp_ban":
		if activeRecord != nil && activeRecord.Reason != "" {
			text += fmt.Sprintf("原处罚原因: %s\n", htmlEscape(activeRecord.Reason))
		}
	default:
		if user.BanReason != "" {
			text += fmt.Sprintf("原封禁原因: %s\n", htmlEscape(user.BanReason))
		}
	}

	text += fmt.Sprintf("解除时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	text += "\n⚠️ 该用户已用掉唯一一次自助解除机会"

	workGroup := h.WorkGroupID()
	if workGroup == 0 {
		return
	}
	h.notifySvc.SendMessage(workGroup, text)
}

// handleAdminPanelCallback 处理管理员面板回调
func (h *Handler) handleAdminPanelCallback(query *telego.CallbackQuery) {
	data := query.Data

	// 处理添加管理员
	if data == "admin_add_admin" {
		h.handleAddAdmin(query)
		return
	}

	// 处理角色选择: admin_role_config_123456 或 admin_role_author_123456
	if strings.HasPrefix(data, "admin_role_") {
		parts := strings.Split(data, "_")
		if len(parts) >= 4 {
			role := parts[2] // config 或 author
			userID, _ := strconv.ParseInt(parts[3], 10, 64)
			h.handleAdminRoleSelection(query, userID, role)
		}
		return
	}

	// 处理删除管理员: admin_del_123456
	if strings.HasPrefix(data, "admin_del_confirm_") {
		userIDStr := strings.TrimPrefix(data, "admin_del_confirm_")
		userID, _ := strconv.ParseInt(userIDStr, 10, 64)
		h.handleDeleteAdminConfirm(query, userID)
		return
	}

	if strings.HasPrefix(data, "admin_del_") {
		userIDStr := strings.TrimPrefix(data, "admin_del_")
		userID, _ := strconv.ParseInt(userIDStr, 10, 64)
		h.handleDeleteAdmin(query, userID)
		return
	}

	// 检测开关面板的切换/刷新动作
	if strings.HasPrefix(data, "admin_cfg_") {
		h.handleConfigToggle(query)
		return
	}

	switch data {
	case "admin_main":
		h.showAdminMainPanel(query)
	case "admin_config":
		h.showConfigPanel(query)
	case "admin_admins":
		h.showAdminAdminsPanel(query)
	case "admin_keywords":
		h.showAdminKeywordsPanel(query)
	case "admin_kw_name":
		h.showKeywordList(query, models.KeywordTypeName)
	case "admin_kw_bio":
		h.showKeywordList(query, models.KeywordTypeBio)
	case "admin_kw_msg":
		h.showKeywordList(query, models.KeywordTypeMsg)
	case "admin_kw_forward":
		h.showKeywordList(query, models.KeywordTypeForward)
	case "admin_kw_invite":
		h.showKeywordList(query, models.KeywordTypeInvite)
	case "admin_list_admins":
		h.showAdminList(query)
	case "admin_bindings":
		h.showBindingsPanel(query)
	case "admin_bind_work":
		h.handleStartBindWork(query)
	case "admin_bind_broadcast":
		h.handleStartBindBroadcast(query)
	case "admin_unbind_work":
		h.handleUnbindWork(query)
	case "admin_unbind_broadcast":
		h.handleUnbindBroadcast(query)
	case "admin_clones":
		h.showCloneListPanel(query)
	default:
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "功能开发中",
		})
	}
}

// showAdminMainPanel 显示主面板(含概览摘要)
func (h *Handler) showAdminMainPanel(query *telego.CallbackQuery) {
	text := h.buildAdminMainText()
	keyboard := h.buildAdminMainKeyboard()

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// buildAdminMainText 主面板文案: 带概览摘要
func (h *Handler) buildAdminMainText() string {
	text := "🛠️ <b>管理员控制面板</b>\n\n"

	adminCount, _ := h.adminSvc.CountAdmins()
	kwCounts, _ := h.keywordRepo.CountByType()
	var kwTotal int64
	for _, c := range kwCounts {
		kwTotal += c
	}

	text += fmt.Sprintf("👥 管理员 <b>%d</b> 位 · 📋 关键词 <b>%d</b> 个\n", adminCount, kwTotal)

	if cfg, err := h.settingsRepo.Get(); err == nil && cfg != nil {
		workStatus := "未绑定"
		if cfg.WorkGroupID != 0 {
			workStatus = "已绑定"
		}
		broadcastStatus := "未绑定"
		if cfg.BroadcastChatID != 0 {
			broadcastStatus = "已绑定"
		}
		text += fmt.Sprintf("📌 工作群 %s · 📡 播报 %s", workStatus, broadcastStatus)
	}

	return text
}

// buildAdminMainKeyboard 主面板按钮:2 列紧凑布局
func (h *Handler) buildAdminMainKeyboard() *telego.InlineKeyboardMarkup {
	rows := [][]telego.InlineKeyboardButton{
		{
			tu.InlineKeyboardButton("👥 管理员").WithCallbackData("admin_admins"),
			tu.InlineKeyboardButton("📋 关键词").WithCallbackData("admin_keywords"),
		},
		{
			tu.InlineKeyboardButton("🛡️ 检测开关").WithCallbackData("admin_config"),
			tu.InlineKeyboardButton("📌 工作群绑定").WithCallbackData("admin_bindings"),
		},
	}
	if h.isMain {
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("🤖 克隆 Bot 列表").WithCallbackData("admin_clones"),
		})
	}
	rows = append(rows, closeRow())
	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// showBindingsPanel 显示工作群/播报频道绑定面板
func (h *Handler) showBindingsPanel(query *telego.CallbackQuery) {
	cfg, _ := h.settingsRepo.Get()
	text := h.renderBindings()

	// 绑定/解绑按钮:有绑定时显示"解绑";没绑时显示"绑定"
	workBtn := tu.InlineKeyboardButton("➕ 绑定工作群").WithCallbackData("admin_bind_work")
	if cfg != nil && cfg.WorkGroupID != 0 {
		workBtn = tu.InlineKeyboardButton("🗑 解绑工作群").WithCallbackData("admin_unbind_work")
	}
	broadcastBtn := tu.InlineKeyboardButton("📡 绑定播报").WithCallbackData("admin_bind_broadcast")
	if cfg != nil && cfg.BroadcastChatID != 0 {
		broadcastBtn = tu.InlineKeyboardButton("🗑 解绑播报").WithCallbackData("admin_unbind_broadcast")
	}
	rows := [][]telego.InlineKeyboardButton{
		{workBtn, broadcastBtn},
		{
			tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_main"),
			tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
		},
	}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: &telego.InlineKeyboardMarkup{InlineKeyboard: rows},
		ParseMode:   "HTML",
	})
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}

// handleStartBindWork 进入绑定工作群会话
func (h *Handler) handleStartBindWork(query *telego.CallbackQuery) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以绑定",
			ShowAlert:       true,
		})
		return
	}
	h.SetSessionState(query.From.ID, sessionBindingWork, addBotPendingTTL)
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "请按提示发送工作群信息",
	})
	h.notifySvc.SendMessage(query.Message.GetChat().ID,
		"📌 绑定工作群\n\n请把工作群的 chat id 发给我(如 <code>-1001234567890</code>),\n"+
			"或转发该群里\"匿名管理员\"的发言;\n发送 /cancel 可取消")
}

// handleStartBindBroadcast 进入绑定播报频道会话
func (h *Handler) handleStartBindBroadcast(query *telego.CallbackQuery) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以绑定",
			ShowAlert:       true,
		})
		return
	}
	h.SetSessionState(query.From.ID, sessionBindingBroadcast, addBotPendingTTL)
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "请按提示发送播报频道信息",
	})
	h.notifySvc.SendMessage(query.Message.GetChat().ID,
		"📡 绑定播报频道\n\n请直接转发\"播报频道\"内的一条消息,\n"+
			"或直接发送 chat id(如 <code>-1001234567890</code>);\n发送 /cancel 可取消")
}

// handleUnbindWork 解绑工作群
func (h *Handler) handleUnbindWork(query *telego.CallbackQuery) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以解绑",
			ShowAlert:       true,
		})
		return
	}
	if err := h.settingsRepo.ClearWorkGroup(); err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 解绑失败",
			ShowAlert:       true,
		})
		return
	}
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 已解绑工作群",
	})
	h.showBindingsPanel(query)
}

// handleUnbindBroadcast 解绑播报频道
func (h *Handler) handleUnbindBroadcast(query *telego.CallbackQuery) {
	if !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 只有作者可以解绑",
			ShowAlert:       true,
		})
		return
	}
	if err := h.settingsRepo.ClearBroadcastChat(); err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 解绑失败",
			ShowAlert:       true,
		})
		return
	}
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 已解绑播报频道",
	})
	h.showBindingsPanel(query)
}

// showCloneListPanel 显示克隆 Bot 列表(仅主 Bot 作者可见)
func (h *Handler) showCloneListPanel(query *telego.CallbackQuery) {
	if !h.isMain || !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 仅主 Bot 作者可用",
			ShowAlert:       true,
		})
		return
	}
	text, kb := h.renderCloneList()
	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: kb,
		ParseMode:   "HTML",
	})
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}

// handleCloneToggle 启停克隆 Bot
func (h *Handler) handleCloneToggle(query *telego.CallbackQuery) {
	if !h.isMain || !h.IsAuthor(query.From.ID) {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 仅主 Bot 作者可用",
			ShowAlert:       true,
		})
		return
	}
	enable := strings.HasPrefix(query.Data, "clone_enable_")
	idStr := strings.TrimPrefix(strings.TrimPrefix(query.Data, "clone_enable_"), "clone_disable_")
	botUserID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 参数错误",
			ShowAlert:       true,
		})
		return
	}
	if h.registry == nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 注册表未就绪",
			ShowAlert:       true,
		})
		return
	}
	if err := h.registry.SetCloneEnabled(botUserID, enable); err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 操作失败: " + err.Error(),
			ShowAlert:       true,
		})
		return
	}
	if enable {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "✅ 已启用",
		})
	} else {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "✅ 已停用",
		})
	}

	text, kb := h.renderCloneList()
	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: kb,
		ParseMode:   "HTML",
	})
}

// showAdminAdminsPanel 显示管理员管理子面板
func (h *Handler) showAdminAdminsPanel(query *telego.CallbackQuery) {
	count, err := h.adminSvc.CountAdmins()
	if err != nil {
		count = 0
	}

	text := fmt.Sprintf("👥 <b>管理员管理</b>\n\n当前管理员: %d 位", count)

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton("➕ 添加").WithCallbackData("admin_add_admin"),
				tu.InlineKeyboardButton("📋 查看列表").WithCallbackData("admin_list_admins"),
			},
			{
				tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_main"),
				tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
			},
		},
	}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// showAdminKeywordsPanel 显示关键词管理子面板(含各类型数量)
func (h *Handler) showAdminKeywordsPanel(query *telego.CallbackQuery) {
	counts, _ := h.keywordRepo.CountByType()

	text := "📋 <b>关键词管理</b>\n\n"
	text += fmt.Sprintf("昵称 <b>%d</b> · 简介 <b>%d</b> · 消息 <b>%d</b>\n转发来源 <b>%d</b> · 群名 <b>%d</b>\n\n",
		counts[models.KeywordTypeName], counts[models.KeywordTypeBio],
		counts[models.KeywordTypeMsg], counts[models.KeywordTypeForward],
		counts[models.KeywordTypeInvite])
	text += "点击进入对应列表,可查看和删除"

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton(fmt.Sprintf("📝 昵称 (%d)", counts[models.KeywordTypeName])).WithCallbackData("admin_kw_name"),
				tu.InlineKeyboardButton(fmt.Sprintf("👤 简介 (%d)", counts[models.KeywordTypeBio])).WithCallbackData("admin_kw_bio"),
			},
			{
				tu.InlineKeyboardButton(fmt.Sprintf("💬 消息 (%d)", counts[models.KeywordTypeMsg])).WithCallbackData("admin_kw_msg"),
				tu.InlineKeyboardButton(fmt.Sprintf("🔀 转发来源 (%d)", counts[models.KeywordTypeForward])).WithCallbackData("admin_kw_forward"),
			},
			{
				tu.InlineKeyboardButton(fmt.Sprintf("🔗 群名 (%d)", counts[models.KeywordTypeInvite])).WithCallbackData("admin_kw_invite"),
			},
			{
				tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_main"),
				tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
			},
		},
	}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// showKeywordList 显示关键词列表
func (h *Handler) showKeywordList(query *telego.CallbackQuery, keywordType models.KeywordType) {
	err := h.renderKeywordListPage(query.Message.GetChat().ID, query.Message.GetMessageID(), keywordType, 1)
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 获取列表失败",
			ShowAlert:       true,
		})
		return
	}

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// showAdminList 显示管理员列表
func (h *Handler) showAdminList(query *telego.CallbackQuery) {
	details, err := h.adminSvc.ListAdminDetails()
	if err != nil {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 获取列表失败",
			ShowAlert:       true,
		})
		return
	}

	if len(details) == 0 {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "暂无管理员",
		})
		return
	}

	// 紧凑布局:文字区只放序号 + 角色 + 身份信息;按钮区每行 1 个,展示"删除 #N · 名称",一眼对应
	text := fmt.Sprintf("👥 <b>管理员列表</b> · 共 %d 位\n\n", len(details))

	var rows [][]telego.InlineKeyboardButton

	for i, detail := range details {
		admin := detail.Admin
		user := detail.User

		roleTag := "🛡 配置管理员"
		if admin.Role == models.RoleAuthor {
			roleTag = "👑 作者"
		}

		displayName := fmt.Sprintf("ID:%d", admin.UserID)
		if user != nil {
			switch {
			case user.Username != "":
				displayName = "@" + user.Username
			case user.FirstName != "":
				displayName = user.FirstName
			}
		}

		text += fmt.Sprintf("%d. %s · <code>%d</code> %s\n", i+1, roleTag, admin.UserID, htmlEscape(displayName))

		// 按钮: 非作者可删除,作者只显示锁定状态
		var btn telego.InlineKeyboardButton
		if admin.Role != models.RoleAuthor {
			btn = tu.InlineKeyboardButton(fmt.Sprintf("🗑 删除 #%d %s", i+1, displayName)).
				WithCallbackData(fmt.Sprintf("admin_del_%d", admin.UserID))
		} else {
			btn = tu.InlineKeyboardButton(fmt.Sprintf("🔒 #%d %s (不可删除)", i+1, displayName)).WithCallbackData("noop")
		}
		rows = append(rows, []telego.InlineKeyboardButton{btn})
	}

	// 底部:返回 + 关闭并排,一行解决
	rows = append(rows, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_admins"),
		tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
	})

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: rows}

	h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(query.Message.GetChat().ID),
		MessageID:   query.Message.GetMessageID(),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// handleClosePanel 处理统一的"✖️ 关闭"按钮:删除当前消息;无权时仅清空键盘
func (h *Handler) handleClosePanel(query *telego.CallbackQuery) {
	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()

	err := h.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
	})
	if err != nil {
		// bot 没有删除权限或消息超时,退化为清空键盘
		_, _ = h.bot.EditMessageReplyMarkup(context.Background(), &telego.EditMessageReplyMarkupParams{
			ChatID:    tu.ID(chatID),
			MessageID: messageID,
		})
	}
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}
