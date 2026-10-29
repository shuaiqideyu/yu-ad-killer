package service

import (
	"context"
	"log"
	"time"

	"telegram-ban-bot/internal/database/repository"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// WorkGroupAutoDeleteTTL 工作群消息自毁时长
const WorkGroupAutoDeleteTTL = 24 * time.Hour

// NotificationService 通知服务
type NotificationService struct {
	bot      *telego.Bot
	settings *repository.BotSettingsRepository
}

func NewNotificationService(bot *telego.Bot, settings *repository.BotSettingsRepository) *NotificationService {
	return &NotificationService{
		bot:      bot,
		settings: settings,
	}
}

// SendMessage 发送普通消息;若目标为工作群则该条机器人消息 24 小时后自毁
func (s *NotificationService) SendMessage(chatID int64, text string) error {
	msg, err := s.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:    tu.ID(chatID),
		Text:      text,
		ParseMode: "HTML",
	})
	if err != nil {
		return err
	}
	s.MaybeScheduleWorkGroupDelete(chatID, msg.MessageID)
	return nil
}

// SendMessageWithKeyboard 发送带键盘的消息
func (s *NotificationService) SendMessageWithKeyboard(chatID int64, text string, keyboard *telego.InlineKeyboardMarkup) (*telego.Message, error) {
	msg, err := s.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatID),
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   "HTML",
	})
	if err != nil {
		return nil, err
	}
	s.MaybeScheduleWorkGroupDelete(chatID, msg.MessageID)
	return msg, nil
}

// MaybeScheduleWorkGroupDelete 仅删除机器人自己发出的工作群消息;调用方必须传入本 bot 刚发送成功的 messageID
func (s *NotificationService) MaybeScheduleWorkGroupDelete(chatID int64, messageID int) {
	if s == nil || s.bot == nil || chatID == 0 || messageID <= 0 {
		return
	}
	if !s.isWorkGroup(chatID) {
		return
	}
	s.scheduleDelete(chatID, messageID, WorkGroupAutoDeleteTTL)
}

func (s *NotificationService) isWorkGroup(chatID int64) bool {
	if s.settings == nil {
		return false
	}
	cfg, err := s.settings.Get()
	if err != nil || cfg == nil || cfg.WorkGroupID == 0 {
		return false
	}
	return cfg.WorkGroupID == chatID
}

func (s *NotificationService) scheduleDelete(chatID int64, messageID int, after time.Duration) {
	time.AfterFunc(after, func() {
		if err := s.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
			ChatID:    tu.ID(chatID),
			MessageID: messageID,
		}); err != nil {
			log.Printf("[通知] 工作群消息自毁失败 chat=%d msg=%d err=%v", chatID, messageID, err)
		}
	})
}
