package repository

import (
	"errors"
	"log"
	"sync"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrSelfUnbanAlreadyUsed 自助解封机会已被使用
var ErrSelfUnbanAlreadyUsed = errors.New("自助解封机会已使用")

type UserRepository struct {
	db        *gorm.DB
	cache     *cache.CacheService
	banMu     sync.RWMutex
	banned    map[int64]struct{}
	banLoaded bool
}

func NewUserRepository(db *gorm.DB, cacheService *cache.CacheService) *UserRepository {
	return &UserRepository{
		db:    db,
		cache: cacheService,
	}
}

// CreateOrUpdate 创建或更新用户
func (r *UserRepository) CreateOrUpdate(user *models.User) error {
	// 使用 Clauses + OnConflict 实现 UPSERT
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"username", "first_name", "last_name", "updated_at"}),
	}).Create(user).Error
}

// GetByID 根据ID获取用户(不使用缓存,直接查询数据库确保实时性)
func (r *UserRepository) GetByID(id int64) (*models.User, error) {
	var user models.User
	err := r.db.Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// GetByUsername 根据用户名获取用户
func (r *UserRepository) GetByUsername(username string) (*models.User, error) {
	var user models.User
	err := r.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByIDs 批量获取用户
func (r *UserRepository) GetByIDs(ids []int64) ([]models.User, error) {
	if len(ids) == 0 {
		return []models.User{}, nil
	}

	var users []models.User
	err := r.db.Where("id IN ?", ids).Find(&users).Error
	return users, err
}

// WarmBannedCache 启动时把已封禁用户载入内存。之后的封禁/解封在同一进程里改这张表。
func (r *UserRepository) WarmBannedCache() error {
	var ids []int64
	err := r.db.Model(&models.User{}).Where("is_banned = ?", true).Pluck("id", &ids).Error
	if err != nil {
		return err
	}
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	r.banMu.Lock()
	r.banned = set
	r.banLoaded = true
	r.banMu.Unlock()
	return nil
}

// IsBanned 检查用户是否被封禁。进程内缓存与 Ban/Unban 同步更新。
func (r *UserRepository) IsBanned(userID int64) (bool, error) {
	r.banMu.RLock()
	loaded := r.banLoaded
	_, banned := r.banned[userID]
	r.banMu.RUnlock()
	if loaded {
		return banned, nil
	}

	var user models.User
	err := r.db.Select("is_banned").Where("id = ?", userID).First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user.IsBanned, nil
}

// Ban 封禁用户
func (r *UserRepository) Ban(userID int64, reason string) error {
	now := time.Now()
	res := r.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"is_banned":  true,
		"ban_reason": reason,
		"banned_at":  &now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		r.noteBanned(userID, true)
		log.Printf("[用户仓库] 用户 %d 已封禁, 原因: %s", userID, reason)
	}
	return nil
}

// Unban 解封用户
func (r *UserRepository) Unban(userID int64) error {
	res := r.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"is_banned":  false,
		"ban_reason": nil,
		"banned_at":  nil,
	})
	if res.Error != nil {
		return res.Error
	}
	r.noteBanned(userID, false)
	if res.RowsAffected > 0 {
		log.Printf("[用户仓库] 用户 %d 已解封", userID)
	}
	return nil
}

// TouchActivity 只刷新活跃时间，资料未变时不用整行 UPSERT。
func (r *UserRepository) TouchActivity(userID int64) (bool, error) {
	res := r.db.Model(&models.User{}).Where("id = ?", userID).Update("updated_at", time.Now())
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *UserRepository) noteBanned(userID int64, banned bool) {
	r.banMu.Lock()
	defer r.banMu.Unlock()
	if !r.banLoaded {
		return
	}
	if r.banned == nil {
		r.banned = make(map[int64]struct{})
	}
	if banned {
		r.banned[userID] = struct{}{}
		return
	}
	delete(r.banned, userID)
}

func (r *UserRepository) forgetBanned(userIDs []int64) {
	r.banMu.Lock()
	defer r.banMu.Unlock()
	if !r.banLoaded {
		return
	}
	for _, userID := range userIDs {
		delete(r.banned, userID)
	}
}

// MarkSelfUnbanUsed 原子标记用户已使用自助解封
// 返回 ErrSelfUnbanAlreadyUsed 表示已被其他请求抢先使用
func (r *UserRepository) MarkSelfUnbanUsed(userID int64) error {
	result := r.db.Model(&models.User{}).
		Where("id = ? AND (self_unban_used = ? OR self_unban_used IS NULL)", userID, false).
		Update("self_unban_used", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSelfUnbanAlreadyUsed
	}
	return nil
}

// List 查询用户列表（分页）
func (r *UserRepository) List(keyword string, isBanned *bool, offset, limit int) ([]models.User, int64, error) {
	var users []models.User
	var total int64

	query := r.db.Model(&models.User{})

	if keyword != "" {
		query = query.Where("username LIKE ? OR first_name LIKE ? OR last_name LIKE ?",
			"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}
	if isBanned != nil {
		query = query.Where("is_banned = ?", *isBanned)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&users).Error

	return users, total, err
}

// Count 统计总用户数
func (r *UserRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&models.User{}).Count(&count).Error
	return count, err
}

// CountBanned 统计被封禁用户数
func (r *UserRepository) CountBanned() (int64, error) {
	var count int64
	err := r.db.Model(&models.User{}).Where("is_banned = ?", true).Count(&count).Error
	return count, err
}

// ListInactiveUserIDs 获取超过指定时间未活跃的用户ID（排除管理员）
func (r *UserRepository) ListInactiveUserIDs(inactiveBefore time.Time, limit int) ([]int64, error) {
	var userIDs []int64

	adminSubQuery := r.db.Model(&models.Admin{}).Select("user_id")
	query := r.db.Model(&models.User{}).
		Where("updated_at < ?", inactiveBefore).
		Where("id NOT IN (?)", adminSubQuery).
		Order("updated_at ASC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Pluck("id", &userIDs).Error
	return userIDs, err
}

// DeleteUserDataByIDs 删除用户及其简介历史
func (r *UserRepository) DeleteUserDataByIDs(userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id IN ?", userIDs).Delete(&models.BioHistory{}).Error; err != nil {
			return err
		}

		if err := tx.Where("id IN ?", userIDs).Delete(&models.User{}).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}
	r.forgetBanned(userIDs)
	return nil
}
