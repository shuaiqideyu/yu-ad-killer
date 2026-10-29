package repository

import (
	"telegram-ban-bot/internal/database/models"

	"gorm.io/gorm"
)

// AdminRepository 管理员数据访问层
type AdminRepository struct {
	db *gorm.DB
}

func NewAdminRepository(db *gorm.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

// GetByUserID 根据用户ID获取管理员
func (r *AdminRepository) GetByUserID(userID int64) (*models.Admin, error) {
	var admin models.Admin
	err := r.db.Where("user_id = ?", userID).First(&admin).Error
	if err != nil {
		return nil, err
	}
	return &admin, nil
}

// IsAdmin 检查用户是否是管理员(包括作者)
func (r *AdminRepository) IsAdmin(userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.Admin{}).Where("user_id = ?", userID).Count(&count).Error
	return count > 0, err
}

// IsAuthor 检查用户是否是作者
func (r *AdminRepository) IsAuthor(userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.Admin{}).
		Where("user_id = ? AND role = ?", userID, models.RoleAuthor).
		Count(&count).Error
	return count > 0, err
}

// Add 添加管理员
func (r *AdminRepository) Add(userID int64, role models.AdminRole, createdBy int64) error {
	admin := &models.Admin{
		UserID:    userID,
		Role:      role,
		CreatedBy: createdBy,
	}
	return r.db.Create(admin).Error
}

// Delete 删除管理员
func (r *AdminRepository) Delete(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&models.Admin{}).Error
}

// GetAll 获取所有管理员
func (r *AdminRepository) GetAll() ([]models.Admin, error) {
	var admins []models.Admin
	err := r.db.Order("created_at ASC").Find(&admins).Error
	return admins, err
}

// Count 统计管理员数量
func (r *AdminRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&models.Admin{}).Count(&count).Error
	return count, err
}

// EnsureAuthor 确保作者存在(用于初始化)
func (r *AdminRepository) EnsureAuthor(authorID int64) error {
	// 检查是否已存在
	isAuthor, err := r.IsAuthor(authorID)
	if err != nil {
		return err
	}

	if isAuthor {
		return nil // 已存在
	}

	// 创建作者
	return r.Add(authorID, models.RoleAuthor, 0)
}
