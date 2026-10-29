package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/config"
	"telegram-ban-bot/internal/database/repository"
	"telegram-ban-bot/internal/detector"
	"telegram-ban-bot/internal/service"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Handler 处理器
type Handler struct {
	bot *telego.Bot
	cfg *config.Config

	// 当前 Bot 是否为主 Bot(决定是否处理 /add 与克隆 Bot 列表面板)
	isMain bool

	// Services
	banService   *service.BanService
	keywordSvc   *service.KeywordService
	userSvc      *service.UserService
	notifySvc    *service.NotificationService
	adminSvc     *service.AdminService
	broadcastSvc *service.BroadcastService
	whitelistSvc *service.WhitelistService

	// Detector
	detector *detector.DetectorManager

	// Repositories
	userRepo     *repository.UserRepository
	keywordRepo  *repository.KeywordRepository
	settingsRepo *repository.BotSettingsRepository
	cloneBotRepo *repository.CloneBotRepository // 仅主 Bot 非空

	// 多 Bot 注册表(全局超管 + 克隆生命周期管理)
	registry BotRegistry

	// Cache
	cache *cache.CacheService

	// 用户信息收集去重(缓存最近收集的用户,避免重复)
	userCollectCache map[int64]userCollectEntry
	userCollectMu    sync.RWMutex

	// 会话状态管理(用于对话模式)
	sessionStates map[int64]*SessionState
	sessionMu     sync.RWMutex

	// 添加克隆 Bot 时的临时数据(token 临存)
	addBotPending   map[int64]*pendingClone
	addBotPendingMu sync.Mutex

	// 重复广告检测缓存(群+文本 -> 最近发送记录)
	repeatedAdMessages map[repeatedAdKey][]repeatedAdMessage
	repeatedAdMu       sync.Mutex

	// 单用户重复消息检测缓存
	userRecentMessages   map[userChatKey][]userDuplicateMessage
	userConsecutiveState map[userChatKey]userConsecutiveMessages
	userDuplicateMu      sync.Mutex

	// 进群检测去重(避免"加入群组"系统消息与 chat_member 更新对同一次进群重复处置)
	recentJoins   map[userChatKey]time.Time
	recentJoinsMu sync.Mutex

	// 并发转发计数(同一群同一用户的短时转发)
	forwardBursts  map[userChatKey][]forwardBurstHit
	forwardBurstMu sync.Mutex

	// 自动处罚群提醒 messageID -> 被处罚用户ID,供管理员引用回复 /un 快速解除
	moderationNotices   map[noticeKey]moderationNotice
	moderationNoticesMu sync.Mutex

	// goroutine 生命周期控制
	stopCh chan struct{}
}

type noticeKey struct {
	ChatID    int64
	MessageID int
}

type moderationNotice struct {
	UserID    int64
	CreatedAt time.Time
}

// pendingClone /add 流程中临时缓存的 token
type pendingClone struct {
	Token     string
	StartedAt time.Time
}

// SessionState 会话状态
type SessionState struct {
	State    string    // "adding_admin", "normal"
	ExpireAt time.Time // 过期时间
}

func NewHandler(
	bot *telego.Bot,
	cfg *config.Config,
	banService *service.BanService,
	keywordSvc *service.KeywordService,
	userSvc *service.UserService,
	notifySvc *service.NotificationService,
	adminSvc *service.AdminService,
	broadcastSvc *service.BroadcastService,
	whitelistSvc *service.WhitelistService,
	detectorMgr *detector.DetectorManager,
	userRepo *repository.UserRepository,
	keywordRepo *repository.KeywordRepository,
	settingsRepo *repository.BotSettingsRepository,
	cloneBotRepo *repository.CloneBotRepository,
	cache *cache.CacheService,
	isMain bool,
	registry BotRegistry,
) *Handler {
	h := &Handler{
		bot:                  bot,
		cfg:                  cfg,
		isMain:               isMain,
		banService:           banService,
		keywordSvc:           keywordSvc,
		userSvc:              userSvc,
		notifySvc:            notifySvc,
		adminSvc:             adminSvc,
		broadcastSvc:         broadcastSvc,
		whitelistSvc:         whitelistSvc,
		detector:             detectorMgr,
		userRepo:             userRepo,
		keywordRepo:          keywordRepo,
		settingsRepo:         settingsRepo,
		cloneBotRepo:         cloneBotRepo,
		registry:             registry,
		cache:                cache,
		userCollectCache:     make(map[int64]userCollectEntry),
		sessionStates:        make(map[int64]*SessionState),
		addBotPending:        make(map[int64]*pendingClone),
		repeatedAdMessages:   make(map[repeatedAdKey][]repeatedAdMessage),
		userRecentMessages:   make(map[userChatKey][]userDuplicateMessage),
		userConsecutiveState: make(map[userChatKey]userConsecutiveMessages),
		recentJoins:          make(map[userChatKey]time.Time),
		forwardBursts:        make(map[userChatKey][]forwardBurstHit),
		moderationNotices:    make(map[noticeKey]moderationNotice),
		stopCh:               make(chan struct{}),
	}

	go h.cleanUserCollectCache()
	go h.cleanSessionStates()
	go h.cleanRepeatedAdMessages()
	go h.cleanUserDuplicateMessages()
	go h.cleanRecentJoins()
	go h.cleanForwardBursts()
	go h.cleanModerationNotices()

	return h
}

// Stop 停止 Handler 内部所有清理 goroutine
func (h *Handler) Stop() {
	select {
	case <-h.stopCh:
	default:
		close(h.stopCh)
	}
}

const (
	// 资料没变时，最多 12 小时补一次活跃时间，保证 30 天清理不会误删还在说话的人。
	userActivityRefresh = 12 * time.Hour
	userCollectEntryTTL = 36 * time.Hour
)

type userCollectEntry struct {
	Hash    string
	Touched time.Time
}

// cleanUserCollectCache 删掉长时间没发言的收集缓存，不一次性清空，避免活跃用户扎堆写库。
func (h *Handler) cleanUserCollectCache() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-userCollectEntryTTL)
			h.userCollectMu.Lock()
			for userID, entry := range h.userCollectCache {
				if entry.Touched.Before(cutoff) {
					delete(h.userCollectCache, userID)
				}
			}
			h.userCollectMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}

// IsAdmin 检查是否为管理员(本地 admins 表 + 主 Bot 全局超管)
func (h *Handler) IsAdmin(userID int64) bool {
	if h.adminSvc.IsAdmin(userID) {
		return true
	}
	if h.registry != nil && h.registry.IsGlobalRoot(userID) {
		return true
	}
	return false
}

// isChatTelegramAdminMessage 判断该群消息发送者是否为该群 Telegram 管理员/群主(含匿名管理员)
//
// 用途: 自动检测路径(关键词/刷屏/水群/协议广告/并发转发等)对群管理员消息一律放行,
// 避免误删/误踢群组真实管理员。仅对群组类型生效;私聊/频道不进入此函数。
//
// 识别规则:
//  1. message.SenderChat.ID == message.Chat.ID: 以群身份匿名发言(匿名管理员/群主)
//  2. message.From.ID 落在 getChatAdministrators 返回集合内
//
// 性能: 管理员列表用 chatAdmins 缓存 5 分钟,过期才重新拉 API;
// 取列表失败时按受保护处理，宁可暂时漏检普通消息，也不误删管理员消息。
func (h *Handler) isChatTelegramAdminMessage(message *telego.Message) bool {
	if message == nil {
		return false
	}
	// Bot 自身配置的管理员/作者即使没有 Telegram 群管身份，也不参与自动清理。
	if message.From != nil && h.IsAdmin(message.From.ID) {
		return true
	}
	// 匿名管理员/群主以群身份发言: SenderChat 等于当前 chat
	if message.SenderChat != nil && message.SenderChat.ID == message.Chat.ID {
		return true
	}
	if message.From == nil {
		return false
	}
	if h.isChatTelegramAdminUser(&message.Chat, message.From.ID) {
		return true
	}

	// 某些群或 Bot API 状态下，管理员列表可能漏掉其他管理员 Bot。
	// 对 Bot 单点复核，确保带管理员身份的业务 Bot 消息永不进入自动删除链路。
	if message.From.IsBot && h.isUserChatAdminViaAPI(message.Chat.ID, message.From.ID) {
		h.cache.AddChatAdmin(message.Chat.ID, message.From.ID)
		return true
	}
	return false
}

// isChatTelegramAdminUser 给定 chat + userID 判断是否为该群 Telegram 管理员/群主
// 用于 guest bot 召唤者溯源等需要根据外部 user 判定的场景
func (h *Handler) isChatTelegramAdminUser(chat *telego.Chat, userID int64) bool {
	if chat == nil || chat.ID == 0 || userID == 0 {
		return false
	}
	if chat.Type != telego.ChatTypeGroup && chat.Type != telego.ChatTypeSupergroup {
		return false
	}

	if isAdmin, known := h.cache.IsChatAdmin(chat.ID, userID); known {
		return isAdmin
	}
	admins := h.loadChatAdmins(chat.ID)
	if admins == nil {
		// 无法确认身份时失败关闭，避免 Telegram API 短暂异常导致管理员消息被误删。
		return true
	}
	_, isAdmin := admins[userID]
	return isAdmin
}

// loadChatAdmins 调用 Telegram API 拉取群组管理员并写入缓存;失败时返回 nil
//
// ReturnBots=true 至关重要: Telegram 默认只返回真人管理员 + 当前 bot 自己,
// 会过滤掉其他被设为群管的 bot(例如 @gb28bot/@xx28bot 等业务监控 bot)。
// 不显式开启会导致这些 bot 管理员发的消息走到 detector 路径被误删/误踢。
func (h *Handler) loadChatAdmins(chatID int64) map[int64]struct{} {
	members, err := h.bot.GetChatAdministrators(context.Background(), &telego.GetChatAdministratorsParams{
		ChatID:     tu.ID(chatID),
		ReturnBots: true,
	})
	if err != nil {
		log.Printf("[白名单] 获取群组管理员失败 chat=%d err=%v", chatID, err)
		return nil
	}
	ids := make(map[int64]struct{}, len(members))
	for _, member := range members {
		if member == nil {
			continue
		}
		user := member.MemberUser()
		if user.ID != 0 {
			ids[user.ID] = struct{}{}
		}
	}
	h.cache.SetChatAdmins(chatID, ids)
	return ids
}

// kickUserFromChat "只踢不封禁": BanChatMember 立即 UnbanChatMember,目标失去本次群成员身份,但仍可被重新邀请/加入。
// 不写 ban_record, 不影响用户在其他群的状态。
// scene 用于日志区分场景(例如 "guest bot 召唤" / "spoiler 遮掩")。
func (h *Handler) kickUserFromChat(chatID, userID int64, scene string) error {
	if err := h.bot.BanChatMember(context.Background(), &telego.BanChatMemberParams{
		ChatID: tu.ID(chatID),
		UserID: userID,
	}); err != nil {
		log.Printf("[%s] 踢出 BanChatMember 失败 chat=%d user=%d err=%v", scene, chatID, userID, err)
		return err
	}
	if err := h.bot.UnbanChatMember(context.Background(), &telego.UnbanChatMemberParams{
		ChatID: tu.ID(chatID),
		UserID: userID,
	}); err != nil {
		// 已经 ban 成功,unban 失败只是"被永封"而非"被踢",记日志即可
		log.Printf("[%s] 踢出后 UnbanChatMember 失败(已 ban 成功) chat=%d user=%d err=%v", scene, chatID, userID, err)
	}
	return nil
}

// banChatMemberHard 仅 BanChatMember 不解封,用于把不该在群里的 bot/账号永久拉黑
// 与 BanService.BanUser 不同:不写 ban_records / users 表,不污染人类用户数据
func (h *Handler) banChatMemberHard(chatID, userID int64, scene string) error {
	if err := h.bot.BanChatMember(context.Background(), &telego.BanChatMemberParams{
		ChatID: tu.ID(chatID),
		UserID: userID,
	}); err != nil {
		log.Printf("[%s] 永久封禁失败 chat=%d user=%d err=%v", scene, chatID, userID, err)
		return err
	}
	return nil
}

// IsAuthor 检查是否为作者(本地 author + 主 Bot 全局超管)
func (h *Handler) IsAuthor(userID int64) bool {
	if h.adminSvc.IsAuthor(userID) {
		return true
	}
	if h.registry != nil && h.registry.IsGlobalRoot(userID) {
		return true
	}
	return false
}

// WorkGroupID 当前 Bot 绑定的工作群 ID;未绑定返回 0
func (h *Handler) WorkGroupID() int64 {
	if h.settingsRepo == nil {
		return 0
	}
	cfg, err := h.settingsRepo.Get()
	if err != nil {
		log.Printf("[处理器] 读取工作群配置失败: %v", err)
		return 0
	}
	return cfg.WorkGroupID
}

// IsWorkGroup 检查是否为已绑定的工作群组(未绑定时永远 false)
func (h *Handler) IsWorkGroup(chatID int64) bool {
	id := h.WorkGroupID()
	if id == 0 {
		return false
	}
	return chatID == id
}

// IsMain 当前 Bot 是否为主 Bot
func (h *Handler) IsMain() bool {
	return h.isMain
}

func (h *Handler) extractMessageText(message *telego.Message) string {
	if message == nil {
		return ""
	}
	text := strings.TrimSpace(message.Text)
	if text != "" {
		return text
	}
	return strings.TrimSpace(message.Caption)
}

const workGroupOriginalTextMaxRunes = 500

// notifyWorkGroupModeration 向工作群发送管理动作通知(操作人/目标/原因/引用原文)
// 消息经 NotificationService 发送后会按 24 小时自毁
func (h *Handler) notifyWorkGroupModeration(
	action string,
	opMessage *telego.Message,
	target *targetUser,
	duration, reason string,
) {
	if h == nil || h.notifySvc == nil || opMessage == nil || target == nil || opMessage.From == nil {
		return
	}
	workGroup := h.WorkGroupID()
	if workGroup == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("🛡 <b>")
	b.WriteString(htmlEscape(action))
	b.WriteString("通知</b>\n\n")
	b.WriteString("操作人: ")
	b.WriteString(formatUserDisplay(opMessage.From.ID, opMessage.From.Username, opMessage.From.FirstName))
	b.WriteString("\n")
	b.WriteString("目标用户: ")
	b.WriteString(target.DisplayName)
	b.WriteString("\n")
	if chatTitle := strings.TrimSpace(opMessage.Chat.Title); chatTitle != "" {
		b.WriteString("所在群组: ")
		b.WriteString(htmlEscape(chatTitle))
		b.WriteString(fmt.Sprintf(" (<code>%d</code>)\n", opMessage.Chat.ID))
	}
	if d := strings.TrimSpace(duration); d != "" && d != "永久" {
		b.WriteString("时长: ")
		b.WriteString(htmlEscape(d))
		b.WriteString("\n")
	}
	if r := strings.TrimSpace(reason); r != "" {
		b.WriteString("原因: ")
		b.WriteString(htmlEscape(r))
		b.WriteString("\n")
	}
	b.WriteString("时间: ")
	b.WriteString(time.Now().Format("2006-01-02 15:04:05"))
	b.WriteString("\n")

	if opMessage.ReplyToMessage != nil {
		original := h.extractMessageText(opMessage.ReplyToMessage)
		if original == "" {
			b.WriteString("\n原文内容: <i>(图片/贴纸等非文字消息)</i>")
		} else {
			b.WriteString("\n原文内容:\n<blockquote>")
			b.WriteString(htmlEscape(truncateRunes(original, workGroupOriginalTextMaxRunes)))
			b.WriteString("</blockquote>")
		}
	}

	if err := h.notifySvc.SendMessage(workGroup, b.String()); err != nil {
		log.Printf("[工作群通知] 发送失败 action=%s err=%v", action, err)
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func (h *Handler) isSlashCommandMessage(message *telego.Message) bool {
	return strings.HasPrefix(h.extractMessageText(message), "/")
}

func (h *Handler) ScheduleCommandCleanup(message *telego.Message) {
	if message == nil || message.MessageID <= 0 {
		return
	}
	if !h.isSlashCommandMessage(message) {
		return
	}
	if h.isChatTelegramAdminMessage(message) {
		return
	}

	go func(chatID int64, messageID int) {
		time.Sleep(1 * time.Second)
		if err := h.banService.DeleteMessage(chatID, messageID); err != nil {
			log.Printf("[处理器] 自动清理命令消息失败: %v", err)
		}
	}(message.Chat.ID, message.MessageID)
}

// CollectUserInfo 收集用户信息(统一入口,带去重)
func (h *Handler) CollectUserInfo(user *telego.User) {
	if user == nil {
		return
	}

	// 生成用户信息hash用于去重
	userHash := fmt.Sprintf("%d:%s:%s:%s", user.ID, user.Username, user.FirstName, user.LastName)

	now := time.Now()
	h.userCollectMu.RLock()
	cached, exists := h.userCollectCache[user.ID]
	h.userCollectMu.RUnlock()

	if exists && cached.Hash == userHash {
		if now.Sub(cached.Touched) < userActivityRefresh {
			return
		}
		ok, err := h.userSvc.TouchActivity(user.ID)
		if err != nil {
			log.Printf("[UserCollect] 刷新活跃时间失败: ID=%d, Err=%v", user.ID, err)
			return
		}
		if ok {
			h.userCollectMu.Lock()
			if current, ok := h.userCollectCache[user.ID]; ok && current.Hash == userHash {
				current.Touched = now
				h.userCollectCache[user.ID] = current
			}
			h.userCollectMu.Unlock()
			return
		}
	}

	// 收集用户信息
	err := h.userSvc.CreateOrUpdateFromUser(user)
	if err != nil {
		log.Printf("[UserCollect] 失败: ID=%d, Err=%v", user.ID, err)
		return
	}

	// 更新缓存
	h.userCollectMu.Lock()
	h.userCollectCache[user.ID] = userCollectEntry{Hash: userHash, Touched: now}
	h.userCollectMu.Unlock()

	// 精简日志输出
	name := user.FirstName
	if user.LastName != "" {
		name += " " + user.LastName
	}
	username := user.Username
	if username == "" {
		username = "无"
	}
	log.Printf("[UserCollect] ID=%d | @%s | %s", user.ID, username, name)
}
