package repository

import (
	"telegram-ban-bot/internal/database/models"

	"gorm.io/gorm"
)

type KeywordRepository struct {
	db *gorm.DB
}

func NewKeywordRepository(db *gorm.DB) *KeywordRepository {
	return &KeywordRepository{db: db}
}

// DB 返回数据库连接
func (r *KeywordRepository) DB() *gorm.DB {
	return r.db
}

// Add 添加关键词
func (r *KeywordRepository) Add(keywordType models.KeywordType, keyword string, createdBy int64) error {
	kw := &models.Keyword{
		Type:      models.BanType(keywordType),
		Word:      keyword,
		CreatedBy: createdBy,
	}
	return r.db.Create(kw).Error
}

// Delete 删除关键词
func (r *KeywordRepository) Delete(id int64) error {
	return r.db.Delete(&models.Keyword{}, id).Error
}

// DeleteByTypeAndWord 按 (类型, 关键词) 精确删除; 不存在不报错
func (r *KeywordRepository) DeleteByTypeAndWord(keywordType models.KeywordType, keyword string) error {
	return r.db.Where("type = ? AND keyword = ?", keywordType, keyword).
		Delete(&models.Keyword{}).Error
}

// Update 更新关键词
func (r *KeywordRepository) Update(id int64, newKeyword string) error {
	return r.db.Model(&models.Keyword{}).Where("id = ?", id).Update("keyword", newKeyword).Error
}

// GetByType 根据类型获取所有关键词
func (r *KeywordRepository) GetByType(keywordType models.KeywordType) ([]models.Keyword, error) {
	var keywords []models.Keyword
	err := r.db.Where("type = ?", keywordType).Order("id DESC").Find(&keywords).Error
	return keywords, err
}

// GetByTypePaginated 按类型分页查询关键词
func (r *KeywordRepository) GetByTypePaginated(keywordType models.KeywordType, offset, limit int) ([]models.Keyword, int64, error) {
	var keywords []models.Keyword
	var total int64

	query := r.db.Model(&models.Keyword{}).Where("type = ?", keywordType)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&keywords).Error
	return keywords, total, err
}

// CountByType 按类型统计关键词数量; 一次查询返回所有类型的计数
func (r *KeywordRepository) CountByType() (map[models.KeywordType]int64, error) {
	type row struct {
		Type  string
		Count int64
	}
	var rows []row
	err := r.db.Model(&models.Keyword{}).
		Select("type, count(*) as count").
		Group("type").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[models.KeywordType]int64, len(rows))
	for _, r := range rows {
		result[models.KeywordType(r.Type)] = r.Count
	}
	return result, nil
}

// GetAll 获取所有关键词
func (r *KeywordRepository) GetAll() ([]models.Keyword, error) {
	var keywords []models.Keyword
	err := r.db.Find(&keywords).Error
	return keywords, err
}

// GetByID 根据ID获取关键词
func (r *KeywordRepository) GetByID(id int64) (*models.Keyword, error) {
	var keyword models.Keyword
	err := r.db.First(&keyword, id).Error
	if err != nil {
		return nil, err
	}
	return &keyword, nil
}

// Exists 检查关键词是否已存在
func (r *KeywordRepository) Exists(keywordType models.KeywordType, keyword string) (bool, error) {
	var count int64
	err := r.db.Model(&models.Keyword{}).
		Where("type = ? AND keyword = ?", keywordType, keyword).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// List 分页查询关键词
func (r *KeywordRepository) List(offset, limit int) ([]models.Keyword, int64, error) {
	var keywords []models.Keyword
	var total int64

	if err := r.db.Model(&models.Keyword{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := r.db.Order("id DESC").Offset(offset).Limit(limit).Find(&keywords).Error
	return keywords, total, err
}

// Search 搜索关键词
func (r *KeywordRepository) Search(keyword string, offset, limit int) ([]models.Keyword, int64, error) {
	var keywords []models.Keyword
	var total int64

	query := r.db.Model(&models.Keyword{}).Where("keyword LIKE ?", "%"+keyword+"%")

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&keywords).Error
	return keywords, total, err
}

// Create 创建关键词
func (r *KeywordRepository) Create(keyword *models.Keyword) error {
	return r.db.Create(keyword).Error
}
