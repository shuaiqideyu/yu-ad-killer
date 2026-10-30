package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/service"
	"telegram-ban-bot/pkg/utils"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// HandleCommand 处理命令
func (h *Handler) HandleCommand(message *telego.Message) {
	// 已注册的 / 命令不会继续进入 AnyMessage，这里负责 1 秒后清理命令消息。
	// 验证: go test ./... (2026-04-25)。
	h.ScheduleCommandCleanup(message)

	if message.From == nil {
		return
	}

	// 群里只处理机器人管理员的命令。普通成员发 /start 等不再查状态、也不回消息。
	if h.requireGroup(message) && !h.IsAdmin(message.From.ID) {
		return
	}

	command := message.Text
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return
	}

	cmd := parts[0]
	args := parts[1:]

	// 兼容 "/cmd@botname" 写法:去掉 @botname 后缀
	if idx := strings.Index(cmd, "@"); idx != -1 {
		cmd = cmd[:idx]
	}
	// 统一带不带斜杠两种形式
	cmd = strings.TrimPrefix(cmd, "/")
	cmd = strings.ToLower(cmd)

	switch cmd {
	case "start":
		h.handleStart(message)
	case "f":
		h.handleBanCommand(message, args)
	case "t":
		h.handleKickCommand(message, args)
	case "jy":
		h.handleJyCommand(message, args)
	case "unjy":
		h.handleUnjyCommand(message, args)
	case "un":
		h.handleUnCommand(message, args)
	case "lh":
		h.handleLhCommand(message, args)
	case "unlh":
		h.handleUnlhCommand(message, args)
	case "name":
		h.handleNameCommand(message, args)
	case "bio":
		h.handleBioCommand(message, args)
	case "msg":
		h.handleMsgCommand(message, args)
	case "no":
		h.handleDuplicateWhitelistCommand(message, args)
	case "z":
		h.handleForwardKeywordCommand(message, args)
	case "q":
		h.handleInviteKeywordCommand(message, args)
	case "unname", "unbio", "unmsg", "unq", "unz", "unno":
		h.handleRemoveKeywordCommand(message, args, strings.ToLower(cmd))
	case "admin":
		h.handleAdminPanel(message)
	case "reload":
		h.handleReloadCache(message)
	case "add":
		h.handleAddBotCommand(message)
	case "cancel":
		h.handleCancelCommand(message)
	case "加白":
		h.handleWhitelistAdd(message, args)
	case "移白":
		h.handleWhitelistRemove(message, args)
	case "白名单":
		h.handleWhitelistList(message)
	}
}

// requireWorkGroup 确认当前消息发生在已绑定的工作群; 未绑定/不是绑定群时静默返回 false
// 已绑定但当前不是该群: 给出友好提示, 引导到正确的群
func (h *Handler) requireWorkGroup(message *telego.Message) bool {
	bound := h.WorkGroupID()
	if bound == 0 {
		return false
	}
	if message.Chat.ID == bound {
		return true
	}
	if message.Chat.Type == telego.ChatTypePrivate {
		h.notifySvc.SendMessage(message.Chat.ID, "此命令需要在工作群组中使用")
	}
	return false
}

// targetUser 解析后的操作目标
type targetUser struct {
	ID          int64
	Username    string
	FirstName   string
	DisplayName string // 已 HTML 转义,直接用于 ParseMode=HTML 反馈
	Source      string // reply / id / @db / @api
}

// requireGroup 校验是否处于群组(非群组直接静默返回)
func (h *Handler) requireGroup(message *telego.Message) bool {
	return message.Chat.Type == telego.ChatTypeGroup || message.Chat.Type == telego.ChatTypeSupergroup
}

// resolveTarget 解析操作目标:回复消息 > 数字ID > @用户名(本地→getChat 兜底)
// 返回 (target, 剩余参数, error)
func (h *Handler) resolveTarget(message *telego.Message, args []string) (*targetUser, []string, error) {
	if message.ReplyToMessage != nil && message.ReplyToMessage.From != nil {
		u := message.ReplyToMessage.From
		return &targetUser{
			ID:          u.ID,
			Username:    u.Username,
			FirstName:   u.FirstName,
			DisplayName: formatUserDisplay(u.ID, u.Username, u.FirstName),
			Source:      "reply",
		}, args, nil
	}

	if len(args) == 0 {
		return nil, nil, fmt.Errorf("请回复目标消息,或提供 @用户名 / 数字ID")
	}

	arg := strings.TrimSpace(args[0])
	rest := args[1:]
	if arg == "" {
		return nil, nil, fmt.Errorf("目标参数为空")
	}

	if id, parseErr := strconv.ParseInt(arg, 10, 64); parseErr == nil && id > 0 {
		var uname, fname string
		if u, _ := h.userRepo.GetByID(id); u != nil {
			uname = u.Username
			fname = u.FirstName
		}
		return &targetUser{
			ID:          id,
			Username:    uname,
			FirstName:   fname,
			DisplayName: formatUserDisplay(id, uname, fname),
			Source:      "id",
		}, rest, nil
	}

	uname := strings.TrimPrefix(arg, "@")
	if uname == "" {
		return nil, nil, fmt.Errorf("无效的 @用户名: %s", arg)
	}

	if u, err := h.userRepo.GetByUsername(uname); err == nil && u != nil {
		return &targetUser{
			ID:          u.ID,
			Username:    u.Username,
			FirstName:   u.FirstName,
			DisplayName: formatUserDisplay(u.ID, u.Username, u.FirstName),
			Source:      "@db",
		}, rest, nil
	}

	chat, err := h.bot.GetChat(context.Background(), &telego.GetChatParams{
		ChatID: telego.ChatID{Username: "@" + uname},
	})
	if err != nil {
		log.Printf("[处理器] resolveTarget getChat 失败: @%s err=%v", uname, err)
		return nil, nil, fmt.Errorf("找不到用户 @%s,请检查用户名是否正确", uname)
	}
	if chat == nil || chat.ID == 0 {
		return nil, nil, fmt.Errorf("找不到用户 @%s", uname)
	}
	return &targetUser{
		ID:          chat.ID,
		Username:    chat.Username,
		FirstName:   chat.FirstName,
		DisplayName: formatUserDisplay(chat.ID, chat.Username, chat.FirstName),
		Source:      "@api",
	}, rest, nil
}

func formatUserDisplay(id int64, username, firstName string) string {
	if username != "" {
		return fmt.Sprintf("@%s (<code>%d</code>)", htmlEscape(username), id)
	}
	if firstName != "" {
		return fmt.Sprintf("%s (<code>%d</code>)", htmlEscape(firstName), id)
	}
	return fmt.Sprintf("<code>%d</code>", id)
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// banTypeChinese 把 BanType 转成中文描述
func banTypeChinese(bt models.BanType) string {
	switch bt {
	case models.BanTypeName:
		return "昵称违规"
	case models.BanTypeBio:
		return "简介违规"
	case models.BanTypeMsg:
		return "消息违规"
	case models.BanTypeExternalReply:
		return "频道回复违规"
	case models.BanTypeManual:
		return "手动封禁"
	case models.BanTypeFCommand:
		return "管理员封杀"
	case models.BanTypeMute:
		return "禁言"
	case models.BanTypeContact:
		return "发送用户名片"
	case models.BanTypeNonAdminBot:
		return "非管理员Bot发言"
	case models.BanTypeForward:
		return "转发来源屏蔽"
	case models.BanTypeInvite:
		return "群名违规"
	default:
		return "其他违规"
	}
}

// friendlyTelegramError 把 Telegram/telego 的英文错误翻成简短中文提示
// 不会向用户暴露任何技术细节;原文仍由调用方写入日志
func friendlyTelegramError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not enough rights"):
		return "机器人权限不足,请将其设为管理员并开启对应权限"
	case strings.Contains(msg, "user_not_participant"),
		strings.Contains(msg, "participant_id_invalid"):
		return "目标用户不在该群组中"
	case strings.Contains(msg, "user is an administrator"):
		return "无法对管理员执行此操作"
	case strings.Contains(msg, "username_not_occupied"),
		strings.Contains(msg, "username not occupied"),
		strings.Contains(msg, "user not found"):
		return "找不到该用户"
	case strings.Contains(msg, "chat not found"):
		return "找不到该群组"
	case strings.Contains(msg, "bot was kicked"),
		strings.Contains(msg, "bot is not a member"),
		strings.Contains(msg, "forbidden"):
		return "机器人已被移出该群或权限不足"
	case strings.Contains(msg, "method is available only for supergroups"):
		return "该操作仅在超级群组中可用"
	case strings.Contains(msg, "too many requests"),
		strings.Contains(msg, "retry after"):
		return "操作过于频繁,请稍后再试"
	case strings.Contains(msg, "message to delete not found"),
		strings.Contains(msg, "message_id_invalid"):
		return "消息已不存在或已被删除"
	default:
		return "操作失败,请稍后再试"
	}
}

// handleKickCommand 处理踢人命令 /t
// 用法:
//   - 回复目标消息: /t
//   - @用户名:      /t @bigbigbig
//   - 数字 ID:      /t 12345
func (h *Handler) handleKickCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, _, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	if err := h.kickUserFromChat(message.Chat.ID, target.ID, "/t"); err != nil {
		log.Printf("[处理器] /t 踢人失败: target=%s err=%v", target.DisplayName, err)
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 踢出失败: %s\n%s", target.DisplayName, friendlyTelegramError(err)))
		return
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已踢出 %s", target.DisplayName))

	var kickOriginal string
	if message.ReplyToMessage != nil {
		kickOriginal = h.extractMessageText(message.ReplyToMessage)
	}
	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:        &message.Chat,
		UserID:      target.ID,
		UserName:    utils.GetFullName(target.FirstName, ""),
		Username:    target.Username,
		Action:      "踢出",
		Duration:    "",
		Reason:      "",
		MessageText: kickOriginal,
		OperatorID:  message.From.ID,
	})
	h.notifyWorkGroupModeration("踢出", message, target, "", "")
}

// handleLhCommand 处理拉黑命令 /lh
// 用法:
//   - 回复目标消息:               /lh [时长] [原因]
//   - @用户名 / 数字ID:           /lh @bigbigbig [时长] [原因]
//   - 例: /lh 10m 刷屏广告 / /lh @user 1d 广告
//
// 时长支持: 30s / 10m / 2h / 1d (无时长则永久);永久拉黑会异步写入数据库并跨群级联.
func (h *Handler) handleLhCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, rest, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	var (
		untilDate    int64
		durationText string
		reason       string // 仅管理员显式填写时才有值;空则通知/反馈不展示原因行
	)

	if len(rest) > 0 {
		if d, ok := parseDurationWithDay(rest[0]); ok {
			untilDate = time.Now().Add(d).Unix()
			durationText = rest[0]
			if len(rest) > 1 {
				reason = strings.Join(rest[1:], " ")
			}
		} else {
			reason = strings.Join(rest, " ")
		}
	}
	persistReason := reason
	if persistReason == "" {
		persistReason = "管理员拉黑"
	}

	params := &telego.BanChatMemberParams{
		ChatID:         tu.ID(message.Chat.ID),
		UserID:         target.ID,
		RevokeMessages: true, // 官方参数: 删除该用户在群内全部消息
	}
	if untilDate > 0 {
		params.UntilDate = untilDate
	}

	if err := h.bot.BanChatMember(context.Background(), params); err != nil {
		log.Printf("[处理器] /lh 拉黑失败: target=%s err=%v", target.DisplayName, err)
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 拉黑失败: %s\n%s", target.DisplayName, friendlyTelegramError(err)))
		return
	}

	chatID := message.Chat.ID
	operatorID := message.From.ID
	var msgID int
	var msgText string
	if message.ReplyToMessage != nil {
		msgID = message.ReplyToMessage.MessageID
		msgText = h.extractMessageText(message.ReplyToMessage)
		go func(cid int64, mid int) {
			if err := h.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
				ChatID:    tu.ID(cid),
				MessageID: mid,
			}); err != nil {
				log.Printf("[处理器] /lh 删除原文失败: %v", err)
			}
		}(chatID, msgID)
	}

	durationLabel := "永久"
	if untilDate > 0 {
		durationLabel = durationText
	}

	feedback := fmt.Sprintf("✅ 已永久拉黑 %s", target.DisplayName)
	if untilDate > 0 {
		feedback = fmt.Sprintf("✅ 已临时拉黑 %s\n时长: %s", target.DisplayName, durationText)
	}
	if reason != "" {
		feedback += "\n原因: " + htmlEscape(reason)
	}
	h.notifySvc.SendMessage(message.Chat.ID, feedback)

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:        &message.Chat,
		UserID:      target.ID,
		UserName:    utils.GetFullName(target.FirstName, ""),
		Username:    target.Username,
		Action:      "拉黑",
		Duration:    durationLabel,
		Reason:      reason,
		MessageText: msgText,
		OperatorID:  message.From.ID,
	})
	h.notifyWorkGroupModeration("拉黑", message, target, durationLabel, reason)

	if untilDate > 0 {
		// 临时拉黑: 异步写入 BanRecord(不设 IsBanned, 允许到期自动恢复)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[处理器] /lh 临时异步任务 panic: %v", r)
				}
			}()
			if err := h.banService.RecordTempBan(chatID, target.ID, untilDate, persistReason, operatorID, msgID, msgText); err != nil {
				log.Printf("[处理器] /lh 临时拉黑记录失败: %v", err)
			}
		}()
		return
	}

	// 永久拉黑: 异步写入数据库 + 跨群级联(避免阻塞反馈)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[处理器] /lh 异步任务 panic: %v", r)
			}
		}()
		if err := h.banService.BanUser(
			chatID,
			target.ID,
			models.BanTypeManual,
			persistReason,
			"",
			msgText,
			operatorID,
			msgID,
			true, // skipKick: 当前群已 BanChatMember,避免重复
		); err != nil {
			log.Printf("[处理器] /lh 记录封禁状态失败: %v", err)
		}
	}()
}

// handleUnlhCommand 处理解除拉黑 /unlh
// 用法:
//   - /unlh (回复消息)
//   - /unlh @bigbigbig
//   - /unlh 12345
func (h *Handler) handleUnlhCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}
	h.banService.RegisterChat(message.Chat.ID)

	target, _, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	// 当前群组解封(覆盖临时拉黑场景: DB 无记录也能恢复)
	var apiErr error
	if err := h.bot.UnbanChatMember(context.Background(), &telego.UnbanChatMemberParams{
		ChatID: tu.ID(message.Chat.ID),
		UserID: target.ID,
	}); err != nil {
		log.Printf("[处理器] /unlh 当前群组解封失败: target=%s err=%v", target.DisplayName, err)
		apiErr = err
	}

	// 立即反馈(后续 DB / 跨群解封异步)
	feedback := fmt.Sprintf("✅ 已解除拉黑 %s\n正在同步解除所有管理群", target.DisplayName)
	if apiErr != nil {
		feedback = fmt.Sprintf("⚠️ 当前群解除失败: %s\n%s\n其他管理群仍将继续同步解除", target.DisplayName, friendlyTelegramError(apiErr))
	}
	h.notifySvc.SendMessage(message.Chat.ID, feedback)
	h.notifyWorkGroupModeration("解除拉黑", message, target, "", "")

	// 异步: 清理数据库封禁状态 + 批量解封其他群组 + 撤销未到期临时拉黑记录
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[处理器] /unlh 异步任务 panic: %v", r)
			}
		}()
		if err := h.banService.UnbanUser(target.ID); err != nil {
			log.Printf("[处理器] /unlh 清理封禁记录失败: %v", err)
		}
		if err := h.banService.RevokeLatestActiveRecord(target.ID, models.BanTypeManual, models.BanTypeFCommand); err != nil {
			log.Printf("[处理器] /unlh 撤销临时拉黑记录失败: %v", err)
		}
	}()
}

// handleJyCommand 处理禁言命令 /jy
// 用法:
//   - 回复目标消息:                /jy [时长] [原因]
//   - @用户名 / 数字ID:            /jy @bigbigbig [时长] [原因]
//
// 时长支持: 30s / 10m / 2h / 1d (无时长则永久禁言).
func (h *Handler) handleJyCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, rest, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	var (
		untilDate    int64
		durationText string
		reason       string
	)
	if len(rest) > 0 {
		if d, ok := parseDurationWithDay(rest[0]); ok {
			untilDate = time.Now().Add(d).Unix()
			durationText = rest[0]
			if len(rest) > 1 {
				reason = strings.Join(rest[1:], " ")
			}
		} else {
			reason = strings.Join(rest, " ")
		}
	}

	params := &telego.RestrictChatMemberParams{
		ChatID:      tu.ID(message.Chat.ID),
		UserID:      target.ID,
		Permissions: lockedPermissions(),
	}
	if untilDate > 0 {
		params.UntilDate = untilDate
	}

	if err := h.bot.RestrictChatMember(context.Background(), params); err != nil {
		log.Printf("[处理器] /jy 禁言失败: target=%s err=%v", target.DisplayName, err)
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 禁言失败: %s\n%s", target.DisplayName, friendlyTelegramError(err)))
		return
	}

	feedback := fmt.Sprintf("✅ 已禁言 %s", target.DisplayName)
	if durationText != "" {
		feedback += "\n时长: " + durationText
	}
	if reason != "" {
		feedback += "\n原因: " + htmlEscape(reason)
	}
	h.notifySvc.SendMessage(message.Chat.ID, feedback)

	bcDuration := "永久"
	if durationText != "" {
		bcDuration = durationText
	}
	var jyOriginal string
	if message.ReplyToMessage != nil {
		jyOriginal = h.extractMessageText(message.ReplyToMessage)
	}
	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:        &message.Chat,
		UserID:      target.ID,
		UserName:    utils.GetFullName(target.FirstName, ""),
		Username:    target.Username,
		Action:      "禁言",
		Duration:    bcDuration,
		Reason:      reason,
		MessageText: jyOriginal,
		OperatorID:  message.From.ID,
	})
	h.notifyWorkGroupModeration("禁言", message, target, bcDuration, reason)

	// 异步写 BanRecord(mute), 便于用户在私聊 /start 查询原因并自助解除
	chatID := message.Chat.ID
	operatorID := message.From.ID
	muteReason := reason
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[处理器] /jy 异步任务 panic: %v", r)
			}
		}()
		if err := h.banService.RecordMute(chatID, target.ID, untilDate, muteReason, operatorID); err != nil {
			log.Printf("[处理器] /jy 记录禁言失败: %v", err)
		}
	}()
}

// handleUnCommand 管理员快捷解除: 引用机器人自动处罚提醒后发送 /un
// 例如水群/刷屏提醒「xxx 触发水群检测，有问题联系管理员。」——回复该消息 /un 即可解禁,
// 无需再查用户名或数字 ID。
func (h *Handler) handleUnCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	reply := message.ReplyToMessage
	if reply == nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 请引用机器人的处罚提醒消息后发送 /un")
		return
	}
	if !h.isOwnBotMessage(reply) {
		// 引用的是用户消息时,走与 /unjy 相同的解除禁言路径,保持便捷
		h.handleUnjyCommand(message, args)
		return
	}

	userID, ok := h.resolveUserIDFromModerationNotice(reply)
	if !ok || userID <= 0 {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 无法从该提醒中识别被处罚用户,请改用 /unjy 指定用户")
		return
	}

	target := &targetUser{
		ID:          userID,
		DisplayName: formatUserDisplay(userID, "", ""),
		Source:      "bot_notice",
	}
	if u, err := h.userRepo.GetByID(userID); err == nil && u != nil {
		target.Username = u.Username
		target.FirstName = u.FirstName
		target.DisplayName = formatUserDisplay(u.ID, u.Username, u.FirstName)
	}

	h.unmuteTarget(message, target, "un")
}

// handleUnjyCommand 处理解除禁言 /unjy
// 用法:
//   - /unjy (回复消息)
//   - /unjy @bigbigbig
//   - /unjy 12345
func (h *Handler) handleUnjyCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, _, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}
	h.unmuteTarget(message, target, "unjy")
}

// unmuteTarget 解除目标用户在本群的禁言,并异步撤销 mute 记录
func (h *Handler) unmuteTarget(message *telego.Message, target *targetUser, cmd string) {
	if message == nil || target == nil {
		return
	}
	if err := h.bot.RestrictChatMember(context.Background(), &telego.RestrictChatMemberParams{
		ChatID:      tu.ID(message.Chat.ID),
		UserID:      target.ID,
		Permissions: service.OpenChatPermissions(),
	}); err != nil {
		log.Printf("[处理器] /%s 解除禁言失败: target=%s err=%v", cmd, target.DisplayName, err)
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 解除禁言失败: %s\n%s", target.DisplayName, friendlyTelegramError(err)))
		return
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已解除禁言 %s", target.DisplayName))
	h.notifyWorkGroupModeration("解除禁言", message, target, "", "")

	targetID := target.ID
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[处理器] /%s 异步任务 panic: %v", cmd, r)
			}
		}()
		if err := h.banService.RevokeLatestActiveRecord(targetID, models.BanTypeMute); err != nil {
			log.Printf("[处理器] /%s 撤销禁言记录失败: %v", cmd, err)
		}
	}()
}

func (h *Handler) isOwnBotMessage(message *telego.Message) bool {
	if message == nil || message.From == nil || h.bot == nil {
		return false
	}
	return message.From.IsBot && message.From.ID == h.bot.ID()
}

// resolveUserIDFromModerationNotice 从机器人处罚提醒中还原被处罚用户 ID
// 优先读进程内登记,再解析消息实体里的 tg://user?id= / text_mention
func (h *Handler) resolveUserIDFromModerationNotice(notice *telego.Message) (int64, bool) {
	if notice == nil {
		return 0, false
	}
	if id, ok := h.lookupModerationNotice(notice.Chat.ID, notice.MessageID); ok {
		return id, true
	}
	return extractUserIDFromNoticeEntities(notice)
}

func extractUserIDFromNoticeEntities(notice *telego.Message) (int64, bool) {
	if notice == nil {
		return 0, false
	}
	entities := notice.Entities
	if len(entities) == 0 {
		entities = notice.CaptionEntities
	}
	for _, e := range entities {
		if e.Type == telego.EntityTypeTextMention && e.User != nil && e.User.ID > 0 {
			return e.User.ID, true
		}
	}
	for _, e := range entities {
		if e.Type != telego.EntityTypeTextLink || e.URL == "" {
			continue
		}
		if id, ok := parseTgUserIDFromURL(e.URL); ok {
			return id, true
		}
	}
	// 兜底: 正文里直接出现 tg://user?id=
	if id, ok := parseTgUserIDFromURL(notice.Text); ok {
		return id, true
	}
	return 0, false
}

func parseTgUserIDFromURL(raw string) (int64, bool) {
	const prefix = "tg://user?id="
	idx := strings.Index(raw, prefix)
	if idx < 0 {
		return 0, false
	}
	rest := raw[idx+len(prefix):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// lockedPermissions 关闭所有发言权限 (用于 /jy)
func lockedPermissions() telego.ChatPermissions {
	f := false
	return telego.ChatPermissions{
		CanSendMessages:       &f,
		CanSendAudios:         &f,
		CanSendDocuments:      &f,
		CanSendPhotos:         &f,
		CanSendVideos:         &f,
		CanSendVideoNotes:     &f,
		CanSendVoiceNotes:     &f,
		CanSendPolls:          &f,
		CanSendOtherMessages:  &f,
		CanAddWebPagePreviews: &f,
	}
}

func parseDurationWithDay(raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}

	last := raw[len(raw)-1]
	switch last {
	case 's', 'm', 'h':
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return 0, false
		}
		return d, true
	case 'd':
		num := strings.TrimSuffix(raw, "d")
		if num == "" {
			return 0, false
		}
		n, err := strconv.Atoi(num)
		if err != nil || n <= 0 {
			return 0, false
		}
		return time.Duration(n) * 24 * time.Hour, true
	default:
		return 0, false
	}
}

// handleStart 处理 /start 命令
func (h *Handler) handleStart(message *telego.Message) {
	userID := message.From.ID

	// 获取用户详细状态
	user, banRecord, err := h.banService.GetUserStatus(userID)

	var text string
	var keyboard *telego.InlineKeyboardMarkup

	// 构造自助解除按钮(当且仅当未使用过机会)
	selfReleaseKeyboard := func(label string) *telego.InlineKeyboardMarkup {
		return &telego.InlineKeyboardMarkup{
			InlineKeyboard: [][]telego.InlineKeyboardButton{
				{tu.InlineKeyboardButton(label).WithCallbackData("self_unban")},
			},
		}
	}

	switch {
	case err != nil || user == nil:
		text = "✅ 您的状态正常"

	case user.IsBanned:
		text = "❌ 您已被封禁\n\n"
		if user.BannedAt != nil {
			text += fmt.Sprintf("⏰ 封禁时间: %s\n", user.BannedAt.Format("2006-01-02 15:04:05"))
		}
		if user.BanReason != "" {
			text += fmt.Sprintf("📝 封禁原因: %s\n", user.BanReason)
		}
		if banRecord != nil {
			text += fmt.Sprintf("🔍 违规类型: %s\n", banTypeChinese(banRecord.BanType))
		}
		if !user.SelfUnbanUsed {
			text += "\n⚠️ 自助解封说明:\n"
			text += "• 每个账号仅有一次自助解除机会\n"
			text += "• 使用后将无法再次自助解除\n"
			text += "• 请谨慎发言,避免再次违规"
			keyboard = selfReleaseKeyboard("🔓 自助解封(仅一次机会)")
		} else {
			text += "\n❌ 您已使用过自助解除机会\n如有疑问,请联系管理员"
		}

	default:
		// 非永久封禁: 检查是否存在临时拉黑 / 禁言的活跃记录
		active, _ := h.banService.GetLatestActiveRecord(userID,
			models.BanTypeManual, models.BanTypeFCommand, models.BanTypeMute)
		if active == nil {
			text = "✅ 您的状态正常"
		} else {
			isMute := active.BanType == models.BanTypeMute
			if isMute {
				text = "🔇 您当前处于 <b>禁言</b> 状态\n\n"
			} else {
				text = "⛔ 您当前处于 <b>临时拉黑</b> 状态\n\n"
			}
			text += fmt.Sprintf("⏰ 处罚时间: %s\n", active.BannedAt.Format("2006-01-02 15:04:05"))
			if active.UntilDate != nil {
				text += fmt.Sprintf("⌛ 到期时间: %s\n", active.UntilDate.Format("2006-01-02 15:04:05"))
			} else {
				text += "⌛ 到期时间: 永久\n"
			}
			if active.Reason != "" {
				text += fmt.Sprintf("📝 原因: %s\n", htmlEscape(active.Reason))
			}
			if operator, _ := h.userRepo.GetByID(active.BannedBy); operator != nil {
				name := operator.FirstName
				if operator.LastName != "" {
					name += " " + operator.LastName
				}
				if operator.Username != "" {
					name += " @" + operator.Username
				}
				if name != "" {
					text += fmt.Sprintf("👮 操作人: %s\n", htmlEscape(name))
				}
			}

			if !user.SelfUnbanUsed {
				text += "\n⚠️ 自助解除说明:\n"
				text += "• 每个账号仅有一次自助解除机会\n"
				text += "• 使用后将无法再次自助解除\n"
				text += "• 请谨慎发言,避免再次违规"
				if isMute {
					keyboard = selfReleaseKeyboard("🔓 自助解除禁言(仅一次机会)")
				} else {
					keyboard = selfReleaseKeyboard("🔓 自助解除拉黑(仅一次机会)")
				}
			} else {
				text += "\n❌ 您已使用过自助解除机会\n如有疑问,请联系管理员"
			}
		}
	}

	// 如果是管理员,添加选择用户按钮
	if h.IsAdmin(userID) {
		// 管理员私聊显示选择用户按钮
		if message.Chat.Type == "private" {
			replyKeyboard := &telego.ReplyKeyboardMarkup{
				Keyboard: [][]telego.KeyboardButton{
					{
						{
							Text: "👤 选择用户",
							RequestUsers: &telego.KeyboardButtonRequestUsers{
								RequestID:   1,
								UserIsBot:   boolPtr(false),
								MaxQuantity: 1,
							},
						},
					},
				},
				ResizeKeyboard:  true,
				OneTimeKeyboard: true,
			}

			h.bot.SendMessage(context.Background(), &telego.SendMessageParams{
				ChatID:      tu.ID(message.Chat.ID),
				Text:        text,
				ReplyMarkup: replyKeyboard,
				ParseMode:   "HTML",
			})
			return
		}
	}

	// 发送消息(带或不带内联键盘)
	if keyboard != nil {
		h.notifySvc.SendMessageWithKeyboard(message.Chat.ID, text, keyboard)
	} else {
		h.notifySvc.SendMessage(message.Chat.ID, text)
	}
}

// handleBanCommand 处理封禁命令 /f
// 用法:
//   - 回复目标消息: /f
//   - @用户名:      /f @bigbigbig
//   - 数字 ID:      /f 12345
//
// 流程: 同步 BanChatMember(+删除被回复消息) → 立即反馈 → 异步写 DB / 跨群级联.
func (h *Handler) handleBanCommand(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, _, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	// 同步: 当前群拉黑(用户最关注的反馈); RevokeMessages 对齐官方删消息能力
	if err := h.bot.BanChatMember(context.Background(), &telego.BanChatMemberParams{
		ChatID:         tu.ID(message.Chat.ID),
		UserID:         target.ID,
		RevokeMessages: true,
	}); err != nil {
		log.Printf("[处理器] /f 拉黑失败: target=%s err=%v", target.DisplayName, err)
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 封禁失败: %s\n%s", target.DisplayName, friendlyTelegramError(err)))
		return
	}

	// 同步: 如果是回复模式则尝试删除被回复消息
	var msgID int
	var msgText string
	if message.ReplyToMessage != nil {
		msgID = message.ReplyToMessage.MessageID
		msgText = h.extractMessageText(message.ReplyToMessage)
		go func(chatID int64, mid int) {
			if err := h.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
				ChatID:    tu.ID(chatID),
				MessageID: mid,
			}); err != nil {
				log.Printf("[处理器] /f 删除消息失败: %v", err)
			}
		}(message.Chat.ID, msgID)
	}

	if msgText != "" {
		if err := h.keywordSvc.AddKeyword(models.KeywordTypeMsg, msgText, message.From.ID); err != nil {
			log.Printf("[处理器] /f 自动添加消息关键词失败: keyword=%s err=%v", msgText, err)
		} else if h.registry != nil {
			go h.registry.SyncKeywordAdd(string(models.KeywordTypeMsg), msgText, message.From.ID)
		}
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已封禁 %s", target.DisplayName))

	h.broadcastSvc.SendBanAsync(service.BanBroadcast{
		Chat:        &message.Chat,
		UserID:      target.ID,
		UserName:    utils.GetFullName(target.FirstName, ""),
		Username:    target.Username,
		Action:      "拉黑",
		Duration:    "永久",
		Reason:      "",
		MessageText: msgText,
		OperatorID:  message.From.ID,
	})
	h.notifyWorkGroupModeration("拉黑", message, target, "永久", "")

	// 异步: DB 写入 + 跨群级联(skipKick=true 因为当前群已 BanChatMember)
	chatID := message.Chat.ID
	operatorID := message.From.ID
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[处理器] /f 异步任务 panic: %v", r)
			}
		}()
		if err := h.banService.BanUser(
			chatID,
			target.ID,
			models.BanTypeManual,
			"管理员手动封禁",
			"",
			msgText,
			operatorID,
			0, // 不再交给 banService 删消息(已在上方 goroutine 处理)
			true,
		); err != nil {
			log.Printf("[处理器] /f 异步封禁失败: %v", err)
		}
	}()
}

// handleNameCommand 处理昵称关键词命令
func (h *Handler) handleNameCommand(message *telego.Message, args []string) {
	if !h.IsAdmin(message.From.ID) {
		return
	}

	if !h.requireWorkGroup(message) {
		return
	}

	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID, "用法: /name <关键词> 或 /name list")
		return
	}

	if args[0] == "list" {
		h.sendKeywordList(message.Chat.ID, models.KeywordTypeName)
		return
	}

	keyword := strings.Join(args, " ")
	if err := h.keywordSvc.AddKeyword(models.KeywordTypeName, keyword, message.From.ID); err != nil {
		log.Printf("[处理器] /name 添加失败: keyword=%s err=%v", keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 添加失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncKeywordAdd(string(models.KeywordTypeName), keyword, message.From.ID)
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已拉黑昵称关键词: <code>%s</code>", htmlEscape(keyword)))
}

// handleBioCommand 处理简介关键词命令
func (h *Handler) handleBioCommand(message *telego.Message, args []string) {
	if !h.IsAdmin(message.From.ID) {
		return
	}

	if !h.requireWorkGroup(message) {
		return
	}

	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID, "用法: /bio <关键词> 或 /bio list")
		return
	}

	if args[0] == "list" {
		h.sendKeywordList(message.Chat.ID, models.KeywordTypeBio)
		return
	}

	keyword := strings.Join(args, " ")

	// 1. 添加关键词
	if err := h.keywordSvc.AddKeyword(models.KeywordTypeBio, keyword, message.From.ID); err != nil {
		log.Printf("[处理器] /bio 添加失败: keyword=%s err=%v", keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 添加失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncKeywordAdd(string(models.KeywordTypeBio), keyword, message.From.ID)
	}

	// 2. 发送初始进度消息
	progressMsg, err := h.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID: tu.ID(message.Chat.ID),
		Text: fmt.Sprintf(
			"✅ 已拉黑简介关键词: <code>%s</code>\n\n📊 批量封禁结果:\n  • 正在准备批量封禁...",
			keyword,
		),
		ParseMode: "HTML",
	})
	if err != nil {
		log.Printf("[处理器] 发送进度消息失败: %v", err)
		return
	}

	// 3. 异步执行批量封禁(跨所有 Bot 数据库扫描)
	go func() {
		var totalBanned, totalSkipped int

		if h.registry != nil {
			// 在所有 Bot 的简介历史中搜索匹配用户并封禁
			banned, skipped := h.registry.SyncBatchBanByBio(keyword, message.From.ID)
			totalBanned = banned
			totalSkipped = skipped
		} else {
			// 无 Registry 时退化为仅本地扫描
			result := h.keywordSvc.BatchBanByBioKeyword(keyword, message.From.ID, h.bot, message.Chat.ID, progressMsg.MessageID)
			totalBanned = result.BannedCount
			totalSkipped = result.SkippedCount
		}

		// 4. 完成后更新为最终结果
		finalText := fmt.Sprintf(
			"✅ 已拉黑简介关键词: <code>%s</code>\n\n📊 批量封禁结果:\n  • 成功封禁: <b>%d</b> 人\n  • 已跳过封禁: <b>%d</b> 人",
			keyword, totalBanned, totalSkipped,
		)

		h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
			ChatID:    tu.ID(message.Chat.ID),
			MessageID: progressMsg.MessageID,
			Text:      finalText,
			ParseMode: "HTML",
		})
	}()
}

// handleMsgCommand 处理消息关键词命令
func (h *Handler) handleMsgCommand(message *telego.Message, args []string) {
	if !h.IsAdmin(message.From.ID) {
		return
	}

	if !h.requireWorkGroup(message) {
		return
	}

	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID, "用法: /msg <关键词> 或 /msg list")
		return
	}

	if args[0] == "list" {
		h.sendKeywordList(message.Chat.ID, models.KeywordTypeMsg)
		return
	}

	keyword := strings.Join(args, " ")
	if err := h.keywordSvc.AddKeyword(models.KeywordTypeMsg, keyword, message.From.ID); err != nil {
		log.Printf("[处理器] /msg 添加失败: keyword=%s err=%v", keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 添加失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncKeywordAdd(string(models.KeywordTypeMsg), keyword, message.From.ID)
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已拉黑消息关键词: <code>%s</code>", htmlEscape(keyword)))
}

// handleForwardKeywordCommand 处理转发来源屏蔽关键词命令
//
// /z <关键词>  — 添加转发来源屏蔽关键词
// /z list     — 列出已有的转发来源屏蔽关键词
//
// 命中逻辑: 群内转发消息的原始来源(频道/用户/群组/Bot)的名称或用户名
// 包含关键词时,删除消息并永久封禁转发者。仅对转发消息生效,
// 用户自身名称/用户名匹配但直接发送的消息不受影响。
func (h *Handler) handleForwardKeywordCommand(message *telego.Message, args []string) {
	if !h.IsAdmin(message.From.ID) {
		return
	}

	if !h.requireWorkGroup(message) {
		return
	}

	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID,
			"用法: /z &lt;关键词&gt; 或 /z list\n\n"+
				"• 添加后,转发来源名称或用户名包含关键词即命中\n"+
				"• 仅对转发消息生效,用户自身发言不受影响\n"+
				"• /z list 查看列表,点击可直接删除")
		return
	}

	if args[0] == "list" {
		h.sendKeywordList(message.Chat.ID, models.KeywordTypeForward)
		return
	}

	keyword := strings.Join(args, " ")
	if err := h.keywordSvc.AddKeyword(models.KeywordTypeForward, keyword, message.From.ID); err != nil {
		log.Printf("[处理器] /z 添加失败: keyword=%s err=%v", keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 添加失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncKeywordAdd(string(models.KeywordTypeForward), keyword, message.From.ID)
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已添加转发来源屏蔽: <code>%s</code>", htmlEscape(keyword)))
}

// handleInviteKeywordCommand 处理邀请链接群名关键词。
// 解析消息和用户简介里的链接，只拿群名做匹配，不看群简介。
func (h *Handler) handleInviteKeywordCommand(message *telego.Message, args []string) {
	if !h.IsAdmin(message.From.ID) {
		return
	}
	if !h.requireWorkGroup(message) {
		return
	}
	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID,
			"用法: /q &lt;关键词&gt; 或 /q list\n\n消息和用户简介里的邀请链接，群名包含关键词即拉黑。不看群简介")
		return
	}
	if args[0] == "list" {
		h.sendKeywordList(message.Chat.ID, models.KeywordTypeInvite)
		return
	}

	keyword := strings.Join(args, " ")
	if err := h.keywordSvc.AddKeyword(models.KeywordTypeInvite, keyword, message.From.ID); err != nil {
		log.Printf("[处理器] /q 添加失败: keyword=%s err=%v", keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 添加失败，群名关键词没有写入")
		return
	}
	if h.registry != nil {
		go h.registry.SyncKeywordAdd(string(models.KeywordTypeInvite), keyword, message.From.ID)
	}
	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已拉黑群名关键词: <code>%s</code>", htmlEscape(keyword)))
}

func (h *Handler) handleRemoveKeywordCommand(message *telego.Message, args []string, cmd string) {
	spec, ok := removeKeywordCommands[cmd]
	if !ok || message.From == nil {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}
	if !h.requireWorkGroup(message) {
		return
	}
	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID, "用法: /"+spec.cmd+" &lt;关键词&gt;")
		return
	}

	keyword := strings.Join(args, " ")
	removed, err := h.keywordSvc.DeleteEquivalentKeyword(spec.typ, keyword)
	if err != nil {
		log.Printf("[处理器] /%s 删除失败: keyword=%s err=%v", spec.cmd, keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 删除失败，"+spec.label+"还在")
		return
	}
	if removed == "" {
		h.notifySvc.SendMessage(message.Chat.ID, "没有这条"+spec.label+": <code>"+htmlEscape(keyword)+"</code>")
		return
	}
	if h.registry != nil {
		go h.registry.SyncKeywordDelete(string(spec.typ), removed)
	}
	h.notifySvc.SendMessage(message.Chat.ID, "✅ 已删除"+spec.label+": <code>"+htmlEscape(removed)+"</code>")
}

type removeKeywordSpec struct {
	typ   models.KeywordType
	cmd   string
	label string
}

var removeKeywordCommands = map[string]removeKeywordSpec{
	"unname": {models.KeywordTypeName, "unname", "昵称关键词"},
	"unbio":  {models.KeywordTypeBio, "unbio", "简介关键词"},
	"unmsg":  {models.KeywordTypeMsg, "unmsg", "消息关键词"},
	"unq":    {models.KeywordTypeInvite, "unq", "群名关键词"},
	"unz":    {models.KeywordTypeForward, "unz", "转发来源"},
	"unno":   {models.KeywordTypeDupWhite, "unno", "水群白名单"},
}

// handleWhitelistAdd 处理加白命令: 加白 @用户名 / 加白 数字ID / 回复消息+加白
func (h *Handler) handleWhitelistAdd(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, _, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	if h.whitelistSvc.IsWhitelisted(target.ID) {
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("ℹ️ %s 已在白名单中", target.DisplayName))
		return
	}

	if err := h.whitelistSvc.Add(target.ID, message.From.ID); err != nil {
		log.Printf("[白名单] 加白失败: target=%d err=%v", target.ID, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 加白失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncWhitelistAdd(target.ID, message.From.ID)
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已加白 %s", target.DisplayName))
}

// handleWhitelistRemove 处理移白命令: 移白 @用户名 / 移白 数字ID / 回复消息+移白
func (h *Handler) handleWhitelistRemove(message *telego.Message, args []string) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	target, _, err := h.resolveTarget(message, args)
	if err != nil {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ "+err.Error())
		return
	}

	if !h.whitelistSvc.IsWhitelisted(target.ID) {
		h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("ℹ️ %s 不在白名单中", target.DisplayName))
		return
	}

	if err := h.whitelistSvc.Remove(target.ID); err != nil {
		log.Printf("[白名单] 移白失败: target=%d err=%v", target.ID, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 移白失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncWhitelistDelete(target.ID)
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已移白 %s", target.DisplayName))
}

// handleWhitelistList 处理白名单列表命令
func (h *Handler) handleWhitelistList(message *telego.Message) {
	if !h.requireGroup(message) {
		return
	}
	if !h.IsAdmin(message.From.ID) {
		return
	}

	text, keyboard, err := h.buildWhitelistPage(1)
	if err != nil {
		log.Printf("[白名单] 获取列表失败: %v", err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 获取白名单列表失败")
		return
	}
	if keyboard == nil {
		h.notifySvc.SendMessage(message.Chat.ID, text)
		return
	}
	h.notifySvc.SendMessageWithKeyboard(message.Chat.ID, text, keyboard)
}

// buildWhitelistPage 构建白名单列表的指定页
func (h *Handler) buildWhitelistPage(page int) (string, *telego.InlineKeyboardMarkup, error) {
	_, total, err := h.whitelistSvc.GetPaginated(0, 1)
	if err != nil {
		return "", nil, err
	}
	if total == 0 {
		return "📋 <b>用户白名单</b>\n\n暂无白名单用户", nil, nil
	}

	page, totalPages := clampPage(int(total), page, listPageSize)
	start := (page - 1) * listPageSize
	items, _, err := h.whitelistSvc.GetPaginated(start, listPageSize)
	if err != nil {
		return "", nil, err
	}

	buttons := make([]telego.InlineKeyboardButton, 0, len(items))
	for _, item := range items {
		label := fmt.Sprintf("%d", item.UserID)
		if u, _ := h.userRepo.GetByID(item.UserID); u != nil {
			if u.Username != "" {
				label = "@" + u.Username
			} else if u.FirstName != "" {
				label = u.FirstName
			}
		}
		buttons = append(buttons, tu.InlineKeyboardButton(label).WithCallbackData(
			fmt.Sprintf("del_wl_%d:%d", item.UserID, page),
		))
	}

	rows := chunkButtonsToRows(buttons, listCols)
	if nav := pageNavRow(page, totalPages, "wlp"); nav != nil {
		rows = append(rows, nav)
	}
	rows = append(rows, closeRow())

	text := fmt.Sprintf("📋 <b>用户白名单</b> · 第 %d/%d 页 · 共 %d 人\n点击用户即可移除", page, totalPages, total)
	return text, &telego.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

// handleDeleteWhitelistUser 处理白名单列表中的删除回调
func (h *Handler) handleDeleteWhitelistUser(query *telego.CallbackQuery) {
	rest := strings.TrimPrefix(query.Data, "del_wl_")
	var targetID int64
	page := 1
	if idx := strings.Index(rest, ":"); idx > 0 {
		id, err := strconv.ParseInt(rest[:idx], 10, 64)
		if err != nil {
			return
		}
		targetID = id
		if p, err := strconv.Atoi(rest[idx+1:]); err == nil && p > 0 {
			page = p
		}
	} else {
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return
		}
		targetID = id
	}

	if err := h.whitelistSvc.Remove(targetID); err != nil {
		log.Printf("[Callback] 移除白名单用户失败: user=%d err=%v", targetID, err)
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 移除失败",
			ShowAlert:       true,
		})
		return
	}

	if h.registry != nil {
		go h.registry.SyncWhitelistDelete(targetID)
	}

	h.renderWhitelistPage(query, page)

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 已移除",
	})
}

// renderWhitelistPage 刷新白名单列表到指定页
func (h *Handler) renderWhitelistPage(query *telego.CallbackQuery, page int) {
	text, keyboard, err := h.buildWhitelistPage(page)
	if err != nil {
		log.Printf("[Callback] 刷新白名单列表失败: %v", err)
		return
	}
	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()
	_, err = h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})
	if err != nil {
		log.Printf("[Callback] 编辑白名单消息失败: %v", err)
	}
}

// handleWhitelistPage 处理白名单列表翻页回调 (前缀 wlp:page:N)
func (h *Handler) handleWhitelistPage(query *telego.CallbackQuery) {
	parts := strings.Split(query.Data, ":")
	page := 1
	if len(parts) == 3 {
		if p, err := strconv.Atoi(parts[2]); err == nil && p > 0 {
			page = p
		}
	}
	h.renderWhitelistPage(query, page)
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}

// handleDuplicateWhitelistCommand 处理水群/刷屏禁言白名单命令。
func (h *Handler) handleDuplicateWhitelistCommand(message *telego.Message, args []string) {
	if !h.IsAdmin(message.From.ID) {
		return
	}
	if !h.requireWorkGroup(message) {
		return
	}
	if len(args) == 0 {
		h.notifySvc.SendMessage(message.Chat.ID, "用法: /no <白名单关键词> 或 /no list")
		return
	}
	if args[0] == "list" {
		h.sendDuplicateWhitelistList(message.Chat.ID)
		return
	}

	keyword := strings.TrimSpace(strings.Join(args, " "))
	if keyword == "" {
		h.notifySvc.SendMessage(message.Chat.ID, "白名单关键词不能为空")
		return
	}

	if err := h.keywordSvc.AddKeyword(models.KeywordTypeDupWhite, keyword, message.From.ID); err != nil {
		log.Printf("[处理器] /no 添加失败: keyword=%s err=%v", keyword, err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 添加失败,请稍后再试")
		return
	}

	if h.registry != nil {
		go h.registry.SyncKeywordAdd(string(models.KeywordTypeDupWhite), keyword, message.From.ID)
	}

	h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("✅ 已添加水群/刷屏白名单关键词: <code>%s</code>", htmlEscape(keyword)))
}

func duplicateWhitelistHash(keyword string) string {
	sum := sha1.Sum([]byte(keyword))
	return hex.EncodeToString(sum[:])
}

func (h *Handler) sendDuplicateWhitelistList(chatID int64) {
	text, keyboard, err := h.buildDuplicateWhitelistList(1)
	if err != nil {
		log.Printf("[处理器] 获取 /no list 失败: %v", err)
		h.notifySvc.SendMessage(chatID, "❌ 获取白名单失败,请稍后再试")
		return
	}
	if keyboard == nil {
		h.notifySvc.SendMessage(chatID, text)
		return
	}
	h.notifySvc.SendMessageWithKeyboard(chatID, text, keyboard)
}

// buildDuplicateWhitelistList 渲染 /no list 面板的第 page 页
//
// 按钮 callback data 形如 "del_no_<hash>:<page>",带上当前 page 让删除后能停在同一页;
// 旧消息上的 "del_no_<hash>" 调用方仍兼容,见 handleDeleteDuplicateWhitelist。
func (h *Handler) buildDuplicateWhitelistList(page int) (string, *telego.InlineKeyboardMarkup, error) {
	keywords, err := h.cache.GetDuplicateWhitelistKeywords()
	if err != nil {
		return "", nil, err
	}

	normalized := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword != "" {
			normalized = append(normalized, keyword)
		}
	}
	sort.Strings(normalized)

	total := len(normalized)
	if total == 0 {
		return "📋 <b>水群/刷屏白名单</b>\n\n暂无关键词", nil, nil
	}

	page, totalPages := clampPage(total, page, listPageSize)
	start := (page - 1) * listPageSize
	end := start + listPageSize
	if end > total {
		end = total
	}

	chunk := normalized[start:end]
	buttons := make([]telego.InlineKeyboardButton, 0, len(chunk))
	for _, keyword := range chunk {
		buttons = append(buttons, tu.InlineKeyboardButton(keyword).WithCallbackData(
			fmt.Sprintf("del_no_%s:%d", duplicateWhitelistHash(keyword), page),
		))
	}

	rows := chunkButtonsToRows(buttons, listCols)
	if nav := pageNavRow(page, totalPages, "nlw"); nav != nil {
		rows = append(rows, nav)
	}
	rows = append(rows, closeRow())

	text := fmt.Sprintf("📋 <b>水群/刷屏白名单</b> · 第 %d/%d 页 · 共 %d 个\n点击关键词即可删除", page, totalPages, total)
	return text, &telego.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

func (h *Handler) handleDeleteDuplicateWhitelist(query *telego.CallbackQuery) {
	// 兼容两种 callback data:
	//   旧版: del_no_<hash>           → 删除后回到第 1 页
	//   新版: del_no_<hash>:<page>    → 删除后停在 <page>
	rest := strings.TrimPrefix(query.Data, "del_no_")
	targetHash := rest
	page := 1
	if idx := strings.Index(rest, ":"); idx > 0 {
		targetHash = rest[:idx]
		if p, err := strconv.Atoi(rest[idx+1:]); err == nil && p > 0 {
			page = p
		}
	}

	keywords, err := h.cache.GetDuplicateWhitelistKeywords()
	if err != nil {
		log.Printf("[Callback] 获取重复消息白名单失败: %v", err)
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 获取列表失败",
			ShowAlert:       true,
		})
		return
	}

	var target string
	for _, keyword := range keywords {
		if duplicateWhitelistHash(keyword) == targetHash {
			target = keyword
			break
		}
	}
	if target == "" {
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "关键词不存在或已删除",
		})
		return
	}

	if err := h.keywordRepo.DeleteByTypeAndWord(models.KeywordTypeDupWhite, target); err != nil {
		log.Printf("[Callback] 删除水群白名单数据库记录失败: keyword=%s err=%v", target, err)
		h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ 删除失败",
			ShowAlert:       true,
		})
		return
	}
	if err := h.cache.RemoveDuplicateWhitelistKeyword(target); err != nil {
		log.Printf("[Callback] 删除水群白名单缓存失败: keyword=%s err=%v", target, err)
	}

	if h.registry != nil {
		go h.registry.SyncKeywordDelete(string(models.KeywordTypeDupWhite), target)
	}

	h.renderDuplicateWhitelistPage(query, page)

	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "✅ 已删除",
	})
}

// renderDuplicateWhitelistPage 把当前消息切换到指定页;clampPage 会处理页码越界
func (h *Handler) renderDuplicateWhitelistPage(query *telego.CallbackQuery, page int) {
	text, keyboard, err := h.buildDuplicateWhitelistList(page)
	if err != nil {
		log.Printf("[Callback] 刷新重复消息白名单失败: %v", err)
		return
	}
	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()
	_, err = h.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})
	if err != nil {
		log.Printf("[Callback] 编辑白名单消息失败: %v", err)
	}
}

// handleDuplicateWhitelistPage 处理 /no list 翻页 callback (前缀 nlw:page:N)
func (h *Handler) handleDuplicateWhitelistPage(query *telego.CallbackQuery) {
	parts := strings.Split(query.Data, ":")
	page := 1
	if len(parts) == 3 {
		if p, err := strconv.Atoi(parts[2]); err == nil && p > 0 {
			page = p
		}
	}
	h.renderDuplicateWhitelistPage(query, page)
	h.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
}

// sendKeywordList 发送关键词列表(用于命令)
func (h *Handler) sendKeywordList(chatID int64, keywordType models.KeywordType) {
	h.sendKeywordListPage(chatID, keywordType, 1)
}

func (h *Handler) sendKeywordListPage(chatID int64, keywordType models.KeywordType, page int) {
	text, keyboard, _, total, err := h.buildKeywordListPage(keywordType, page)
	if err != nil {
		h.notifySvc.SendMessage(chatID, "获取列表失败")
		return
	}

	if total == 0 {
		h.notifySvc.SendMessage(chatID, "暂无关键词")
		return
	}
	h.notifySvc.SendMessageWithKeyboard(chatID, text, keyboard)
}

// parseUserIdentifier 解析用户标识(用户ID或用户名)
func (h *Handler) parseUserIdentifier(identifier string) (userID int64, username string, err error) {
	// 去除前导 @
	identifier = strings.TrimPrefix(identifier, "@")

	// 尝试解析为用户 ID
	if id, parseErr := strconv.ParseInt(identifier, 10, 64); parseErr == nil {
		// 是纯数字,当作用户 ID
		user, _ := h.userRepo.GetByID(id)
		if user != nil {
			return id, user.Username, nil
		}
		return id, "", nil
	}

	// 尝试作为用户名查询
	user, err := h.userRepo.GetByUsername(identifier)
	if err == nil && user != nil {
		return user.ID, user.Username, nil
	}

	return 0, "", fmt.Errorf("无法找到用户: %s", identifier)
}

// handleAdminPanel 处理管理员面板命令
func (h *Handler) handleAdminPanel(message *telego.Message) {
	if !h.IsAdmin(message.From.ID) {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 无权限")
		return
	}

	h.notifySvc.SendMessageWithKeyboard(message.Chat.ID, h.buildAdminMainText(), h.buildAdminMainKeyboard())
}

// handleReloadCache 处理 /reload 命令 - 重新加载内存缓存
func (h *Handler) handleReloadCache(message *telego.Message) {
	// 只有管理员可以使用
	if !h.IsAdmin(message.From.ID) {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 无权限")
		return
	}

	// 只能在已绑定的工作群组使用; 未绑定或不是绑定群直接静默
	if !h.IsWorkGroup(message.Chat.ID) {
		return
	}

	h.notifySvc.SendMessage(message.Chat.ID, "🔄 正在重新加载内存缓存...")

	// 重新加载所有关键词到缓存
	err := h.keywordSvc.LoadAllKeywordsToCache()
	if err != nil {
		log.Printf("[Command] Failed to reload cache: %v", err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 重新加载失败,请稍后再试")
		return
	}

	h.notifySvc.SendMessage(message.Chat.ID, "✅ 内存缓存已重新加载\n\n所有关键词已从数据库同步到缓存")
}
