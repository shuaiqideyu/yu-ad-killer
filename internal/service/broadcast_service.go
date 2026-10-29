package service

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"telegram-ban-bot/internal/database/repository"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BanBroadcast 拉黑/禁言/踢人等处罚动作的播报输入
type BanBroadcast struct {
	// Chat 触发动作所在的群(用于显示群名/链接);可空(空时显示"未知群组")
	Chat *telego.Chat

	// 目标用户信息
	UserID   int64
	UserName string // 显示用全名(已合并 FirstName + LastName)
	Username string // @username,可空

	// Action 动作中文描述,用于标题前缀(默认"拉黑")
	Action string

	// Duration 时长(永久/30s/10m/2h/1d 等)
	Duration string

	// Reason 处罚原因
	Reason string

	// MessageText 触发消息/被回复原文(可空)
	MessageText string

	// OperatorID >0 表示具体管理员;==0 表示系统自动检测
	OperatorID int64
	// OperatorOverride 显式指定操作人显示名(优先于 OperatorID 查询)
	OperatorOverride string

	// At 操作时间(默认 time.Now())
	At time.Time
}

// BroadcastService 单 Bot 的播报服务,未绑定播报频道时静默
type BroadcastService struct {
	bot      *telego.Bot
	settings *repository.BotSettingsRepository
	userRepo *repository.UserRepository
}

func NewBroadcastService(
	bot *telego.Bot,
	settings *repository.BotSettingsRepository,
	userRepo *repository.UserRepository,
) *BroadcastService {
	return &BroadcastService{
		bot:      bot,
		settings: settings,
		userRepo: userRepo,
	}
}

// SendBan 发送拉黑/禁言/踢出/封禁等播报到当前 Bot 绑定的播报频道
// 若未绑定播报频道, 直接 return nil
func (s *BroadcastService) SendBan(b BanBroadcast) error {
	if s == nil || s.settings == nil {
		return nil
	}
	cfg, err := s.settings.Get()
	if err != nil {
		return fmt.Errorf("读取播报配置失败: %w", err)
	}
	if cfg.BroadcastChatID == 0 {
		return nil
	}

	if b.At.IsZero() {
		b.At = time.Now()
	}
	action := strings.TrimSpace(b.Action)
	if action == "" {
		action = "拉黑"
	}
	duration := strings.TrimSpace(b.Duration)
	reason := strings.TrimSpace(b.Reason)

	text := fmt.Sprintf("🚫 %s通知\n\n", html.EscapeString(action))
	text += "群组：" + s.formatGroup(b.Chat) + "\n"
	text += "用户：" + html.EscapeString(s.formatUserName(b)) + "\n"
	text += fmt.Sprintf("ID：<code>%d</code>\n", b.UserID)
	// 永久 / 空时长、未标注具体理由时不占行,避免通知噪音
	if duration != "" && duration != "永久" {
		text += "时长：" + html.EscapeString(duration) + "\n"
	}
	if reason != "" && reason != "未填写" {
		text += "理由：" + html.EscapeString(reason) + "\n"
	}
	if msg := strings.TrimSpace(b.MessageText); msg != "" {
		if len([]rune(msg)) > 500 {
			msg = string([]rune(msg)[:500]) + "…"
		}
		text += "原文：" + html.EscapeString(msg) + "\n"
	}
	text += "操作时间：" + b.At.Format("2006-01-02 15:04:05") + "\n"
	text += "操作人：" + html.EscapeString(s.formatOperator(b))

	_, err = s.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:             tu.ID(cfg.BroadcastChatID),
		Text:               text,
		ParseMode:          "HTML",
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		log.Printf("[播报] 发送失败 chat=%d err=%v", cfg.BroadcastChatID, err)
		return err
	}
	return nil
}

// SendBanAsync 异步发送, 失败仅记录日志, 不影响调用方流程
func (s *BroadcastService) SendBanAsync(b BanBroadcast) {
	if s == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[播报] panic: %v", r)
			}
		}()
		if err := s.SendBan(b); err != nil {
			log.Printf("[播报] 错误: %v", err)
		}
	}()
}

// formatGroup 群组段:<title> @<username> (https://t.me/<username>)
// 私有群无 username 时仅展示 title 与 chat id
func (s *BroadcastService) formatGroup(chat *telego.Chat) string {
	if chat == nil {
		return "未知群组"
	}
	title := strings.TrimSpace(chat.Title)
	if title == "" {
		title = "未命名群组"
	}
	parts := []string{html.EscapeString(title)}
	if chat.Username != "" {
		parts = append(parts, "@"+html.EscapeString(chat.Username))
		parts = append(parts, fmt.Sprintf("(https://t.me/%s)", chat.Username))
	} else {
		parts = append(parts, fmt.Sprintf("(<code>%d</code>)", chat.ID))
	}
	return strings.Join(parts, " ")
}

func (s *BroadcastService) formatUserName(b BanBroadcast) string {
	name := strings.TrimSpace(b.UserName)
	if name != "" {
		if b.Username != "" {
			name += " @" + b.Username
		}
		return name
	}
	if b.Username != "" {
		return "@" + b.Username
	}
	return fmt.Sprintf("用户 %d", b.UserID)
}

func (s *BroadcastService) formatOperator(b BanBroadcast) string {
	if strings.TrimSpace(b.OperatorOverride) != "" {
		return b.OperatorOverride
	}
	if b.OperatorID == 0 {
		return "系统自动"
	}
	if s.userRepo == nil {
		return fmt.Sprintf("管理员 %d", b.OperatorID)
	}
	u, err := s.userRepo.GetByID(b.OperatorID)
	if err != nil || u == nil {
		return fmt.Sprintf("管理员 %d", b.OperatorID)
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if u.Username != "" {
		if name != "" {
			name += " "
		}
		name += "@" + u.Username
	}
	if name == "" {
		name = fmt.Sprintf("管理员 %d", b.OperatorID)
	}
	return name
}
