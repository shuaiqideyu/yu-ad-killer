package handlers

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/service"
	"telegram-ban-bot/pkg/utils"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	repeatedAdWindow    = 10 * time.Second
	repeatedAdThreshold = 3

	userSpamWindow        = 5 * time.Second
	userSpamThreshold     = 5
	userWaterThreshold    = 5
	userMessageHistoryTTL = 10 * time.Minute
	userMessageHistoryMax = 200

	// 多选转发会在约 1 秒内落成多条消息，日期跨秒时用 2 秒才不会把同一次转发拆开。
	forwardBurstWindow    = 2 * time.Second
	forwardBurstThreshold = 4

	// 进群检测去重窗口: 同一次进群可能同时触发"加入群组"系统消息与 chat_member 更新
	joinDedupTTL = 30 * time.Second
)

type repeatedAdKey struct {
	ChatID int64
	Text   string
}

type repeatedAdMessage struct {
	UserID    int64
	MessageID int
	SentAt    time.Time
	Handled   bool
}

type userChatKey struct {
	ChatID int64
	UserID int64
}

type userDuplicateMessage struct {
	MessageID int
	Text      string
	SentAt    time.Time
	Handled   bool
}

type userConsecutiveMessages struct {
	Text     string
	Messages []userDuplicateMessage
}

type forwardBurstHit struct {
	MessageID int
	SentAt    time.Time
	Handled   bool
}

func messageHasTGChatLink(message *telego.Message) bool {
	if message == nil || message.ReplyMarkup == nil {
		return false
	}
	for _, row := range message.ReplyMarkup.InlineKeyboard {
		for _, btn := range row {
			if len(utils.ExtractTGChatLinks(btn.URL)) > 0 {
				return true
			}
		}
	}
	return false
}

// HandleMessage 处理普通消息（包括所有类型：文字、贴纸、图片、视频等）
func (h *Handler) HandleMessage(message *telego.Message) {
	if message.From == nil {
		return
	}

	// 注册管理群(用于封禁时全群踢出+清理消息)
	if message.Chat.Type == "supergroup" || message.Chat.Type == "group" {
		h.banService.RegisterChat(message.Chat.ID)
	}

	// 新成员进群: 进群系统消息无文字内容, 会被下方 hasTextContent 过滤,
	// 这里提前对新成员做昵称/简介检测, 不必等其发出第一条消息
	if len(message.NewChatMembers) > 0 {
		h.handleNewChatMembers(message)
		return
	}

	// Bot API 10.0 Guest Bot: 用户在群里"召唤"未入群的 guest bot 发消息(典型广告号绕过手段)
	if h.handleGuestBotMessage(message) {
		return
	}

	// 群组 Telegram 管理员/群主白名单
	if h.isChatTelegramAdminMessage(message) {
		return
	}

	// 非管理员 Bot 发言检测: 经过上面的群管白名单过滤后,
	// 此处 message.From.IsBot==true 的一定是非管理员 Bot
	if h.handleNonAdminBotMessage(message) {
		return
	}

	// 用户白名单须在自动处罚之前(含转发来源屏蔽),避免加白用户被误封
	if h.whitelistSvc != nil {
		if h.whitelistSvc.IsWhitelisted(message.From.ID) {
			return
		}
		if message.SenderChat != nil && h.whitelistSvc.IsWhitelisted(message.SenderChat.ID) {
			return
		}
	}

	// 转发来源屏蔽: 消息为转发且转发来源名称/用户名命中关键词时,删除+封禁转发者
	if h.handleForwardSourceMessage(message) {
		return
	}

	// 同一人短时间连续转发满 4 条: 永久禁言并删掉这几条
	if h.checkForwardBurst(message) {
		return
	}

	// 遮掩格式(spoiler / has_media_spoiler): 诱导点击广告号,踢出不永封
	if h.handleSpoilerMessage(message) {
		return
	}

	// 已封禁用户再发言: 走 BanUser 幂等补踢(RevokeMessages + 显式删触发消息 + 跨群)
	if isBanned, _ := h.banService.IsBanned(message.From.ID); isBanned {
		log.Printf("[处理器] 已封禁用户 %d 发送消息,清理全部消息并踢出", message.From.ID)
		if err := h.banService.BanUser(
			message.Chat.ID,
			message.From.ID,
			models.BanTypeManual,
			"已封禁用户发言补踢",
			"",
			h.extractMessageText(message),
			0,
			message.MessageID,
			false,
		); err != nil {
			log.Printf("[处理器] 已封禁用户补踢失败: user=%d err=%v", message.From.ID, err)
		}
		return
	}

	// 用户名片: 自己的、别人的、仅电话、带 Telegram 账号、vCard 文件一律拉黑。
	// 必须放在 hasTextContent 过滤之前(名片消息通常无 text/caption)
	if h.handleContactMessage(message) {
		return
	}

	// 文字、说明，以及没文字但按钮里挂了 Telegram 链接的消息都要检测。
	// 纯贴纸、纯图片不检测。
	hasTextContent := message.Text != "" || message.Caption != ""
	if !hasTextContent && !messageHasTGChatLink(message) {
		return
	}

	// 跳过命令消息
	if message.Text != "" && message.Text[0] == '/' {
		return
	}

	// 单用户重复消息检测: 快速刷屏优先,其次处理无时间限制的连续水群。
	if h.checkUserDuplicateMessage(message) {
		return
	}

	// 10秒内重复发送3条完全相同且包含 @ 的消息,按协议广告号批量永久禁言并清理消息。
	if h.checkRepeatedAd(message) {
		return
	}

	// 执行检测
	violated, banType, reason, err := h.detector.CheckAll(message)
	if err != nil {
		log.Printf("[处理器] 检测错误: %v", err)
		return
	}

	if !violated {
		return
	}

	// 触发封禁
	log.Printf("[处理器] 用户 %d 违规: 类型=%s, 原因=%s", message.From.ID, banType, reason)

	var keyword, messageText string
	if banType != "external_reply" {
		// 提取关键词
		if len(reason) > 0 {
			keyword = reason
		}
	}
	messageText = message.Text
	if messageText == "" {
		messageText = message.Caption
	}

	// 执行封禁(内部会先删除消息再封禁)
	err = h.banService.BanUser(
		message.Chat.ID,
		message.From.ID,
		models.BanType(banType),
		reason,
		keyword,
		messageText,
		0,                 // 自动封禁
		message.MessageID, // 违规消息ID
		false,             // 实时检测封禁,立即踢出
	)
	if err != nil {
		log.Printf("[处理器] 封禁用户失败: %v", err)
		return
	}

	autoReason := reason
	if autoReason == "" {
		autoReason = "自动检测命中"
	}
	if banType != "" && reason != "" {
		autoReason = fmt.Sprintf("%s命中: %s", banTypeChinese(models.BanType(banType)), reason)
	}
	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:        &message.Chat,
		UserID:      message.From.ID,
		UserName:    utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:    message.From.Username,
		Action:      "拉黑",
		Duration:    "永久",
		Reason:      autoReason,
		MessageText: messageText,
		OperatorID:  0,
	})
}

// handleNewChatMembers 处理"加入群组"系统消息携带的新成员, 逐个走进群检测
func (h *Handler) handleNewChatMembers(message *telego.Message) {
	for i := range message.NewChatMembers {
		h.checkJoinedMember(message.Chat, message.NewChatMembers[i], message.MessageID)
	}
}

// HandleChatMemberUpdated 处理 chat_member 更新, 覆盖邀请链接、管理员审批、加入请求等
// 不产生"加入群组"系统消息的进群方式
//
// 前提: Telegram 仅在机器人为群管理员时才推送 chat_member 更新; 且需在 getUpdates 的
// allowed_updates 中显式声明(见 runtime.registerHandlers)
func (h *Handler) HandleChatMemberUpdated(update *telego.ChatMemberUpdated) {
	if update == nil {
		return
	}
	if update.Chat.Type != telego.ChatTypeSupergroup && update.Chat.Type != telego.ChatTypeGroup {
		return
	}
	h.banService.RegisterChat(update.Chat.ID)
	// 成员身份变化可能包含升/降管理员，立即失效管理员缓存，避免最长 5 分钟的旧数据窗口。
	h.cache.InvalidateChatAdmins(update.Chat.ID)
	if !isJoinTransition(update.OldChatMember, update.NewChatMember) {
		return
	}
	// chat_member 更新无系统消息可删, joinMsgID 传 0
	h.checkJoinedMember(update.Chat, update.NewChatMember.MemberUser(), 0)
}

// isJoinTransition 判断成员状态变化是否为"从不在群到在群"(即一次进群)
// 进群方式无关: 普通加入 / 邀请链接 / 管理员审批 / 加入请求通过 都会落到该状态跃迁
func isJoinTransition(oldMember, newMember telego.ChatMember) bool {
	if newMember == nil || !newMember.MemberIsMember() {
		return false
	}
	if oldMember == nil {
		return true
	}
	return !oldMember.MemberIsMember()
}

// checkJoinedMember 对单个进群成员执行昵称/简介检测与处置,
// 供"加入群组"系统消息与 chat_member 更新两个入口复用
//
// 背景:
//   - 广告号换号进群后, 若昵称/简介已含关键词, 旧逻辑要等其发出第一条带文字消息才检测,
//     这段时间广告号已潜伏在群内
//
// 处置:
//  1. 去重(同一次进群可能同时触发系统消息与 chat_member 更新), 跳过 bot / 管理员 / 白名单
//  2. 已封禁号再次进群: 走 BanUser 幂等补踢并清理进群提示
//  3. 构造仅含 From 的消息复用 CheckAllFreshBio(无文本时只跑昵称+简介), 命中即封禁,
//     删除进群提示消息(joinMsgID>0 时), 并广播到播报频道
//
// 局限: 进群时昵称/简介干净、纯靠后续消息打广告的号, 仍由 HandleMessage 兜底
// joinMsgID: 进群系统消息ID, 命中后删除; chat_member 更新无系统消息, 传 0
func (h *Handler) checkJoinedMember(chat telego.Chat, member telego.User, joinMsgID int) {
	if member.ID == 0 || member.IsBot {
		return
	}
	chatID := chat.ID
	if chatID == 0 {
		return
	}
	if !h.shouldProcessJoin(chatID, member.ID) {
		return
	}

	go h.CollectUserInfo(&member)

	if h.IsAdmin(member.ID) {
		return
	}
	if h.isChatTelegramAdminUser(&chat, member.ID) {
		return
	}
	if h.whitelistSvc != nil && h.whitelistSvc.IsWhitelisted(member.ID) {
		return
	}

	// 已封禁号再次进群: 走幂等补踢并清理进群提示消息
	if banned, _ := h.banService.IsBanned(member.ID); banned {
		if err := h.banService.BanUser(chatID, member.ID, models.BanTypeManual,
			"已封禁用户再次进群", "", "", 0, joinMsgID, false); err != nil {
			log.Printf("[进群检测] 已封禁用户 %d 补踢失败: %v", member.ID, err)
		}
		return
	}

	// 仅含 From 的消息: CheckAll 在无文本/无频道回复时只执行昵称+简介检测
	// 进群强制刷新简介,避免短时缓存挡住刚改简介的广告号
	probe := &telego.Message{From: &member, Chat: chat}
	violated, banType, reason, err := h.detector.CheckAllFreshBio(probe)
	if err != nil {
		log.Printf("[进群检测] 检测错误 user=%d: %v", member.ID, err)
		return
	}
	if !violated {
		return
	}

	log.Printf("[进群检测] 新成员 %d 违规: 类型=%s, 原因=%s", member.ID, banType, reason)
	if err := h.banService.BanUser(chatID, member.ID, models.BanType(banType),
		reason, reason, "", 0, joinMsgID, false); err != nil {
		log.Printf("[进群检测] 封禁用户 %d 失败: %v", member.ID, err)
		return
	}

	autoReason := reason
	if banType != "" && reason != "" {
		autoReason = fmt.Sprintf("%s命中: %s", banTypeChinese(models.BanType(banType)), reason)
	}
	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &chat,
		UserID:     member.ID,
		UserName:   utils.GetFullName(member.FirstName, member.LastName),
		Username:   member.Username,
		Action:     "拉黑",
		Duration:   "永久",
		Reason:     autoReason,
		OperatorID: 0,
	})
}

// shouldProcessJoin 进群检测去重: 同一群内同一用户在 joinDedupTTL 内只处置一次,
// 避免"加入群组"系统消息与 chat_member 更新对同一次进群重复封禁/广播
func (h *Handler) shouldProcessJoin(chatID, userID int64) bool {
	key := userChatKey{ChatID: chatID, UserID: userID}
	h.recentJoinsMu.Lock()
	defer h.recentJoinsMu.Unlock()
	if last, ok := h.recentJoins[key]; ok && time.Since(last) <= joinDedupTTL {
		return false
	}
	h.recentJoins[key] = time.Now()
	return true
}

// cleanRecentJoins 定期清理进群去重缓存中过期的条目
func (h *Handler) cleanRecentJoins() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-joinDedupTTL)
			h.recentJoinsMu.Lock()
			for key, t := range h.recentJoins {
				if t.Before(cutoff) {
					delete(h.recentJoins, key)
				}
			}
			h.recentJoinsMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}

func (h *Handler) checkUserDuplicateMessage(message *telego.Message) bool {
	if message == nil || message.From == nil {
		return false
	}
	if h.IsAdmin(message.From.ID) {
		return false
	}

	text := h.extractMessageText(message)
	if text == "" {
		return false
	}
	if h.isDuplicateWhitelistMessage(text) {
		return false
	}

	// 由 /config 控制的检测开关; 全部关闭则直接跳过避免维护无意义状态
	spamEnabled, waterEnabled := h.duplicateDetectionFlags()
	if !spamEnabled && !waterEnabled {
		return false
	}

	now := time.Now()
	triggerType, triggeredMessages := h.recordUserDuplicateMessage(message, text, now, spamEnabled, waterEnabled)
	if triggerType == "" || len(triggeredMessages) == 0 {
		return false
	}

	reason := "触发刷屏限制"
	if triggerType == "water" {
		reason = "触发水群检测"
	}

	for _, msg := range triggeredMessages {
		if err := h.banService.DeleteMessage(message.Chat.ID, msg.MessageID); err != nil {
			log.Printf("[重复消息检测] 删除消息失败: chat=%d message=%d err=%v", message.Chat.ID, msg.MessageID, err)
		}
	}

	if err := h.bot.RestrictChatMember(context.Background(), &telego.RestrictChatMemberParams{
		ChatID:      tu.ID(message.Chat.ID),
		UserID:      message.From.ID,
		Permissions: lockedPermissions(),
	}); err != nil {
		log.Printf("[重复消息检测] 永久禁言失败: chat=%d user=%d err=%v", message.Chat.ID, message.From.ID, err)
		return true
	}

	go func(chatID, userID int64, reason string) {
		if err := h.banService.RecordMute(chatID, userID, 0, reason, 0); err != nil {
			log.Printf("[重复消息检测] 记录永久禁言失败: chat=%d user=%d err=%v", chatID, userID, err)
		}
	}(message.Chat.ID, message.From.ID, reason)

	notice := fmt.Sprintf("%s %s，有问题联系管理员。\n管理员回复本消息发送 /un 可直接解除。",
		h.userMentionHTML(message.From), reason)
	sent, err := h.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:    tu.ID(message.Chat.ID),
		Text:      notice,
		ParseMode: "HTML",
	})
	if err != nil {
		log.Printf("[重复消息检测] 发送群提醒失败: chat=%d user=%d err=%v", message.Chat.ID, message.From.ID, err)
	} else if sent != nil {
		h.rememberModerationNotice(sent.Chat.ID, sent.MessageID, message.From.ID)
		if h.notifySvc != nil {
			h.notifySvc.MaybeScheduleWorkGroupDelete(sent.Chat.ID, sent.MessageID)
		}
	}

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &message.Chat,
		UserID:     message.From.ID,
		UserName:   utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:   message.From.Username,
		Action:     "禁言",
		Duration:   "永久",
		Reason:     reason,
		OperatorID: 0,
	})

	log.Printf("[重复消息检测] 用户 %d %s, 已清理 %d 条消息并永久禁言", message.From.ID, reason, len(triggeredMessages))
	return true
}

func (h *Handler) isDuplicateWhitelistMessage(text string) bool {
	if h.cache == nil {
		return false
	}
	return h.cache.MatchesDuplicateWhitelist(text)
}

// normalizeDuplicateWhitelistText 把待匹配文本与白名单关键词都做"空白无关 + 大小写无关"归一化。
//
// 背景: 管理员通常以紧凑形式录入白名单(如"双组2"),而真人发言常带空格("双组 2"/"双 组2"),
// 直接 strings.Contains 会漏过合法消息,导致刷屏/水群检测误伤。
//
// 安全性: 白名单只作用于刷屏/水群检测,不会让广告号绕过关键词(name/bio/msg)检测;
// 因此即使广告号用空格切字符匹配上白名单跳过刷屏检测,关键词检测仍能拦截广告内容。
//
// 实现: utils.NormalizeLoose 过滤全部空白并转小写。
func normalizeDuplicateWhitelistText(s string) string {
	return utils.NormalizeLoose(s)
}

func (h *Handler) recordUserDuplicateMessage(message *telego.Message, text string, now time.Time, spamEnabled, waterEnabled bool) (string, []userDuplicateMessage) {
	spamCutoff := now.Add(-userSpamWindow)
	historyCutoff := now.Add(-userMessageHistoryTTL)
	chatKey := userChatKey{
		ChatID: message.Chat.ID,
		UserID: message.From.ID,
	}
	current := userDuplicateMessage{
		MessageID: message.MessageID,
		Text:      text,
		SentAt:    now,
	}

	h.userDuplicateMu.Lock()
	defer h.userDuplicateMu.Unlock()

	history := h.userRecentMessages[chatKey]
	filteredHistory := history[:0]
	for _, msg := range history {
		if msg.SentAt.After(historyCutoff) {
			filteredHistory = append(filteredHistory, msg)
		}
	}
	filteredHistory = append(filteredHistory, current)
	if len(filteredHistory) > userMessageHistoryMax {
		filteredHistory = filteredHistory[len(filteredHistory)-userMessageHistoryMax:]
	}

	windowStart := 0
	for windowStart < len(filteredHistory) && filteredHistory[windowStart].SentAt.Before(spamCutoff) {
		windowStart++
	}
	spamWindowMessages := filteredHistory[windowStart:]

	var spamTriggered []userDuplicateMessage
	sameTextCount := 0
	for i := range spamWindowMessages {
		if spamWindowMessages[i].Text != text {
			continue
		}
		sameTextCount++
	}
	if spamEnabled && sameTextCount >= userSpamThreshold {
		for i := range filteredHistory {
			if filteredHistory[i].Handled {
				continue
			}
			spamTriggered = append(spamTriggered, filteredHistory[i])
			filteredHistory[i].Handled = true
		}
	}

	state := h.userConsecutiveState[chatKey]
	if state.Text != text {
		state = userConsecutiveMessages{Text: text}
	}
	state.Messages = append(state.Messages, current)

	var waterTriggered []userDuplicateMessage
	if waterEnabled && len(state.Messages) >= userWaterThreshold {
		for i := range state.Messages {
			state.Messages[i].Handled = true
		}
		for i := range filteredHistory {
			if filteredHistory[i].Handled {
				continue
			}
			waterTriggered = append(waterTriggered, filteredHistory[i])
			filteredHistory[i].Handled = true
		}
	}

	if len(spamTriggered) > 0 {
		handledIDs := make(map[int]struct{}, len(spamTriggered))
		for _, msg := range spamTriggered {
			handledIDs[msg.MessageID] = struct{}{}
		}
		for i := range state.Messages {
			if _, exists := handledIDs[state.Messages[i].MessageID]; exists {
				state.Messages[i].Handled = true
			}
		}
	}
	if len(waterTriggered) > 0 {
		handledIDs := make(map[int]struct{}, len(waterTriggered))
		for _, msg := range waterTriggered {
			handledIDs[msg.MessageID] = struct{}{}
		}
		for i := range filteredHistory {
			if _, exists := handledIDs[filteredHistory[i].MessageID]; exists {
				filteredHistory[i].Handled = true
			}
		}
	}

	h.userRecentMessages[chatKey] = filteredHistory
	h.userConsecutiveState[chatKey] = state

	if len(spamTriggered) > 0 {
		return "spam", spamTriggered
	}
	if len(waterTriggered) > 0 {
		return "water", waterTriggered
	}
	return "", nil
}

func (h *Handler) userMentionHTML(user *telego.User) string {
	if user == nil {
		return "该用户"
	}
	name := strings.TrimSpace(utils.GetFullName(user.FirstName, user.LastName))
	if name == "" && user.Username != "" {
		name = "@" + user.Username
	}
	if name == "" {
		name = fmt.Sprintf("%d", user.ID)
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, user.ID, htmlEscape(name))
}

const moderationNoticeTTL = 7 * 24 * time.Hour

func (h *Handler) rememberModerationNotice(chatID int64, messageID int, userID int64) {
	if h == nil || chatID == 0 || messageID <= 0 || userID <= 0 {
		return
	}
	h.moderationNoticesMu.Lock()
	h.moderationNotices[noticeKey{ChatID: chatID, MessageID: messageID}] = moderationNotice{
		UserID:    userID,
		CreatedAt: time.Now(),
	}
	h.moderationNoticesMu.Unlock()
}

func (h *Handler) lookupModerationNotice(chatID int64, messageID int) (int64, bool) {
	if h == nil || chatID == 0 || messageID <= 0 {
		return 0, false
	}
	h.moderationNoticesMu.Lock()
	defer h.moderationNoticesMu.Unlock()
	entry, ok := h.moderationNotices[noticeKey{ChatID: chatID, MessageID: messageID}]
	if !ok {
		return 0, false
	}
	if time.Since(entry.CreatedAt) > moderationNoticeTTL {
		delete(h.moderationNotices, noticeKey{ChatID: chatID, MessageID: messageID})
		return 0, false
	}
	return entry.UserID, true
}

func (h *Handler) cleanModerationNotices() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-moderationNoticeTTL)
			h.moderationNoticesMu.Lock()
			for key, entry := range h.moderationNotices {
				if entry.CreatedAt.Before(cutoff) {
					delete(h.moderationNotices, key)
				}
			}
			h.moderationNoticesMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}

func (h *Handler) checkRepeatedAd(message *telego.Message) bool {
	if message == nil || message.From == nil {
		return false
	}
	if h.IsAdmin(message.From.ID) {
		return false
	}

	text := h.extractMessageText(message)
	if text == "" || !strings.Contains(text, "@") {
		return false
	}

	now := time.Now()
	cutoff := now.Add(-repeatedAdWindow)
	key := repeatedAdKey{
		ChatID: message.Chat.ID,
		Text:   text,
	}

	h.repeatedAdMu.Lock()
	messages := h.repeatedAdMessages[key]
	filtered := messages[:0]
	for _, msg := range messages {
		if msg.SentAt.After(cutoff) {
			filtered = append(filtered, msg)
		}
	}
	filtered = append(filtered, repeatedAdMessage{
		UserID:    message.From.ID,
		MessageID: message.MessageID,
		SentAt:    now,
	})
	shouldMute := len(filtered) >= repeatedAdThreshold
	triggeredMessages := make([]repeatedAdMessage, 0, len(filtered))
	if shouldMute {
		for i := range filtered {
			if filtered[i].Handled {
				continue
			}
			triggeredMessages = append(triggeredMessages, filtered[i])
			filtered[i].Handled = true
		}
	}
	h.repeatedAdMessages[key] = filtered
	h.repeatedAdMu.Unlock()

	if !shouldMute {
		return false
	}
	if len(triggeredMessages) == 0 {
		return true
	}

	reason := fmt.Sprintf("%d秒内重复发送%d条相同含@协议广告消息", int(repeatedAdWindow.Seconds()), repeatedAdThreshold)
	mutedUsers := make(map[int64]struct{}, len(triggeredMessages))
	for _, msg := range triggeredMessages {
		if err := h.banService.DeleteMessage(message.Chat.ID, msg.MessageID); err != nil {
			log.Printf("[广告检测] 删除广告消息失败: chat=%d message=%d err=%v", message.Chat.ID, msg.MessageID, err)
		}
		if _, exists := mutedUsers[msg.UserID]; exists {
			continue
		}
		mutedUsers[msg.UserID] = struct{}{}

		if err := h.bot.RestrictChatMember(context.Background(), &telego.RestrictChatMemberParams{
			ChatID:      tu.ID(message.Chat.ID),
			UserID:      msg.UserID,
			Permissions: lockedPermissions(),
		}); err != nil {
			log.Printf("[广告检测] 永久禁言失败: chat=%d user=%d err=%v", message.Chat.ID, msg.UserID, err)
			continue
		}

		go func(chatID, userID int64) {
			if err := h.banService.RecordMute(chatID, userID, 0, reason, 0); err != nil {
				log.Printf("[广告检测] 记录永久禁言失败: chat=%d user=%d err=%v", chatID, userID, err)
			}
		}(message.Chat.ID, msg.UserID)

		h.broadcastSvc.SendBanAsync(service.BanBroadcast{
			Chat:       &message.Chat,
			UserID:     msg.UserID,
			UserName:   "",
			Action:     "禁言",
			Duration:   "永久",
			Reason:     reason,
			OperatorID: 0,
		})
	}

	log.Printf("[广告检测] 群 %d 出现重复协议广告,已清理 %d 条消息并永久禁言 %d 个用户", message.Chat.ID, len(triggeredMessages), len(mutedUsers))
	return true
}

func (h *Handler) cleanRepeatedAdMessages() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-repeatedAdWindow)

			h.repeatedAdMu.Lock()
			for key, messages := range h.repeatedAdMessages {
				filtered := messages[:0]
				for _, msg := range messages {
					if msg.SentAt.After(cutoff) {
						filtered = append(filtered, msg)
					}
				}
				if len(filtered) == 0 {
					delete(h.repeatedAdMessages, key)
					continue
				}
				h.repeatedAdMessages[key] = filtered
			}
			h.repeatedAdMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}

func (h *Handler) cleanUserDuplicateMessages() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-userMessageHistoryTTL)

			h.userDuplicateMu.Lock()
			for key, messages := range h.userRecentMessages {
				filtered := messages[:0]
				for _, msg := range messages {
					if msg.SentAt.After(cutoff) {
						filtered = append(filtered, msg)
					}
				}
				if len(filtered) == 0 {
					delete(h.userRecentMessages, key)
					delete(h.userConsecutiveState, key)
					continue
				}
				h.userRecentMessages[key] = filtered
			}
			// 清理 userConsecutiveState 中对应 key 已不在 userRecentMessages 的条目
			for key := range h.userConsecutiveState {
				if _, ok := h.userRecentMessages[key]; !ok {
					delete(h.userConsecutiveState, key)
				}
			}
			h.userDuplicateMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}

// handleSpoilerMessage 检测"遮掩格式"(Telegram spoiler 实体)消息
//
// 背景:
//   - 广告号常用 spoiler(||...||) 把诱骗链接/钱包/邀请码遮住,诱导真实成员点击展开
//   - 普通群聊几乎不存在 spoiler 的合法用法,出现即视为高危
//
// 处置:
//  1. 单点 GetChatMember 复核当前用户在群里的角色,creator/administrator 直接放行;
//     同步把其 ID 写入 chatAdmins 缓存,避免下一条 spoiler 再次绕过入口白名单
//  2. 先踢人(BanChatMember+UnbanChatMember),踢成功才删消息——避免误删管理员消息
//  3. 不写 ban_record,允许真人误操作后被重新邀请回群
//  4. 踢人返回 "user is an administrator" 时主动加缓存自愈(防御兜底)
//
// 调用前提:
//   - HandleMessage 入口已过滤 guest bot / TG 群管理员白名单 / 已封禁用户
//   - 这里再补一层 bot 内部 admin(IsAdmin) + 单点 GetChatMember 兜底
//
// 返回 true 表示已识别并处理完毕,调用方应直接返回
func (h *Handler) handleSpoilerMessage(message *telego.Message) bool {
	if message == nil || message.From == nil {
		return false
	}
	if !hasSpoilerContent(message) {
		return false
	}
	if h.IsAdmin(message.From.ID) {
		return false
	}

	chatID := message.Chat.ID
	userID := message.From.ID

	// 入口白名单可能因 GetChatAdministrators 拉取失败或缓存 miss 而漏过管理员;
	// 这里再做一次"单点用户查询",对管理员/群主一概放行并补缓存
	if h.isUserChatAdminViaAPI(chatID, userID) {
		h.cache.AddChatAdmin(chatID, userID)
		log.Printf("[遮掩格式] 单点复核命中管理员,跳过 chat=%d user=%d", chatID, userID)
		return true
	}

	// 先踢后删: 踢成功才动消息,避免管理员被误删 spoiler 内容
	if err := h.kickUserFromChat(chatID, userID, "遮掩格式"); err != nil {
		if isUserAdminAPIError(err) {
			// Telegram 直接告诉我们目标是管理员,缓存马上自愈,防止后续 spoiler 重复触发
			h.cache.AddChatAdmin(chatID, userID)
			log.Printf("[遮掩格式] 踢出时识别为群管理员,加入缓存 chat=%d user=%d", chatID, userID)
			return true
		}
		log.Printf("[遮掩格式] 踢出失败 chat=%d user=%d err=%v", chatID, userID, err)
		return true
	}

	if err := h.banService.DeleteMessage(chatID, message.MessageID); err != nil {
		log.Printf("[遮掩格式] 删除消息失败 chat=%d message=%d user=%d err=%v",
			chatID, message.MessageID, userID, err)
	}

	log.Printf("[遮掩格式] 已踢出发送 spoiler 消息的用户 chat=%d user=%d", chatID, userID)

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &message.Chat,
		UserID:     userID,
		UserName:   utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:   message.From.Username,
		Action:     "踢出",
		Duration:   "踢出",
		Reason:     "发送遮掩格式(spoiler)消息",
		OperatorID: 0,
	})
	return true
}

// isUserChatAdminViaAPI 单点调用 getChatMember 判断 user 在当前 chat 的角色
// 仅用于 spoiler 等"罕见但要求高准确度"的处置路径,不放进 hot path
func (h *Handler) isUserChatAdminViaAPI(chatID, userID int64) bool {
	member, err := h.bot.GetChatMember(context.Background(), &telego.GetChatMemberParams{
		ChatID: tu.ID(chatID),
		UserID: userID,
	})
	if err != nil {
		log.Printf("[白名单] 单点 GetChatMember 失败 chat=%d user=%d err=%v", chatID, userID, err)
		return false
	}
	if member == nil {
		return false
	}
	status := member.MemberStatus()
	return status == telego.MemberStatusCreator || status == telego.MemberStatusAdministrator
}

// isUserAdminAPIError 识别 BanChatMember 返回的"目标是管理员"错误,用于自愈缓存判断
func isUserAdminAPIError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "user is an administrator")
}

// hasSpoilerContent 检查媒体遮罩或文字/caption spoiler 实体
// 官方 Message.has_media_spoiler 覆盖「整图/视频打码」类广告,仅扫 entity 会漏
func hasSpoilerContent(message *telego.Message) bool {
	if message == nil {
		return false
	}
	if message.HasMediaSpoiler {
		return true
	}
	for _, entity := range message.Entities {
		if entity.Type == telego.EntityTypeSpoiler {
			return true
		}
	}
	for _, entity := range message.CaptionEntities {
		if entity.Type == telego.EntityTypeSpoiler {
			return true
		}
	}
	return false
}

// handleGuestBotMessage 拦截 Bot API 10.0 Guest Bot 召唤模式发出的消息
//
// 背景:
//   - 广告号可以通过 @某guest_bot 召唤一个未入群的 bot 直接发广告,旧封禁链路对这种消息无能为力
//   - Telegram 在消息上标记 guest_bot_caller_user / guest_bot_caller_chat,任何看到此消息的端都能识别
//
// 处置:
//  1. 删除该 guest bot 消息
//  2. 召唤者(GuestBotCallerUser)走 TG 群管白名单:管理员误调直接放行;否则只踢不封禁
//     (踢出 = BanChatMember + UnbanChatMember,允许召唤者后续重新进群,但失去本次召唤痕迹)
//  3. 被召唤的 guest bot 本体永久 BanChatMember(不解封),把 bot 加进群黑名单,
//     防止后续被群成员再次召唤(Telegram 允许预先封禁不在群内的账号)
//  4. 不写 users / ban_records 表,避免把 bot 或一次性事件污染人类用户数据
//  5. caller 仅以 chat 身份(GuestBotCallerChat)出现时,无法用 BanChatMember 处理,仅记录日志
//
// 返回 true 表示本消息已被识别为 guest bot 消息并处理完毕,调用方应直接返回
func (h *Handler) handleGuestBotMessage(message *telego.Message) bool {
	if message == nil {
		return false
	}
	if message.GuestBotCallerUser == nil && message.GuestBotCallerChat == nil {
		return false
	}

	botName := ""
	botID := int64(0)
	if message.From != nil {
		botID = message.From.ID
		if message.From.Username != "" {
			botName = "@" + message.From.Username
		} else {
			botName = strings.TrimSpace(utils.GetFullName(message.From.FirstName, message.From.LastName))
		}
	}
	callerID := int64(0)
	if message.GuestBotCallerUser != nil {
		callerID = message.GuestBotCallerUser.ID
	}
	callerChatID := int64(0)
	if message.GuestBotCallerChat != nil {
		callerChatID = message.GuestBotCallerChat.ID
	}

	if err := h.banService.DeleteMessage(message.Chat.ID, message.MessageID); err != nil {
		log.Printf("[GuestBot] 删除消息失败 chat=%d message=%d bot=%s(%d) caller_user=%d caller_chat=%d err=%v",
			message.Chat.ID, message.MessageID, botName, botID, callerID, callerChatID, err)
	} else {
		log.Printf("[GuestBot] 已删除 guest bot 消息 chat=%d message=%d bot=%s(%d) caller_user=%d caller_chat=%d",
			message.Chat.ID, message.MessageID, botName, botID, callerID, callerChatID)
	}

	// 永久封禁被召唤的 guest bot 本身,阻止后续召唤
	if botID > 0 {
		if err := h.banChatMemberHard(message.Chat.ID, botID, "GuestBot"); err != nil {
			log.Printf("[GuestBot] 封禁 guest bot 失败 chat=%d bot=%s(%d) err=%v",
				message.Chat.ID, botName, botID, err)
		} else {
			log.Printf("[GuestBot] 已永久封禁 guest bot chat=%d bot=%s(%d)",
				message.Chat.ID, botName, botID)
		}
	}

	// 踢召唤者(只踢不封禁),管理员误调放行
	if message.GuestBotCallerUser != nil {
		caller := message.GuestBotCallerUser
		if h.isChatTelegramAdminUser(&message.Chat, caller.ID) {
			log.Printf("[GuestBot] 召唤者是群管理员,跳过踢出 chat=%d caller=%d", message.Chat.ID, caller.ID)
		} else {
			if err := h.kickUserFromChat(message.Chat.ID, caller.ID, "GuestBot"); err != nil {
				log.Printf("[GuestBot] 踢出召唤者失败 chat=%d caller=%d err=%v",
					message.Chat.ID, caller.ID, err)
			} else {
				log.Printf("[GuestBot] 已踢出召唤者 chat=%d caller=%d", message.Chat.ID, caller.ID)
				h.broadcastSvc.SendBanAsync(service.BanBroadcast{
					Chat:       &message.Chat,
					UserID:     caller.ID,
					UserName:   utils.GetFullName(caller.FirstName, caller.LastName),
					Username:   caller.Username,
					Action:     "踢出",
					Duration:   "踢出",
					Reason:     fmt.Sprintf("召唤外部 bot 发消息(bot=%s)", botName),
					OperatorID: 0,
				})
			}
		}
	} else if callerChatID != 0 {
		log.Printf("[GuestBot] 召唤者以频道身份(chat=%d)发起,跳过踢出 host_chat=%d",
			callerChatID, message.Chat.ID)
	}
	return true
}

// handleContactMessage 检测群内名片并永久拉黑
//
// 背景:
//   - 名片含 Telegram Contact(自己的/别人的/仅电话/带 user_id)以及 .vcf / vCard 文件
//   - 真人群聊几乎不会主动发联系人卡片,广告号常用此方式投递推广联系方式绕过文本关键词检测
//
// 处置策略:
//  1. 仅处理群/超级群;调用方已先后过滤 guest bot / TG 群管理员白名单 / spoiler / 已封禁用户;
//     这里再补一层 bot 内部 admin(IsAdmin) 兜底,管理员演示/测试时不会被误伤
//  2. 走 BanService.BanUser 主流程: 标记 user.is_banned + 写 ban_records +
//     并发删消息 + 群组拉黑(永久封禁,与昵称/简介/消息关键词命中保持一致的处置)
//  3. 命中即广播到工作群/播报频道,理由附带名片携带的电话号码/目标 user_id 便于人工复核
//
// 返回 true 表示已识别并处理完毕,调用方应直接返回
func (h *Handler) handleContactMessage(message *telego.Message) bool {
	if message == nil || message.From == nil {
		return false
	}
	if message.Chat.Type != telego.ChatTypeSupergroup && message.Chat.Type != telego.ChatTypeGroup {
		return false
	}
	if !isContactCardMessage(message) {
		return false
	}
	if h.IsAdmin(message.From.ID) {
		return false
	}

	contactDesc := contactCardDescription(message)
	reason := "发送用户名片"
	keyword := contactDesc

	log.Printf("[用户名片] 命中: chat=%d user=%d contact=%s",
		message.Chat.ID, message.From.ID, contactDesc)

	if err := h.banService.BanUser(
		message.Chat.ID,
		message.From.ID,
		models.BanTypeContact,
		reason,
		keyword,
		contactDesc,
		0,
		message.MessageID,
		false,
	); err != nil {
		log.Printf("[用户名片] 封禁失败 chat=%d user=%d err=%v",
			message.Chat.ID, message.From.ID, err)
		return true
	}

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &message.Chat,
		UserID:     message.From.ID,
		UserName:   utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:   message.From.Username,
		Action:     "拉黑",
		Duration:   "永久",
		Reason:     fmt.Sprintf("%s: %s", reason, contactDesc),
		OperatorID: 0,
	})
	return true
}

func isContactCardMessage(message *telego.Message) bool {
	if message == nil {
		return false
	}
	if message.Contact != nil {
		return true
	}
	return isVCardDocument(message.Document)
}

func isVCardDocument(doc *telego.Document) bool {
	if doc == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(doc.MimeType)) {
	case "text/vcard", "text/x-vcard":
		return true
	}
	switch strings.ToLower(filepath.Ext(doc.FileName)) {
	case ".vcf", ".vcard":
		return true
	}
	return false
}

func contactCardDescription(message *telego.Message) string {
	if message == nil {
		return "未知名片"
	}
	if contact := message.Contact; contact != nil {
		contactDesc := strings.TrimSpace(utils.GetFullName(contact.FirstName, contact.LastName))
		if contact.PhoneNumber != "" {
			if contactDesc != "" {
				contactDesc += " "
			}
			contactDesc += contact.PhoneNumber
		}
		if contact.UserID != 0 {
			if contactDesc != "" {
				contactDesc += " "
			}
			contactDesc += fmt.Sprintf("(uid=%d)", contact.UserID)
		}
		if contactDesc == "" {
			return "未知名片"
		}
		return contactDesc
	}
	if doc := message.Document; doc != nil {
		if name := strings.TrimSpace(doc.FileName); name != "" {
			return name
		}
		return "vCard 文件"
	}
	return "未知名片"
}

// handleNonAdminBotMessage 拦截非管理员 Bot 在群内发送的消息
//
// 背景:
//   - 广告号可以把非管理员 Bot 拉进群里,或通过各种方式让 Bot 在群里发言
//   - 正常群组中,Bot 如果需要在群里发言,通常会被设为管理员
//   - 非管理员 Bot 在群里发言,极大概率是广告/骚扰行为
//
// 调用前提:
//   - HandleMessage 入口已过滤 guest bot(由 handleGuestBotMessage 处理)
//   - isChatTelegramAdminMessage 已放行群管理员(含 Bot 管理员)
//   - 此处 message.From.IsBot==true 则一定是非管理员 Bot
//
// 处置(由 bot_settings.non_admin_bot_action 控制):
//   - "" (空/默认): 不处理
//   - "delete": 仅删除消息
//   - "ban": 删除消息 + 永久拉黑(BanChatMember,不解封)
//
// 返回 true 表示已识别并处理完毕,调用方应直接返回
func (h *Handler) handleNonAdminBotMessage(message *telego.Message) bool {
	if message == nil || message.From == nil || !message.From.IsBot {
		return false
	}
	if message.Chat.Type != telego.ChatTypeSupergroup && message.Chat.Type != telego.ChatTypeGroup {
		return false
	}

	action := h.nonAdminBotAction()
	if action == models.NonAdminBotActionNone {
		return false
	}

	chatID := message.Chat.ID
	botID := message.From.ID
	botName := message.From.Username
	if botName == "" {
		botName = strings.TrimSpace(utils.GetFullName(message.From.FirstName, message.From.LastName))
	}

	if err := h.banService.DeleteMessage(chatID, message.MessageID); err != nil {
		log.Printf("[非管理员Bot] 删除消息失败 chat=%d message=%d bot=@%s(%d) err=%v",
			chatID, message.MessageID, botName, botID, err)
	}

	broadcastAction := "删除消息"

	if action == models.NonAdminBotActionBan {
		if err := h.banChatMemberHard(chatID, botID, "非管理员Bot"); err != nil {
			log.Printf("[非管理员Bot] 拉黑失败 chat=%d bot=@%s(%d) err=%v",
				chatID, botName, botID, err)
		} else {
			log.Printf("[非管理员Bot] 已删除消息并拉黑 chat=%d bot=@%s(%d)",
				chatID, botName, botID)
		}
		broadcastAction = "拉黑"
	} else {
		log.Printf("[非管理员Bot] 已删除消息 chat=%d bot=@%s(%d)",
			chatID, botName, botID)
	}

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &message.Chat,
		UserID:     botID,
		UserName:   utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:   message.From.Username,
		Action:     broadcastAction,
		Duration:   broadcastAction,
		Reason:     "非管理员Bot在群内发言",
		OperatorID: 0,
	})
	return true
}

// nonAdminBotAction 读取非管理员 Bot 处置动作配置
func (h *Handler) nonAdminBotAction() models.NonAdminBotAction {
	if h.settingsRepo == nil {
		return models.NonAdminBotActionNone
	}
	cfg, err := h.settingsRepo.Get()
	if err != nil {
		log.Printf("[处理器] 读取非管理员Bot检测配置失败: %v", err)
		return models.NonAdminBotActionNone
	}
	return cfg.NonAdminBotAction
}

// checkForwardBurst 同一用户在短时间内连续转发满 4 条时永久禁言。
//
// 多选转发会几乎同时出现。按消息发送时间聚成 2 秒窗口，满 4 条就删掉这些转发并禁言。
// 编辑、频道同步到讨论组的自动转发不计数。本 Bot 的检测开关关闭时不计数。管理员与白名单在调用前已跳过。
// 返回 true 表示本条已纳入一次处罚，调用方不再继续检测。
func (h *Handler) checkForwardBurst(message *telego.Message) bool {
	if message == nil || message.From == nil || message.ForwardOrigin == nil {
		return false
	}
	if !h.forwardBurstEnabled() {
		return false
	}
	if message.EditDate != 0 || message.IsAutomaticForward {
		return false
	}
	if message.Chat.Type != telego.ChatTypeSupergroup && message.Chat.Type != telego.ChatTypeGroup {
		return false
	}
	if h.IsAdmin(message.From.ID) {
		return false
	}

	sentAt := time.Unix(message.Date, 0)
	if message.Date <= 0 {
		sentAt = time.Now()
	}
	key := userChatKey{ChatID: message.Chat.ID, UserID: message.From.ID}
	current := forwardBurstHit{MessageID: message.MessageID, SentAt: sentAt}

	h.forwardBurstMu.Lock()
	next, triggered, notify := nextForwardBurst(h.forwardBursts[key], current)
	if len(next) == 0 {
		delete(h.forwardBursts, key)
	} else {
		h.forwardBursts[key] = next
	}
	h.forwardBurstMu.Unlock()

	if len(triggered) == 0 {
		return false
	}

	for _, msg := range triggered {
		if err := h.banService.DeleteMessage(message.Chat.ID, msg.MessageID); err != nil {
			log.Printf("[并发转发] 删除消息失败: chat=%d message=%d err=%v", message.Chat.ID, msg.MessageID, err)
		}
	}
	if !notify {
		return true
	}

	reason := "触发并发转发"

	if err := h.bot.RestrictChatMember(context.Background(), &telego.RestrictChatMemberParams{
		ChatID:      tu.ID(message.Chat.ID),
		UserID:      message.From.ID,
		Permissions: lockedPermissions(),
	}); err != nil {
		log.Printf("[并发转发] 永久禁言失败: chat=%d user=%d err=%v", message.Chat.ID, message.From.ID, err)
		return true
	}

	go func(chatID, userID int64, reason string) {
		if err := h.banService.RecordMute(chatID, userID, 0, reason, 0); err != nil {
			log.Printf("[并发转发] 记录永久禁言失败: chat=%d user=%d err=%v", chatID, userID, err)
		}
	}(message.Chat.ID, message.From.ID, reason)

	notice := fmt.Sprintf("%s %s，有问题联系管理员。\n管理员回复本消息发送 /un 可直接解除。",
		h.userMentionHTML(message.From), reason)
	sent, err := h.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:    tu.ID(message.Chat.ID),
		Text:      notice,
		ParseMode: "HTML",
	})
	if err != nil {
		log.Printf("[并发转发] 发送群提醒失败: chat=%d user=%d err=%v", message.Chat.ID, message.From.ID, err)
	} else if sent != nil {
		h.rememberModerationNotice(sent.Chat.ID, sent.MessageID, message.From.ID)
		if h.notifySvc != nil {
			h.notifySvc.MaybeScheduleWorkGroupDelete(sent.Chat.ID, sent.MessageID)
		}
	}

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &message.Chat,
		UserID:     message.From.ID,
		UserName:   utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:   message.From.Username,
		Action:     "禁言",
		Duration:   "永久",
		Reason:     reason,
		OperatorID: 0,
	})

	log.Printf("[并发转发] 用户 %d 在群 %d %s, 已清理 %d 条并永久禁言", message.From.ID, message.Chat.ID, reason, len(triggered))
	return true
}

// forwardBurstEnabled 读本 Bot 的并发转发开关。读失败按开启处理，避免配置短暂读不到时漏掉爆发转发。
func (h *Handler) forwardBurstEnabled() bool {
	if h.settingsRepo == nil {
		return true
	}
	cfg, err := h.settingsRepo.Get()
	if err != nil || cfg == nil {
		if err != nil {
			log.Printf("[并发转发] 读取开关失败,按开启处理: %v", err)
		}
		return true
	}
	return !cfg.ForwardBurstDisabled
}

// nextForwardBurst 把一条转发并入窗口。窗口右端取这批里最晚的发送时间，避免乱序把同一次转发拆开。
// 同一 messageID 只计一次。满阈值时返回尚未删除的消息；notify 只在第一次跨过阈值时为 true。
func nextForwardBurst(history []forwardBurstHit, current forwardBurstHit) (next []forwardBurstHit, triggered []forwardBurstHit, notify bool) {
	for _, msg := range history {
		if msg.MessageID == current.MessageID {
			return history, nil, false
		}
	}

	combined := make([]forwardBurstHit, 0, len(history)+1)
	combined = append(combined, history...)
	combined = append(combined, current)

	latest := current.SentAt
	for _, msg := range combined {
		if msg.SentAt.After(latest) {
			latest = msg.SentAt
		}
	}
	cutoff := latest.Add(-forwardBurstWindow)

	next = make([]forwardBurstHit, 0, len(combined))
	for _, msg := range combined {
		if msg.SentAt.Before(cutoff) {
			continue
		}
		next = append(next, msg)
	}
	if len(next) < forwardBurstThreshold {
		return next, nil, false
	}

	notify = true
	for _, msg := range next {
		if msg.Handled {
			notify = false
			break
		}
	}
	triggered = make([]forwardBurstHit, 0, len(next))
	for i := range next {
		if next[i].Handled {
			continue
		}
		next[i].Handled = true
		triggered = append(triggered, next[i])
	}
	return next, triggered, notify
}

func (h *Handler) cleanForwardBursts() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-forwardBurstWindow)
			h.forwardBurstMu.Lock()
			for key, messages := range h.forwardBursts {
				filtered := messages[:0]
				for _, msg := range messages {
					if !msg.SentAt.Before(cutoff) {
						filtered = append(filtered, msg)
					}
				}
				if len(filtered) == 0 {
					delete(h.forwardBursts, key)
					continue
				}
				h.forwardBursts[key] = filtered
			}
			h.forwardBurstMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}

// handleForwardSourceMessage 检测转发消息的来源是否命中屏蔽关键词
//
// 背景:
//   - 广告号常转发来自广告频道/Bot/账号的消息到群内推广
//   - 管理员通过 /z <关键词> 添加转发来源屏蔽词
//   - 仅对转发消息生效: 用户自身名称/用户名匹配但直接发送的消息不受影响
//
// 匹配规则:
//   - 提取转发来源(ForwardOrigin)的名称和用户名
//   - 对 forward 类型关键词做大小写不敏感的子串匹配
//   - 覆盖全部来源类型: 频道/群组/用户/隐藏用户/Bot
//
// 处置: 删除消息 + 永久封禁转发者(message.From)
//
// 返回 true 表示已识别并处理完毕,调用方应直接返回
func (h *Handler) handleForwardSourceMessage(message *telego.Message) bool {
	if message == nil || message.From == nil || message.ForwardOrigin == nil {
		return false
	}
	if message.Chat.Type != telego.ChatTypeSupergroup && message.Chat.Type != telego.ChatTypeGroup {
		return false
	}

	sourceName, sourceUsername := extractForwardSourceInfo(message.ForwardOrigin)
	if sourceName == "" && sourceUsername == "" {
		return false
	}

	var matchedKeyword string
	if h.cache != nil {
		if matched, kw := h.cache.MatchKeyword(string(models.KeywordTypeForward), sourceName); matched {
			matchedKeyword = kw
		} else if matched, kw := h.cache.MatchKeyword(string(models.KeywordTypeForward), sourceUsername); matched {
			matchedKeyword = kw
		}
	}
	if matchedKeyword == "" {
		return false
	}

	sourceDesc := sourceName
	if sourceUsername != "" {
		if sourceDesc != "" {
			sourceDesc += " (@" + sourceUsername + ")"
		} else {
			sourceDesc = "@" + sourceUsername
		}
	}

	reason := fmt.Sprintf("转发来源命中: %s", matchedKeyword)
	messageText := message.Text
	if messageText == "" {
		messageText = message.Caption
	}

	log.Printf("[转发来源屏蔽] 命中: chat=%d user=%d source=%s keyword=%s",
		message.Chat.ID, message.From.ID, sourceDesc, matchedKeyword)

	if err := h.banService.BanUser(
		message.Chat.ID,
		message.From.ID,
		models.BanTypeForward,
		reason,
		matchedKeyword,
		messageText,
		0,
		message.MessageID,
		false,
	); err != nil {
		log.Printf("[转发来源屏蔽] 封禁失败 chat=%d user=%d err=%v",
			message.Chat.ID, message.From.ID, err)
		return true
	}

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:       &message.Chat,
		UserID:     message.From.ID,
		UserName:   utils.GetFullName(message.From.FirstName, message.From.LastName),
		Username:   message.From.Username,
		Action:     "拉黑",
		Duration:   "永久",
		Reason:     fmt.Sprintf("%s (来源: %s)", reason, sourceDesc),
		OperatorID: 0,
	})
	return true
}

// extractForwardSourceInfo 从 ForwardOrigin 提取来源的名称和用户名
func extractForwardSourceInfo(origin telego.MessageOrigin) (name, username string) {
	if origin == nil {
		return "", ""
	}
	switch o := origin.(type) {
	case *telego.MessageOriginChannel:
		return o.Chat.Title, o.Chat.Username
	case *telego.MessageOriginChat:
		return o.SenderChat.Title, o.SenderChat.Username
	case *telego.MessageOriginUser:
		return strings.TrimSpace(utils.GetFullName(o.SenderUser.FirstName, o.SenderUser.LastName)), o.SenderUser.Username
	case *telego.MessageOriginHiddenUser:
		return o.SenderUserName, ""
	}
	return "", ""
}
