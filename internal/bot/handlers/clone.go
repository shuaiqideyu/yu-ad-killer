package handlers

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	sessionAddingBotToken   = "adding_bot_token"
	sessionAddingBotAdmin   = "adding_bot_admin"
	sessionBindingWork      = "binding_work_group"
	sessionBindingBroadcast = "binding_broadcast_chat"
	addBotPendingTTL        = 5 * time.Minute
)

var botTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)

// handleAddBotCommand 入口: /add 进入"添加克隆 Bot"会话(仅主 Bot 私聊作者可用)
func (h *Handler) handleAddBotCommand(message *telego.Message) {
	if !h.isMain {
		return
	}
	if message.Chat.Type != telego.ChatTypePrivate {
		return
	}
	if !h.IsAuthor(message.From.ID) {
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 只有作者可以添加克隆 Bot")
		return
	}
	h.SetSessionState(message.From.ID, sessionAddingBotToken, addBotPendingTTL)

	text := "➕ 添加克隆 Bot\n\n" +
		"请发送新机器人的 Bot Token, 例如:\n" +
		"<code>123456:ABCDEF...</code>\n\n" +
		"发送 /cancel 取消"
	h.notifySvc.SendMessage(message.Chat.ID, text)
}

// handleCancelCommand 取消所有正在进行的会话(克隆/绑定/添加管理员)
func (h *Handler) handleCancelCommand(message *telego.Message) {
	if message.Chat.Type != telego.ChatTypePrivate {
		return
	}
	state, exists := h.GetSessionState(message.From.ID)
	if !exists {
		return
	}
	h.ClearSessionState(message.From.ID)
	h.clearAddBotPending(message.From.ID)
	switch state {
	case sessionAddingBotToken, sessionAddingBotAdmin:
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 已取消添加克隆 Bot")
	case sessionBindingWork:
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 已取消绑定工作群")
	case sessionBindingBroadcast:
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 已取消绑定播报频道")
	default:
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 已取消")
	}
}

// handleAddBotTokenInput 在 /add 会话中接收 token
func (h *Handler) handleAddBotTokenInput(message *telego.Message) {
	if !h.isMain {
		return
	}
	token := strings.TrimSpace(message.Text)
	if !botTokenPattern.MatchString(token) {
		h.notifySvc.SendMessage(message.Chat.ID,
			"❌ Token 格式不对, 应形如 <code>123456:ABCDEF...</code>\n\n请重新发送 token, 或 /cancel 取消")
		return
	}

	h.setAddBotPending(message.From.ID, token)
	h.SetSessionState(message.From.ID, sessionAddingBotAdmin, addBotPendingTTL)

	text := "✅ 已收到 Token\n\n" +
		"请发送该机器人的管理员用户ID:\n" +
		"• 直接发送数字 ID(如 <code>123456789</code>)\n" +
		"• 或发送 @username\n\n" +
		"发送 /cancel 取消"
	h.notifySvc.SendMessage(message.Chat.ID, text)
}

// handleAddBotAdminInput 在 /add 第二步接收 admin id, 启动克隆 Bot
func (h *Handler) handleAddBotAdminInput(message *telego.Message) {
	if !h.isMain {
		return
	}

	pending := h.popAddBotPending(message.From.ID)
	if pending == nil {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 会话已失效, 请重新 /add")
		return
	}
	if time.Since(pending.StartedAt) > addBotPendingTTL {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 会话已超时, 请重新 /add")
		return
	}

	adminID, username, err := h.parseUserInput(message.Text)
	if err != nil {
		h.setAddBotPending(message.From.ID, pending.Token)
		h.notifySvc.SendMessage(message.Chat.ID,
			fmt.Sprintf("❌ 无法识别管理员: %v\n\n请发送数字 ID 或 @username, 或 /cancel 取消", err))
		return
	}
	if adminID == 0 && username != "" {
		if u, qerr := h.userRepo.GetByUsername(username); qerr == nil && u != nil {
			adminID = u.ID
		} else if u, lerr := h.lookupUserByUsername(username); lerr == nil && u != nil {
			adminID = u.ID
		}
	}
	if adminID == 0 {
		h.setAddBotPending(message.From.ID, pending.Token)
		h.notifySvc.SendMessage(message.Chat.ID,
			"❌ 无法解析管理员 ID, 请直接发送数字 ID, 或 /cancel 取消")
		return
	}

	if h.registry == nil {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 系统注册表未就绪")
		return
	}

	summary, err := h.registry.SpawnClone(pending.Token, adminID, message.From.ID)
	if err != nil {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID,
			fmt.Sprintf("❌ 启动克隆 Bot 失败: %v", err))
		return
	}

	h.ClearSessionState(message.From.ID)
	resp := fmt.Sprintf(
		"✅ 克隆 Bot 已启动\n\n"+
			"Bot: @%s (id=<code>%d</code>)\n"+
			"管理员: <code>%d</code>\n\n"+
			"该 Bot 拥有独立数据库与缓存。\n"+
			"管理员可通过私聊 /admin 绑定工作群与播报频道。",
		htmlEscape(summary.BotUsername), summary.BotUserID, summary.LocalAdminID,
	)
	h.notifySvc.SendMessage(message.Chat.ID, resp)
}

// lookupUserByUsername 兜底:通过 Telegram getChat 查询用户名对应的 ID
func (h *Handler) lookupUserByUsername(username string) (*telego.ChatFullInfo, error) {
	uname := strings.TrimPrefix(username, "@")
	if uname == "" {
		return nil, fmt.Errorf("空用户名")
	}
	chat, err := h.bot.GetChat(context.Background(), &telego.GetChatParams{
		ChatID: telego.ChatID{Username: "@" + uname},
	})
	if err != nil {
		return nil, err
	}
	return chat, nil
}

// extractForwardedChatID 从转发消息或纯文本 chat_id 中提取目标 chat 的 ID 与类型
//
// 支持以下三种输入:
//  1. 转发频道消息 -> MessageOriginChannel (拿到 chat.id 与 chat.title)
//  2. 转发群组里的"匿名管理员"发言 -> MessageOriginChat
//  3. 直接发送一段纯数字(可带负号) chat_id, 例如 -1001234567890
//
// Telegram 协议下转发"普通群成员的消息"只暴露 user, 不暴露 group, 因此普通群组只能用方式 3
func (h *Handler) extractForwardedChatID(message *telego.Message) (int64, string, string, bool) {
	if message == nil {
		return 0, "", "", false
	}
	if message.ForwardOrigin != nil {
		switch origin := message.ForwardOrigin.(type) {
		case *telego.MessageOriginChannel:
			if origin.Chat.ID != 0 {
				return origin.Chat.ID, "channel", origin.Chat.Title, true
			}
		case *telego.MessageOriginChat:
			if origin.SenderChat.ID != 0 {
				return origin.SenderChat.ID, "chat", origin.SenderChat.Title, true
			}
		case *telego.MessageOriginUser:
			// 用户身份不可作为绑定目标
			return 0, "user", "", false
		}
	}

	// 纯文本 chat_id 输入兜底
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return 0, "", "", false
	}
	if id, err := parseSignedInt64(text); err == nil && id != 0 {
		return id, "id", "", true
	}
	return 0, "", "", false
}

func parseSignedInt64(text string) (int64, error) {
	text = strings.TrimSpace(text)
	if len(text) == 0 {
		return 0, fmt.Errorf("empty")
	}
	var n int64
	_, err := fmt.Sscanf(text, "%d", &n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// handleBindWorkGroupInput 在绑定工作群会话中接收转发消息
func (h *Handler) handleBindWorkGroupInput(message *telego.Message) {
	if !h.IsAuthor(message.From.ID) {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 只有作者可以绑定工作群")
		return
	}
	chatID, kind, title, ok := h.extractForwardedChatID(message)
	if !ok {
		h.notifySvc.SendMessage(message.Chat.ID,
			"❌ 请把工作群的 chat id 直接发给我(如 <code>-1001234567890</code>),\n"+
				"或转发\"匿名管理员\"在该群的发言,或 /cancel 取消\n\n"+
				"提示:转发普通成员消息时 Telegram 不会带出群 ID,只能用 chat id 或匿名管理员消息")
		return
	}
	if kind == "channel" {
		h.notifySvc.SendMessage(message.Chat.ID,
			"⚠️ 转发的是频道消息,工作群应是群组/超级群,请重新发送,或 /cancel 取消")
		return
	}
	if h.settingsRepo == nil {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 配置仓库未就绪")
		return
	}
	if err := h.settingsRepo.SetWorkGroup(chatID); err != nil {
		log.Printf("[绑定] 写入工作群失败: %v", err)
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 写入配置失败,请稍后再试")
		return
	}
	h.ClearSessionState(message.From.ID)
	displayTitle := title
	if displayTitle == "" {
		displayTitle = "(无标题)"
	}
	h.notifySvc.SendMessage(message.Chat.ID,
		fmt.Sprintf("✅ 已绑定工作群: %s (<code>%d</code>)\n\n"+
			"现在 /name /bio /msg /q /no /reload 等指令可在该群使用",
			htmlEscape(displayTitle), chatID))
}

// handleBindBroadcastChatInput 在绑定播报频道会话中接收转发消息
func (h *Handler) handleBindBroadcastChatInput(message *telego.Message) {
	if !h.IsAuthor(message.From.ID) {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 只有作者可以绑定播报频道")
		return
	}
	chatID, kind, title, ok := h.extractForwardedChatID(message)
	if !ok {
		h.notifySvc.SendMessage(message.Chat.ID,
			"❌ 请直接转发\"播报频道\"内的一条消息,\n"+
				"或直接发送 chat id(如 <code>-1001234567890</code>),或 /cancel 取消")
		return
	}
	_ = kind
	if h.settingsRepo == nil {
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 配置仓库未就绪")
		return
	}
	if err := h.settingsRepo.SetBroadcastChat(chatID); err != nil {
		log.Printf("[绑定] 写入播报频道失败: %v", err)
		h.ClearSessionState(message.From.ID)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 写入配置失败,请稍后再试")
		return
	}
	h.ClearSessionState(message.From.ID)
	displayTitle := title
	if displayTitle == "" {
		displayTitle = "(无标题)"
	}
	h.notifySvc.SendMessage(message.Chat.ID,
		fmt.Sprintf("✅ 已绑定播报频道: %s (<code>%d</code>)\n\n"+
			"后续所有处罚动作都会按格式播报到该频道",
			htmlEscape(displayTitle), chatID))
}

// setAddBotPending 缓存 /add 流程中的 token
func (h *Handler) setAddBotPending(userID int64, token string) {
	h.addBotPendingMu.Lock()
	defer h.addBotPendingMu.Unlock()
	h.addBotPending[userID] = &pendingClone{
		Token:     token,
		StartedAt: time.Now(),
	}
}

func (h *Handler) popAddBotPending(userID int64) *pendingClone {
	h.addBotPendingMu.Lock()
	defer h.addBotPendingMu.Unlock()
	p := h.addBotPending[userID]
	delete(h.addBotPending, userID)
	return p
}

func (h *Handler) clearAddBotPending(userID int64) {
	h.addBotPendingMu.Lock()
	defer h.addBotPendingMu.Unlock()
	delete(h.addBotPending, userID)
}

// renderCloneList 构造主 Bot 的克隆列表面板
func (h *Handler) renderCloneList() (string, *telego.InlineKeyboardMarkup) {
	if !h.isMain || h.registry == nil {
		return "", nil
	}
	clones := h.registry.ListClones()
	if len(clones) == 0 {
		return "🤖 <b>克隆 Bot 列表</b>\n\n暂无克隆,可在私聊使用 /add 添加",
			&telego.InlineKeyboardMarkup{
				InlineKeyboard: [][]telego.InlineKeyboardButton{
					{
						tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_main"),
						tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
					},
				},
			}
	}
	text := fmt.Sprintf("🤖 <b>克隆 Bot 列表</b> · 共 %d 个\n\n", len(clones))
	rows := make([][]telego.InlineKeyboardButton, 0, len(clones)+1)
	for _, c := range clones {
		flag := "✅"
		if !c.Enabled {
			flag = "⏸"
		} else if !c.Running {
			flag = "⚠️"
		}
		text += fmt.Sprintf("%s @%s · <code>%d</code> · 本地管理员 <code>%d</code>\n",
			flag, htmlEscape(c.BotUsername), c.BotUserID, c.LocalAdminID)
		label := "⏸ 停用"
		callback := fmt.Sprintf("clone_disable_%d", c.BotUserID)
		if !c.Enabled {
			label = "▶️ 启用"
			callback = fmt.Sprintf("clone_enable_%d", c.BotUserID)
		}
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(fmt.Sprintf("%s @%s", label, c.BotUsername)).WithCallbackData(callback),
		})
	}
	rows = append(rows, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("🔙 返回").WithCallbackData("admin_main"),
		tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
	})
	return text, &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// renderBindings 显示当前 Bot 的绑定状态
func (h *Handler) renderBindings() string {
	cfg, err := h.settingsRepo.Get()
	if err != nil {
		return "❌ 读取绑定信息失败"
	}
	work := "未绑定"
	if cfg.WorkGroupID != 0 {
		work = fmt.Sprintf("<code>%d</code>", cfg.WorkGroupID)
	}
	broadcast := "未绑定"
	if cfg.BroadcastChatID != 0 {
		broadcast = fmt.Sprintf("<code>%d</code>", cfg.BroadcastChatID)
	}
	return fmt.Sprintf(
		"📌 <b>当前绑定</b>\n\n工作群: %s\n播报频道: %s",
		work, broadcast,
	)
}
