package repository

import (
	"telegram-ban-bot/internal/database/models"
	"time"

	"gorm.io/gorm"
)

type BanRepository struct {
	db *gorm.DB
}

func NewBanRepository(db *gorm.DB) *BanRepository {
	return &BanRepository{db: db}
}

// DB 返回数据库连接
func (r *BanRepository) DB() *gorm.DB {
	return r.db
}

// AddRecord 添加封禁记录
func (r *BanRepository) AddRecord(record *models.BanRecord) error {
	return r.db.Create(record).Error
}

// GetUserRecords 获取用户的封禁记录
func (r *BanRepository) GetUserRecords(userID int64) ([]models.BanRecord, error) {
	var records []models.BanRecord
	err := r.db.Where("user_id = ?", userID).
		Order("banned_at DESC").
		Find(&records).Error
	return records, err
}

// GetLatestRecord 获取用户最新的封禁记录
func (r *BanRepository) GetLatestRecord(userID int64) (*models.BanRecord, error) {
	var record models.BanRecord
	err := r.db.Where("user_id = ?", userID).
		Order("banned_at DESC").
		First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// GetLatestActiveRecord 获取用户最新的、仍生效中的处罚记录(未手动解除且未到期)
// 可选按类型过滤(banTypes 为空表示不过滤)
func (r *BanRepository) GetLatestActiveRecord(userID int64, banTypes ...models.BanType) (*models.BanRecord, error) {
	var record models.BanRecord
	now := time.Now()

	query := r.db.Where("user_id = ?", userID).
		Where("revoked_at IS NULL").
		Where("until_date IS NULL OR until_date > ?", now)

	if len(banTypes) > 0 {
		query = query.Where("ban_type IN ?", banTypes)
	}

	err := query.Order("banned_at DESC").First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// RevokeRecord 将指定记录标记为已解除
func (r *BanRepository) RevokeRecord(recordID int64) error {
	now := time.Now()
	return r.db.Model(&models.BanRecord{}).
		Where("id = ? AND revoked_at IS NULL", recordID).
		Update("revoked_at", &now).Error
}

// RevokeActiveRecords 将用户指定类型的所有生效中记录标记为已解除
// banTypes 为空则解除所有类型
func (r *BanRepository) RevokeActiveRecords(userID int64, banTypes ...models.BanType) error {
	now := time.Now()
	query := r.db.Model(&models.BanRecord{}).
		Where("user_id = ?", userID).
		Where("revoked_at IS NULL").
		Where("until_date IS NULL OR until_date > ?", now)
	if len(banTypes) > 0 {
		query = query.Where("ban_type IN ?", banTypes)
	}
	return query.Update("revoked_at", &now).Error
}

// GetUserBanChats 获取用户所有被封禁过的群组ID列表(去重)
func (r *BanRepository) GetUserBanChats(userID int64) ([]int64, error) {
	var chatIDs []int64
	err := r.db.Model(&models.BanRecord{}).
		Where("user_id = ? AND chat_id IS NOT NULL AND chat_id != 0", userID).
		Distinct("chat_id").
		Pluck("chat_id", &chatIDs).Error
	return chatIDs, err
}

// List 查询封禁记录列表（分页）
func (r *BanRepository) List(banType, keyword string, startTime, endTime interface{}, offset, limit int) ([]models.BanRecord, int64, error) {
	var records []models.BanRecord
	var total int64

	query := r.db.Model(&models.BanRecord{})

	if banType != "" {
		query = query.Where("ban_type = ?", banType)
	}
	if keyword != "" {
		query = query.Where("triggered_keyword LIKE ?", "%"+keyword+"%")
	}
	if startTime != nil {
		query = query.Where("banned_at >= ?", startTime)
	}
	if endTime != nil {
		query = query.Where("banned_at <= ?", endTime)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("banned_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&records).Error

	return records, total, err
}

// GetByID 根据ID查询封禁记录
func (r *BanRepository) GetByID(id int64) (*models.BanRecord, error) {
	var record models.BanRecord
	err := r.db.Where("id = ?", id).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// CountByTimeRange 统计时间范围内的封禁数量
func (r *BanRepository) CountByTimeRange(startTime, endTime interface{}) (int64, error) {
	var count int64
	query := r.db.Model(&models.BanRecord{})
	
	if startTime != nil {
		query = query.Where("banned_at >= ?", startTime)
	}
	if endTime != nil {
		query = query.Where("banned_at < ?", endTime)
	}
	
	err := query.Count(&count).Error
	return count, err
}

// CountByType 统计指定类型的封禁数量
func (r *BanRepository) CountByType(banType models.BanType) (int64, error) {
	var count int64
	err := r.db.Model(&models.BanRecord{}).Where("ban_type = ?", banType).Count(&count).Error
	return count, err
}

// GetKeywordRanking 获取关键词命中排行
func (r *BanRepository) GetKeywordRanking(limit int) ([]map[string]interface{}, error) {
	var results []map[string]interface{}
	err := r.db.Model(&models.BanRecord{}).
		Select("triggered_keyword as keyword, COUNT(*) as count").
		Where("triggered_keyword != ''").
		Group("triggered_keyword").
		Order("count DESC").
		Limit(limit).
		Find(&results).Error
	return results, err
}
