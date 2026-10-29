package repository

import (
	"errors"
	"telegram-ban-bot/internal/database/models"

	"gorm.io/gorm"
)

// CloneBotRepository 克隆 Bot 注册表访问层
type CloneBotRepository struct {
	db *gorm.DB
}

func NewCloneBotRepository(db *gorm.DB) *CloneBotRepository {
	return &CloneBotRepository{db: db}
}

// Create 写入一条克隆 Bot 注册
func (r *CloneBotRepository) Create(bot *models.CloneBot) error {
	return r.db.Create(bot).Error
}

// GetByBotUserID 按 telegram bot user id 查询
func (r *CloneBotRepository) GetByBotUserID(botUserID int64) (*models.CloneBot, error) {
	var b models.CloneBot
	err := r.db.Where("bot_user_id = ?", botUserID).First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListEnabled 列出所有启用中的克隆 Bot
func (r *CloneBotRepository) ListEnabled() ([]models.CloneBot, error) {
	var bots []models.CloneBot
	err := r.db.Where("enabled = ?", true).Order("created_at ASC").Find(&bots).Error
	return bots, err
}

// ListAll 列出所有克隆 Bot(含已停用)
func (r *CloneBotRepository) ListAll() ([]models.CloneBot, error) {
	var bots []models.CloneBot
	err := r.db.Order("created_at ASC").Find(&bots).Error
	return bots, err
}

// SetEnabled 启用/停用某个克隆 Bot
func (r *CloneBotRepository) SetEnabled(botUserID int64, enabled bool) error {
	res := r.db.Model(&models.CloneBot{}).
		Where("bot_user_id = ?", botUserID).
		Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("clone bot 不存在")
	}
	return nil
}

// Delete 物理删除克隆 Bot 注册(数据库文件需另行清理)
func (r *CloneBotRepository) Delete(botUserID int64) error {
	return r.db.Where("bot_user_id = ?", botUserID).Delete(&models.CloneBot{}).Error
}
