package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BanSyncFunc 封禁/解封同步回调; action="ban"|"unban"
type BanSyncFunc func(action string, userID int64, reason string)

// BanService 封禁服务
type BanService struct {
	bot          *telego.Bot
	userRepo     *repository.UserRepository
	banRepo      *repository.BanRepository
	cache        *cache.CacheService
	peerSync     BanSyncFunc
	managedChats sync.Map // chatID(int64) -> struct{}{}, 追踪所有管理群组
}

func NewBanService(
	bot *telego.Bot,
	userRepo *repository.UserRepository,
	banRepo *repository.BanRepository,
	cacheService *cache.CacheService,
) *BanService {
	return &BanService{
		bot:      bot,
		userRepo: userRepo,
		banRepo:  banRepo,
		cache:    cacheService,
	}
}

// BanUser 封禁用户(先封禁数据库,再删除消息,最后踢出群组)
// skipKick: true表示只更新数据库不踢出(批量封禁时使用),false表示立即踢出(实时检测时使用)
func (s *BanService) BanUser(chatID int64, userID int64, banType models.BanType, reason, keyword, messageText string, bannedBy int64, messageID int, skipKick bool) error {
	// 幂等: 已封禁用户只补踢不重复入库
	if already, _ := s.IsBanned(userID); already {
		if !skipKick && chatID != 0 {
			_ = s.banUserFromChat(chatID, userID)
		}
		if messageID > 0 {
			_ = s.DeleteMessage(chatID, messageID)
		}
		go s.banFromAllChats(userID, chatID)
		return nil
	}

	// 1. 构建详细的封禁原因
	var detailedReason string
	switch banType {
	case models.BanTypeManual, models.BanTypeFCommand:
		// 优先保留调用方传入的业务原因(例如 /lh 后面的"测试"),
		// 仅在缺省时才退化为通用描述,最后统一附加操作人信息.
		operatorName := s.resolveOperatorName(bannedBy)
		baseReason := strings.TrimSpace(reason)
		if baseReason == "" {
			if banType == models.BanTypeFCommand {
				baseReason = "管理员使用 f 命令封杀"
			} else {
				baseReason = "管理员手动封禁"
			}
		}
		detailedReason = fmt.Sprintf("%s (操作人: %s)", baseReason, operatorName)
	case models.BanTypeName:
		if isRussianBanReason(keyword) {
			detailedReason = keyword
		} else {
			detailedReason = fmt.Sprintf("昵称包含关键词: %s", keyword)
		}
	case models.BanTypeBio:
		if isRussianBanReason(keyword) {
			detailedReason = keyword
		} else {
			detailedReason = fmt.Sprintf("简介包含关键词: %s", keyword)
		}
	case models.BanTypeMsg:
		if strings.TrimSpace(reason) != "" && reason != keyword {
			detailedReason = reason
		} else {
			detailedReason = fmt.Sprintf("消息包含关键词: %s", keyword)
		}
	case models.BanTypeInvite:
		detailedReason = fmt.Sprintf("群名包含关键词: %s", keyword)
	case models.BanTypeExternalReply:
		detailedReason = "回复频道消息并附带文字"
	case models.BanTypeContact:
		detailedReason = "发送用户名片(广告号高危行为)"
	default:
		detailedReason = reason
	}

	// 2. 更新数据库封禁状态(最优先,确保用户被标记为封禁)
	if err := s.userRepo.Ban(userID, detailedReason); err != nil {
		return fmt.Errorf("封禁数据库更新失败: %w", err)
	}

	// 3. 添加封禁记录
	record := &models.BanRecord{
		UserID:           userID,
		BanType:          banType,
		TriggeredKeyword: keyword,
		MessageText:      messageText,
		BannedBy:         bannedBy,
		ChatID:           chatID,
	}
	if err := s.banRepo.AddRecord(record); err != nil {
		log.Printf("[封禁服务] 添加封禁记录失败: %v", err)
	}

	// 4. 群组拉黑 + 删除全部消息(RevokeMessages=true 由 Telegram 服务端清理)
	// 注意: Telegram群组ID是负数,使用!=0而不是>0
	if !skipKick && chatID != 0 {
		log.Printf("[封禁服务] 开始拉黑用户: chatID=%d, userID=%d", chatID, userID)
		if err := s.banUserFromChat(chatID, userID); err != nil {
			log.Printf("[封禁服务] 群组拉黑失败(已封禁数据库): chatID=%d, userID=%d, err=%v", chatID, userID, err)
		} else {
			log.Printf("[封禁服务] 已在群组 %d 拉黑用户 %d 并清除全部消息 ✓", chatID, userID)
		}
	}

	// 显式删除触发消息: 不依赖 BanChatMember 的 RevokeMessages
	// (后者需机器人具备"删除消息"权限才生效, 缺权限时会静默失败导致广告原文沉淀在群里)
	if messageID > 0 && chatID != 0 {
		if err := s.DeleteMessage(chatID, messageID); err != nil {
			log.Printf("[封禁服务] 删除触发消息失败: chat=%d message=%d err=%v", chatID, messageID, err)
		}
	}

	// 5. 异步从所有其他管理群踢出 + 删除消息(确保广告不沉积)
	go s.banFromAllChats(userID, chatID)

	log.Printf("[封禁服务] 用户 %d 封禁成功, 类型: %s, 原因: %s", userID, banType, detailedReason)

	if s.peerSync != nil {
		go s.peerSync("ban", userID, detailedReason)
	}

	return nil
}

// resolveOperatorName 查询操作人的可读显示名,格式: "FirstName LastName @Username"
// 查不到或未指定时返回 "管理员".
func (s *BanService) resolveOperatorName(operatorID int64) string {
	if operatorID <= 0 {
		return "管理员"
	}
	operator, err := s.userRepo.GetByID(operatorID)
	if err != nil || operator == nil {
		return "管理员"
	}
	name := operator.FirstName
	if operator.LastName != "" {
		if name != "" {
			name += " "
		}
		name += operator.LastName
	}
	if operator.Username != "" {
		if name != "" {
			name += " "
		}
		name += "@" + operator.Username
	}
	if strings.TrimSpace(name) == "" {
		return "管理员"
	}
	return name
}

// UnbanUser 解封用户(优先从封禁源同步解封,其他群组异步批量解封)
func (s *BanService) UnbanUser(userID int64) error {
	// 1. 获取最新封禁记录,获取封禁源群组
	latestRecord, err := s.banRepo.GetLatestRecord(userID)
	if err != nil {
		log.Printf("[封禁服务] 获取封禁记录失败: %v", err)
	}

	// 2. 如果有封禁源群组,优先从封禁源同步解封
	var sourceChatID int64
	if latestRecord != nil && latestRecord.ChatID != 0 {
		sourceChatID = latestRecord.ChatID
		if err := s.unbanFromChat(sourceChatID, userID); err != nil {
			log.Printf("[封禁服务] 从封禁源群组 %d 解封用户 %d 失败: %v", sourceChatID, userID, err)
		} else {
			log.Printf("[封禁服务] 已从封禁源群组 %d 解封用户 %d", sourceChatID, userID)
		}
	}

	// 3. 更新数据库解封状态
	if err := s.userRepo.Unban(userID); err != nil {
		return fmt.Errorf("failed to unban user: %w", err)
	}

	// 4. 异步批量解封其他群组：同时覆盖历史封禁记录和当前运行期登记的全部管理群。
	go func() {
		s.unbanFromKnownChats(userID, sourceChatID)
	}()

	if s.peerSync != nil {
		go s.peerSync("unban", userID, "")
	}

	log.Printf("[封禁服务] 用户 %d 解封成功", userID)
	return nil
}

// banUserFromChat 从群组拉黑用户(永久封禁,带重试机制)
// RevokeMessages=true: Telegram 服务端删除该用户在群内的全部消息
func (s *BanService) banUserFromChat(chatID, userID int64) error {
	return s.retryOperation(func() error {
		return s.bot.BanChatMember(context.Background(), &telego.BanChatMemberParams{
			ChatID:         tu.ID(chatID),
			UserID:         userID,
			RevokeMessages: true,
		})
	}, "群组拉黑", userID)
}

// unbanFromChat 从单个群组解封用户(带重试机制)
func (s *BanService) unbanFromChat(chatID, userID int64) error {
	return s.retryOperation(func() error {
		return s.bot.UnbanChatMember(context.Background(), &telego.UnbanChatMemberParams{
			ChatID: tu.ID(chatID),
			UserID: userID,
		})
	}, "群组解封", userID)
}

// batchUnbanFromChats 异步批量解封用户(从多个群组)
func (s *BanService) batchUnbanFromChats(userID int64, chatIDs []int64) {
	for _, chatID := range chatIDs {
		// 调用Telegram API解封
		err := s.bot.UnbanChatMember(context.Background(), &telego.UnbanChatMemberParams{
			ChatID: tu.ID(chatID),
			UserID: userID,
		})

		if err != nil {
			// 静默忽略错误(用户不在群组、机器人无权限等正常情况)
			log.Printf("[封禁服务] 从群组 %d 解封用户 %d 失败(忽略): %v", chatID, userID, err)
		} else {
			log.Printf("[封禁服务] 已从群组 %d 解封用户 %d", chatID, userID)
		}

		// API频率控制:每次调用间隔50ms,防止触发429错误
		time.Sleep(50 * time.Millisecond)
	}
	log.Printf("[封禁服务] 批量解封完成,用户 %d,共处理 %d 个群组", userID, len(chatIDs))
}

// unbanFromKnownChats 解封当前 Bot 已知的全部群组：
// 历史封禁记录保证重启后仍可恢复，managedChats 补齐跨群级联封禁但未逐群写记录的群。
func (s *BanService) unbanFromKnownChats(userID, excludeChatID int64) {
	recordedChats, err := s.banRepo.GetUserBanChats(userID)
	if err != nil {
		log.Printf("[封禁服务] 获取用户封禁群组列表失败,继续处理运行期管理群: %v", err)
	}

	chatIDs := mergeChatIDs(excludeChatID, recordedChats, s.managedChatIDs())
	if len(chatIDs) == 0 {
		return
	}
	log.Printf("[封禁服务] 开始批量解封用户 %d,共 %d 个群组", userID, len(chatIDs))
	s.batchUnbanFromChats(userID, chatIDs)
}

func (s *BanService) managedChatIDs() []int64 {
	var chatIDs []int64
	s.managedChats.Range(func(key, _ any) bool {
		chatIDs = append(chatIDs, key.(int64))
		return true
	})
	return chatIDs
}

func mergeChatIDs(excludeChatID int64, groups ...[]int64) []int64 {
	seen := make(map[int64]struct{})
	var merged []int64
	for _, chatIDs := range groups {
		for _, chatID := range chatIDs {
			if chatID == 0 || chatID == excludeChatID {
				continue
			}
			if _, exists := seen[chatID]; exists {
				continue
			}
			seen[chatID] = struct{}{}
			merged = append(merged, chatID)
		}
	}
	return merged
}

// DeleteMessage 删除消息(带重试机制)
func (s *BanService) DeleteMessage(chatID int64, messageID int) error {
	return s.retryOperation(func() error {
		return s.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
			ChatID:    tu.ID(chatID),
			MessageID: messageID,
		})
	}, "删除消息", chatID)
}

// IsBanned 检查用户是否被封禁(直接查询数据库,确保实时性)
func (s *BanService) IsBanned(userID int64) (bool, error) {
	return s.userRepo.IsBanned(userID)
}

// GetUserStatus 获取用户状态
func (s *BanService) GetUserStatus(userID int64) (*models.User, *models.BanRecord, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, nil, err
	}

	var record *models.BanRecord
	if user.IsBanned {
		record, err = s.banRepo.GetLatestRecord(userID)
		if err != nil {
			log.Printf("[封禁服务] 获取封禁记录失败: %v", err)
		}
	}

	return user, record, nil
}

// RecordTempBan 记录一次临时拉黑(只写 BanRecord, 不改 User.IsBanned)
// chatID 发生群, untilDate 到期时间戳(秒), reason 原因文字, operatorID 操作人
func (s *BanService) RecordTempBan(chatID, userID int64, untilUnix int64, reason string, operatorID int64, messageID int, messageText string) error {
	var until *time.Time
	if untilUnix > 0 {
		t := time.Unix(untilUnix, 0)
		until = &t
	}
	record := &models.BanRecord{
		UserID:      userID,
		BanType:     models.BanTypeManual,
		MessageText: messageText,
		BannedBy:    operatorID,
		ChatID:      chatID,
		UntilDate:   until,
		Reason:      reason,
	}
	if err := s.banRepo.AddRecord(record); err != nil {
		return fmt.Errorf("写入临时拉黑记录失败: %w", err)
	}
	if messageID > 0 {
		if err := s.DeleteMessage(chatID, messageID); err != nil {
			log.Printf("[封禁服务] 临时拉黑删除消息失败: %v", err)
		}
	}
	log.Printf("[封禁服务] 已记录临时拉黑 user=%d chat=%d until=%v", userID, chatID, until)
	return nil
}

// RecordMute 记录一次禁言(只写 BanRecord, 不改 User.IsBanned)
// untilUnix=0 表示永久禁言
func (s *BanService) RecordMute(chatID, userID int64, untilUnix int64, reason string, operatorID int64) error {
	var until *time.Time
	if untilUnix > 0 {
		t := time.Unix(untilUnix, 0)
		until = &t
	}
	record := &models.BanRecord{
		UserID:    userID,
		BanType:   models.BanTypeMute,
		BannedBy:  operatorID,
		ChatID:    chatID,
		UntilDate: until,
		Reason:    reason,
	}
	if err := s.banRepo.AddRecord(record); err != nil {
		return fmt.Errorf("写入禁言记录失败: %w", err)
	}
	log.Printf("[封禁服务] 已记录禁言 user=%d chat=%d until=%v", userID, chatID, until)
	return nil
}

// RevokeLatestActiveRecord 将用户最新一条生效中的记录标记为已解除
// banTypes 可选,限定类型;为空则匹配所有类型
func (s *BanService) RevokeLatestActiveRecord(userID int64, banTypes ...models.BanType) error {
	return s.banRepo.RevokeActiveRecords(userID, banTypes...)
}

// GetLatestActiveRecord 查询用户当前生效中的最新处罚记录
func (s *BanService) GetLatestActiveRecord(userID int64, banTypes ...models.BanType) (*models.BanRecord, error) {
	return s.banRepo.GetLatestActiveRecord(userID, banTypes...)
}

// SelfRelease 用户自助解除(综合处理永封/临时拉黑/禁言,共用 self_unban_used)
// 返回已解除的类型: "ban" | "temp_ban" | "mute"
//
// 先判定可解除的处罚,再原子占名额,最后执行 API——避免「无处罚也消耗名额」
// 与「占名额后再检查 SelfUnbanUsed 导致永封永远解不开」。
func (s *BanService) SelfRelease(userID int64) (string, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return "", fmt.Errorf("获取用户信息失败: %w", err)
	}
	if user == nil {
		return "", fmt.Errorf("用户不存在")
	}
	if user.SelfUnbanUsed {
		return "", fmt.Errorf("您已使用过自助解除机会")
	}

	// 1) 永久封禁优先: 占名额后走统一 UnbanUser(历史群 ∪ 运行期管理群)
	if user.IsBanned {
		if err := s.userRepo.MarkSelfUnbanUsed(userID); err != nil {
			return "", fmt.Errorf("您已使用过自助解除机会")
		}
		if err := s.UnbanUser(userID); err != nil {
			return "", err
		}
		log.Printf("[封禁服务] 用户 %d 自助解除永久封禁成功", userID)
		return "ban", nil
	}

	// 2) 临时拉黑 / 禁言
	record, err := s.banRepo.GetLatestActiveRecord(userID,
		models.BanTypeManual, models.BanTypeFCommand, models.BanTypeMute)
	if err != nil {
		return "", fmt.Errorf("查询处罚记录失败: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("您当前未受处罚")
	}

	if err := s.userRepo.MarkSelfUnbanUsed(userID); err != nil {
		return "", fmt.Errorf("您已使用过自助解除机会")
	}

	releaseType := "temp_ban"
	if record.BanType == models.BanTypeMute {
		releaseType = "mute"
		if record.ChatID != 0 {
			if err := s.bot.RestrictChatMember(context.Background(), &telego.RestrictChatMemberParams{
				ChatID:      tu.ID(record.ChatID),
				UserID:      userID,
				Permissions: OpenChatPermissions(),
			}); err != nil {
				log.Printf("[封禁服务] 自助解除禁言 API 失败 chat=%d user=%d: %v", record.ChatID, userID, err)
			}
		}
	} else if record.ChatID != 0 {
		if err := s.unbanFromChat(record.ChatID, userID); err != nil {
			log.Printf("[封禁服务] 自助解除临时拉黑 API 失败 chat=%d user=%d: %v", record.ChatID, userID, err)
		}
	}

	if err := s.banRepo.RevokeActiveRecords(userID, record.BanType); err != nil {
		log.Printf("[封禁服务] 撤销生效记录失败: %v", err)
	}

	log.Printf("[封禁服务] 用户 %d 自助解除成功 type=%s", userID, releaseType)
	return releaseType, nil
}

// OpenChatPermissions 恢复默认发言权限(自助解除 /un /unjy 共用)
func OpenChatPermissions() telego.ChatPermissions {
	t := true
	return telego.ChatPermissions{
		CanSendMessages:       &t,
		CanSendAudios:         &t,
		CanSendDocuments:      &t,
		CanSendPhotos:         &t,
		CanSendVideos:         &t,
		CanSendVideoNotes:     &t,
		CanSendVoiceNotes:     &t,
		CanSendPolls:          &t,
		CanSendOtherMessages:  &t,
		CanAddWebPagePreviews: &t,
	}
}

// retryOperation 重试操作(指数退避);对"消息已不存在/权限不足"等不可恢复错误立即返回,避免空等阻塞 worker
func (s *BanService) retryOperation(operation func() error, operationName string, targetID int64) error {
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := operation()
		if err == nil {
			if attempt > 1 {
				log.Printf("[封禁服务] %s成功(重试%d次): 目标ID=%d", operationName, attempt-1, targetID)
			}
			return nil
		}

		lastErr = err
		if isNonRetryableTelegramError(err) {
			log.Printf("[封禁服务] %s不可重试失败: %v, 目标ID=%d", operationName, err, targetID)
			return fmt.Errorf("%s失败: %w", operationName, lastErr)
		}
		if attempt < maxRetries {
			backoff := time.Duration(attempt) * time.Second
			log.Printf("[封禁服务] %s失败(第%d次尝试): %v, 等待%v后重试", operationName, attempt, err, backoff)
			time.Sleep(backoff)
		}
	}

	log.Printf("[封禁服务] %s最终失败(已重试%d次): %v, 目标ID=%d", operationName, maxRetries, lastErr, targetID)
	return fmt.Errorf("%s失败: %w", operationName, lastErr)
}

func isNonRetryableTelegramError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range []string{
		"message to delete not found",
		"message can't be deleted",
		"message_id_invalid",
		"message identifier is not specified",
		"chat not found",
		"user_not_participant",
		"participant_id_invalid",
		"not enough rights",
		"have no rights",
		"can't remove chat owner",
		"user is an administrator",
		"can't ban",
		"forbidden: bot was kicked",
		"bot is not a member",
	} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// ========== 管理群追踪 ==========

// RegisterChat 注册管理群组(收到群消息时调用,用于封禁时全群踢出)
func (s *BanService) RegisterChat(chatID int64) {
	if chatID != 0 {
		s.managedChats.Store(chatID, struct{}{})
	}
}

// BanFromAllChats 从所有管理群踢出用户并删除全部消息(供跨 Bot 同步使用)
func (s *BanService) BanFromAllChats(userID int64) {
	s.banFromAllChats(userID, 0)
}

// UnbanFromAllChats 从当前 Bot 已知的全部管理群解除用户拉黑(供跨 Bot 同步使用)。
func (s *BanService) UnbanFromAllChats(userID int64) {
	s.unbanFromKnownChats(userID, 0)
}

// banFromAllChats 从所有管理群踢出用户(排除 excludeChat 避免重复调用)
// 每个群调用 BanChatMember+RevokeMessages 由 Telegram 服务端清理全部消息
func (s *BanService) banFromAllChats(userID int64, excludeChat int64) {
	s.managedChats.Range(func(key, _ any) bool {
		chatID := key.(int64)
		if chatID == excludeChat {
			return true
		}
		if err := s.bot.BanChatMember(context.Background(), &telego.BanChatMemberParams{
			ChatID:         tu.ID(chatID),
			UserID:         userID,
			RevokeMessages: true,
		}); err != nil {
			// 静默: bot 不在该群/无权限/用户不在群 等正常情况
		}
		time.Sleep(50 * time.Millisecond)
		return true
	})
}

func isRussianBanReason(keyword string) bool {
	return strings.Contains(keyword, "俄罗斯") || strings.Contains(keyword, "俄语")
}

// ========== 跨 Bot 同步 ==========

// SetPeerSync 注入跨 Bot 同步回调; 由 Runtime 在注册到 Registry 后调用
func (s *BanService) SetPeerSync(fn BanSyncFunc) {
	s.peerSync = fn
}

// MarkBanned 仅更新本地 DB 封禁标记(供跨 Bot 同步接收端使用, 不触发 Telegram API 和 peerSync)
func (s *BanService) MarkBanned(userID int64, reason string) {
	if err := s.userRepo.Ban(userID, reason); err != nil {
		log.Printf("[封禁同步] 标记用户 %d 封禁失败: %v", userID, err)
	}
}

// MarkUnbanned 仅更新本地 DB 解封标记(供跨 Bot 同步接收端使用, 不触发 Telegram API 和 peerSync)
func (s *BanService) MarkUnbanned(userID int64) {
	if err := s.userRepo.Unban(userID); err != nil {
		log.Printf("[封禁同步] 标记用户 %d 解封失败: %v", userID, err)
	}
}
