package repository

import (
	"telegram-ban-bot/internal/database/models"

	"gorm.io/gorm"
)

type WhitelistRepository struct {
	db *gorm.DB
}

func NewWhitelistRepository(db *gorm.DB) *WhitelistRepository {
	return &WhitelistRepository{db: db}
}

// Add 添加用户到白名单(已存在则忽略)
func (r *WhitelistRepository) Add(userID, addedBy int64) error {
	wl := &models.Whitelist{
		UserID:  userID,
		AddedBy: addedBy,
	}
	return r.db.
		Where("user_id = ?", userID).
		FirstOrCreate(wl).Error
}

// Remove 从白名单移除用户
func (r *WhitelistRepository) Remove(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&models.Whitelist{}).Error
}

// IsWhitelisted 检查用户是否在白名单中
func (r *WhitelistRepository) IsWhitelisted(userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.Whitelist{}).
		Where("user_id = ?", userID).
		Count(&count).Error
	return count > 0, err
}

// GetAllUserIDs 获取所有白名单用户ID(用于启动时加载到缓存)
func (r *WhitelistRepository) GetAllUserIDs() ([]int64, error) {
	var ids []int64
	err := r.db.Model(&models.Whitelist{}).Pluck("user_id", &ids).Error
	return ids, err
}

// GetPaginated 分页查询白名单
func (r *WhitelistRepository) GetPaginated(offset, limit int) ([]models.Whitelist, int64, error) {
	var items []models.Whitelist
	var total int64

	query := r.db.Model(&models.Whitelist{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}
