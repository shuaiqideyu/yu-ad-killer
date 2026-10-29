package repository

import (
	"errors"
	"sync"
	"telegram-ban-bot/internal/database/models"

	"gorm.io/gorm"
)

const botSettingsRowID int64 = 1

// BotSettingsRepository 单 Bot 运行时配置访问层
// 表内固定存 ID=1 一行;读取时若不存在自动返回零值,不报错
type BotSettingsRepository struct {
	db     *gorm.DB
	mu     sync.RWMutex
	cached *models.BotSettings
}

func NewBotSettingsRepository(db *gorm.DB) *BotSettingsRepository {
	return &BotSettingsRepository{db: db}
}

// Get 读取当前配置;表内未初始化时返回空配置(WorkGroupID=0, BroadcastChatID=0)。
// 返回副本，调用方改字段不会污染缓存。
func (r *BotSettingsRepository) Get() (*models.BotSettings, error) {
	r.mu.RLock()
	if r.cached != nil {
		cp := *r.cached
		r.mu.RUnlock()
		return &cp, nil
	}
	r.mu.RUnlock()

	loaded, err := r.load()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.cached != nil {
		cp := *r.cached
		r.mu.Unlock()
		return &cp, nil
	}
	cp := *loaded
	r.cached = &cp
	r.mu.Unlock()
	out := cp
	return &out, nil
}

func (r *BotSettingsRepository) load() (*models.BotSettings, error) {
	var s models.BotSettings
	err := r.db.Where("id = ?", botSettingsRowID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &models.BotSettings{ID: botSettingsRowID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *BotSettingsRepository) storeCache(s models.BotSettings) {
	r.mu.Lock()
	r.cached = &s
	r.mu.Unlock()
}

// SetWorkGroup 写入工作群 ID(0 视为清除绑定)
func (r *BotSettingsRepository) SetWorkGroup(chatID int64) error {
	return r.upsert(func(s *models.BotSettings) {
		s.WorkGroupID = chatID
	})
}

// SetBroadcastChat 写入播报频道/群组 ID(0 视为清除绑定)
func (r *BotSettingsRepository) SetBroadcastChat(chatID int64) error {
	return r.upsert(func(s *models.BotSettings) {
		s.BroadcastChatID = chatID
	})
}

// SetWaterDetectDisabled 写入水群检测开关; true=关闭, false=开启
func (r *BotSettingsRepository) SetWaterDetectDisabled(disabled bool) error {
	return r.upsert(func(s *models.BotSettings) {
		s.WaterDetectDisabled = disabled
	})
}

// SetSpamDetectDisabled 写入刷屏检测开关; true=关闭, false=开启
func (r *BotSettingsRepository) SetSpamDetectDisabled(disabled bool) error {
	return r.upsert(func(s *models.BotSettings) {
		s.SpamDetectDisabled = disabled
	})
}

// SetForwardBurstDisabled 写入并发转发禁言开关; true=关闭, false=开启
func (r *BotSettingsRepository) SetForwardBurstDisabled(disabled bool) error {
	return r.upsert(func(s *models.BotSettings) {
		s.ForwardBurstDisabled = disabled
	})
}

// SetNonAdminBotAction 写入非管理员 Bot 发言处置动作
func (r *BotSettingsRepository) SetNonAdminBotAction(action models.NonAdminBotAction) error {
	return r.upsert(func(s *models.BotSettings) {
		s.NonAdminBotAction = action
	})
}

// ClearWorkGroup 清除工作群绑定
func (r *BotSettingsRepository) ClearWorkGroup() error {
	return r.SetWorkGroup(0)
}

// ClearBroadcastChat 清除播报频道绑定
func (r *BotSettingsRepository) ClearBroadcastChat() error {
	return r.SetBroadcastChat(0)
}

// EnsureWorkGroupSeed 当表内尚无配置时, 用 seed 写入工作群 ID(用于向后兼容老 .env)
func (r *BotSettingsRepository) EnsureWorkGroupSeed(seed int64) error {
	if seed == 0 {
		return nil
	}
	current, err := r.Get()
	if err != nil {
		return err
	}
	if current.WorkGroupID != 0 {
		return nil
	}
	return r.SetWorkGroup(seed)
}

func (r *BotSettingsRepository) upsert(mutate func(*models.BotSettings)) error {
	var saved models.BotSettings
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s models.BotSettings
		err := tx.Where("id = ?", botSettingsRowID).First(&s).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s = models.BotSettings{ID: botSettingsRowID}
			mutate(&s)
			if err := tx.Create(&s).Error; err != nil {
				return err
			}
			saved = s
			return nil
		}
		if err != nil {
			return err
		}
		mutate(&s)
		if err := tx.Save(&s).Error; err != nil {
			return err
		}
		saved = s
		return nil
	})
	if err != nil {
		return err
	}
	r.storeCache(saved)
	return nil
}
