package repository

import (
	"log"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"

	"gorm.io/gorm"
)

type BioRepository struct {
	db    *gorm.DB
	cache *cache.CacheService
}

func NewBioRepository(db *gorm.DB, cacheService *cache.CacheService) *BioRepository {
	return &BioRepository{
		db:    db,
		cache: cacheService,
	}
}

// AddHistory 添加简介历史记录
func (r *BioRepository) AddHistory(userID int64, bio string) error {
	history := &models.BioHistory{
		UserID: userID,
		Bio:    bio,
	}
	err := r.db.Create(history).Error

	if err == nil {
		if addErr := r.cache.AddBioHistory(userID, bio); addErr != nil {
			log.Printf("[简介仓库] 添加简介历史到缓存失败: %v", addErr)
		}
		log.Printf("[简介仓库] 用户 %d 简介历史已添加并同步到缓存", userID)
	}

	return err
}

// AddHistoryIfChanged 原子检查并添加简介历史: 仅当最新记录与传入 bio 不同时才写入
func (r *BioRepository) AddHistoryIfChanged(userID int64, bio string) (changed bool, err error) {
	latest, err := r.GetLatestBio(userID)
	if err != nil {
		return false, err
	}
	if latest == bio {
		return false, nil
	}
	if err := r.AddHistory(userID, bio); err != nil {
		return false, err
	}
	return true, nil
}

// GetLatestBio 获取用户最新简介
func (r *BioRepository) GetLatestBio(userID int64) (string, error) {
	var history models.BioHistory
	err := r.db.Where("user_id = ?", userID).
		Order("detected_at DESC").
		First(&history).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return history.Bio, nil
}

// GetAllHistory 获取用户所有简介历史
func (r *BioRepository) GetAllHistory(userID int64) ([]string, error) {
	var histories []models.BioHistory
	err := r.db.Where("user_id = ?", userID).
		Order("detected_at DESC").
		Find(&histories).Error
	if err != nil {
		return nil, err
	}

	bios := make([]string, 0, len(histories))
	for _, h := range histories {
		if h.Bio != "" {
			bios = append(bios, h.Bio)
		}
	}
	return bios, nil
}

// GetHistoriesByUserIDs 按用户批量读取简介历史，每个用户内按 detected_at 倒序。
func (r *BioRepository) GetHistoriesByUserIDs(userIDs []int64) (map[int64][]string, error) {
	grouped := make(map[int64][]string)
	if len(userIDs) == 0 {
		return grouped, nil
	}
	var histories []models.BioHistory
	err := r.db.Where("user_id IN ?", userIDs).
		Order("detected_at DESC").
		Find(&histories).Error
	if err != nil {
		return nil, err
	}
	for _, history := range histories {
		if history.Bio == "" {
			continue
		}
		grouped[history.UserID] = append(grouped[history.UserID], history.Bio)
	}
	return grouped, nil
}

// GetAllUserIDs 获取所有有简介历史的用户ID
func (r *BioRepository) GetAllUserIDs() ([]int64, error) {
	var userIDs []int64
	err := r.db.Model(&models.BioHistory{}).
		Distinct("user_id").
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}
