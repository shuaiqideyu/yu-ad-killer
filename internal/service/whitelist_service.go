package service

import (
	"fmt"
	"log"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
)

// WhitelistService 用户白名单服务
type WhitelistService struct {
	cache *cache.CacheService
	repo  *repository.WhitelistRepository
}

func NewWhitelistService(cache *cache.CacheService, repo *repository.WhitelistRepository) *WhitelistService {
	return &WhitelistService{
		cache: cache,
		repo:  repo,
	}
}

// Add 添加用户到白名单(DB + 缓存双写)
func (s *WhitelistService) Add(userID, addedBy int64) error {
	if err := s.repo.Add(userID, addedBy); err != nil {
		return fmt.Errorf("写入白名单失败: %w", err)
	}
	s.cache.AddWhitelistUser(userID)
	log.Printf("[白名单] 已添加用户 %d (操作人 %d)", userID, addedBy)
	return nil
}

// Remove 从白名单移除用户(DB + 缓存双写)
func (s *WhitelistService) Remove(userID int64) error {
	if err := s.repo.Remove(userID); err != nil {
		return fmt.Errorf("移除白名单失败: %w", err)
	}
	s.cache.RemoveWhitelistUser(userID)
	log.Printf("[白名单] 已移除用户 %d", userID)
	return nil
}

// IsWhitelisted 检查用户是否在白名单中(优先走缓存)
func (s *WhitelistService) IsWhitelisted(userID int64) bool {
	return s.cache.IsWhitelistUser(userID)
}

// LoadToCache 启动时从数据库加载白名单到缓存
func (s *WhitelistService) LoadToCache() error {
	ids, err := s.repo.GetAllUserIDs()
	if err != nil {
		return fmt.Errorf("加载白名单用户ID失败: %w", err)
	}
	s.cache.LoadWhitelistUsers(ids)
	log.Printf("[白名单] 已加载 %d 个白名单用户到缓存", len(ids))
	return nil
}

// GetAllUserIDs 获取全部白名单用户 ID(用于跨 Bot 初始同步)
func (s *WhitelistService) GetAllUserIDs() ([]int64, error) {
	return s.repo.GetAllUserIDs()
}

// GetPaginated 分页获取白名单列表
func (s *WhitelistService) GetPaginated(offset, limit int) ([]models.Whitelist, int64, error) {
	return s.repo.GetPaginated(offset, limit)
}
