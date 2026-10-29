package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/pkg/utils"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// HandleAdminMessage 处理管理员私信消息
func (h *Handler) HandleAdminMessage(message *telego.Message) {
	log.Printf("[Admin] 收到管理员消息: ChatType=%s, Text=%s, UsersShared=%v, ForwardOrigin=%v",
		message.Chat.Type, message.Text, message.UsersShared != nil, message.ForwardOrigin != nil)

	if !h.IsAdmin(message.From.ID) {
		log.Printf("[Admin] 非管理员消息,忽略")
		return
	}

	// 只处理私聊
	if message.Chat.Type != telego.ChatTypePrivate {
		log.Printf("[Admin] 非私聊消息,忽略")
		return
	}

	// 优先分发新的会话状态: 添加克隆 Bot / 绑定工作群 / 绑定播报频道
	if sessionState, exists := h.GetSessionState(message.From.ID); exists {
		switch sessionState {
		case sessionAddingBotToken:
			if message.Text == "/cancel" || message.Text == "取消" {
				h.handleCancelCommand(message)
				return
			}
			h.handleAddBotTokenInput(message)
			return
		case sessionAddingBotAdmin:
			if message.Text == "/cancel" || message.Text == "取消" {
				h.handleCancelCommand(message)
				return
			}
			h.handleAddBotAdminInput(message)
			return
		case sessionBindingWork:
			if message.Text == "/cancel" || message.Text == "取消" {
				h.handleCancelCommand(message)
				return
			}
			h.handleBindWorkGroupInput(message)
			return
		case sessionBindingBroadcast:
			if message.Text == "/cancel" || message.Text == "取消" {
				h.handleCancelCommand(message)
				return
			}
			h.handleBindBroadcastChatInput(message)
			return
		}
	}

	// 检查是否在添加管理员的对话模式
	if sessionState, exists := h.GetSessionState(message.From.ID); exists && sessionState == "adding_admin" {
		// 处理取消操作
		if message.Text == "/cancel" || message.Text == "取消" {
			h.ClearSessionState(message.From.ID)
			h.notifySvc.SendMessage(message.Chat.ID, "❌ 已取消添加管理员")
			return
		}

		// 解析用户输入
		userID, username, err := h.parseUserInput(message.Text)
		if err != nil {
			h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 无法识别用户: %v\n\n请发送:\n• 用户ID(纯数字)\n• 用户名(@username或username)\n• 或发送 /cancel 取消", err))
			return
		}

		// 如果只有用户名,从数据库查询用户ID
		if userID == 0 && username != "" {
			user, err := h.userRepo.GetByUsername(username)
			if err != nil {
				h.notifySvc.SendMessage(message.Chat.ID, fmt.Sprintf("❌ 无法找到该用户\n\n数据库中没有用户名 @%s 的记录\n请确认用户名正确,或直接发送用户ID", username))
				return
			}
			userID = user.ID
		}

		// 验证userID是否有效
		if userID == 0 {
			h.notifySvc.SendMessage(message.Chat.ID, "❌ 无效的用户ID")
			return
		}

		// 显示角色选择界面
		h.showAdminRoleSelection(message.Chat.ID, userID)
		return
	}

	// 识别用户
	userID, username, err := h.identifyUser(message)
	if err != nil {
		log.Printf("[Admin] 识别用户失败: %v", err)
		h.notifySvc.SendMessage(message.Chat.ID, "❌ 无法识别用户\n\n支持的方式:\n1. 点击\"选择用户\"按钮\n2. 转发用户消息\n3. 发送用户ID(纯数字)\n4. 发送用户名(@username)\n5. 发送用户链接(t.me/username)")
		return
	}

	if userID == 0 && username == "" {
		log.Printf("[Admin] 未识别到用户信息")
		return
	}

	log.Printf("[Admin] 识别成功: userID=%d, username=%s", userID, username)

	// 显示用户管理界面
	h.showUserManagement(message.Chat.ID, userID, username)
}

// identifyUser 识别用户身份
func (h *Handler) identifyUser(message *telego.Message) (int64, string, error) {
	// 1. 检测 UsersShared
	if message.UsersShared != nil && len(message.UsersShared.Users) > 0 {
		userID := message.UsersShared.Users[0].UserID
		log.Printf("[Admin] User selected via UsersShared: %d", userID)
		return userID, "", nil
	}

	// 2. 检测转发消息
	if message.ForwardOrigin != nil {
		if senderUser, ok := message.ForwardOrigin.(*telego.MessageOriginUser); ok {
			userID := senderUser.SenderUser.ID
			log.Printf("[Admin] User identified via ForwardOrigin: %d", userID)
			return userID, "", nil
		}
	}

	// 3. 检测纯数字(用户ID)
	if userID, ok := utils.ParseUserID(message.Text); ok {
		log.Printf("[Admin] User identified via ID: %d", userID)
		return userID, "", nil
	}

	// 4. 检测用户名
	if username, ok := utils.ParseUsername(message.Text); ok {
		log.Printf("[Admin] User identified via username: @%s", username)
		return 0, username, nil
	}

	// 5. 检测用户链接
	if username, ok := utils.ParseUserLink(message.Text); ok {
		log.Printf("[Admin] User identified via link: @%s", username)
		return 0, username, nil
	}

	return 0, "", fmt.Errorf("cannot identify user")
}

// showUserManagement 显示用户管理界面
func (h *Handler) showUserManagement(chatID int64, userID int64, username string) {
	// 只有用户名时: 优先从本地数据库解析 ID(/lh、/f、关键词命中等场景都会收录用户),
	// 数据库没有再回退到 Telegram API getChat,避免 bot 从未接触过该用户时直接报错.
	if userID == 0 && username != "" {
		uname := strings.TrimPrefix(username, "@")
		if u, err := h.userRepo.GetByUsername(uname); err == nil && u != nil {
			userID = u.ID
		} else {
			chat, apiErr := h.bot.GetChat(context.Background(), &telego.GetChatParams{
				ChatID: telego.ChatID{Username: "@" + uname},
			})
			if apiErr != nil || chat == nil || chat.ID == 0 {
				log.Printf("[Admin] 通过用户名 @%s 查询用户失败: dbErr=%v apiErr=%v", uname, err, apiErr)
				h.notifySvc.SendMessage(chatID, fmt.Sprintf(
					"❌ 无法找到用户 @%s\n\n• 请确认用户名拼写正确\n• 或改用用户ID/转发用户消息查询",
					uname,
				))
				return
			}
			userID = chat.ID
			// 顺手把新用户收录进本地数据库,后续就能直接命中
			h.userSvc.CreateOrUpdateFromChatFullInfo(chat)
		}
	}

	// 获取用户状态(直接查询数据库,无需清除缓存)
	user, record, err := h.banService.GetUserStatus(userID)
	if err != nil {
		// 用户不存在数据库中,尝试从Telegram获取并存储
		chat, err := h.bot.GetChat(context.Background(), &telego.GetChatParams{
			ChatID: telego.ChatID{ID: userID},
		})
		if err == nil {
			h.userSvc.CreateOrUpdateFromChatFullInfo(chat)
			// 重新查询
			user, record, _ = h.banService.GetUserStatus(userID)
		}
	}

	// 查询操作人信息(如果有封禁记录)
	var operatorUser *models.User
	if record != nil && record.BannedBy != 0 {
		operatorUser, _ = h.userRepo.GetByID(record.BannedBy)
	}

	// 使用统一的格式化函数构建消息
	text := utils.FormatUserInfoForPrivateChat(user, record, operatorUser)

	// 构建按钮(添加刷新按钮)
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

	h.notifySvc.SendMessageWithKeyboard(chatID, text, keyboard)
}

// formatUsername 格式化用户名
func formatUsername(username string) string {
	if username == "" {
		return "无"
	}
	return "@" + username
}

// parseUserInput 解析用户输入的ID或用户名
func (h *Handler) parseUserInput(text string) (int64, string, error) {
	if text == "" {
		return 0, "", fmt.Errorf("输入为空")
	}

	// 尝试解析为用户ID
	if userID, ok := utils.ParseUserID(text); ok {
		return userID, "", nil
	}

	// 尝试解析为用户名
	if username, ok := utils.ParseUsername(text); ok {
		return 0, username, nil
	}

	// 尝试解析为用户链接
	if username, ok := utils.ParseUserLink(text); ok {
		return 0, username, nil
	}

	return 0, "", fmt.Errorf("无法识别的格式")
}

// showAdminRoleSelection 显示管理员角色选择界面
func (h *Handler) showAdminRoleSelection(chatID int64, userID int64) {
	if !h.IsAuthor(chatID) {
		h.notifySvc.SendMessage(chatID, "❌ 只有作者可以添加管理员")
		return
	}

	if h.IsAdmin(userID) {
		h.notifySvc.SendMessage(chatID, "❌ 该用户已是管理员")
		return
	}

	// 尝试从数据库获取用户信息
	user, _ := h.userRepo.GetByID(userID)

	// 如果数据库中没有,尝试从Telegram获取并存储
	if user == nil {
		chat, err := h.bot.GetChat(context.Background(), &telego.GetChatParams{
			ChatID: telego.ChatID{ID: userID},
		})
		if err == nil {
			h.userSvc.CreateOrUpdateFromChatFullInfo(chat)
			user, _ = h.userRepo.GetByID(userID)
		}
	}

	userInfo := fmt.Sprintf("ID: %d", userID)
	if user != nil {
		if user.Username != "" {
			userInfo = fmt.Sprintf("@%s (ID: %d)", user.Username, userID)
		} else if user.FirstName != "" {
			userInfo = fmt.Sprintf("%s (ID: %d)", user.FirstName, userID)
		}
	}

	text := fmt.Sprintf("➕ 添加管理员\n\n选择管理员角色:\n\n用户: %s", userInfo)

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				tu.InlineKeyboardButton("👤 配置管理员").WithCallbackData(fmt.Sprintf("admin_role_config_%d", userID)),
				tu.InlineKeyboardButton("👑 作者").WithCallbackData(fmt.Sprintf("admin_role_author_%d", userID)),
			},
			{
				tu.InlineKeyboardButton("❌ 取消").WithCallbackData("admin_admins"),
			},
		},
	}

	h.notifySvc.SendMessageWithKeyboard(chatID, text, keyboard)
}
