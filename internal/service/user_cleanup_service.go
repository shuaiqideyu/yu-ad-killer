package service

import (
	"log"
	"sync"
	"time"

	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/repository"
)

const userCleanupBatchSize = 500

// UserCleanupService 定期清理长时间未活跃的非管理员用户数据。
type UserCleanupService struct {
	userRepo        *repository.UserRepository
	cacheService    *cache.CacheService
	inactiveTTL     time.Duration
	cleanupInterval time.Duration
	startOnce       sync.Once
}

func NewUserCleanupService(
	userRepo *repository.UserRepository,
	cacheService *cache.CacheService,
	inactiveTTL time.Duration,
	cleanupInterval time.Duration,
) *UserCleanupService {
	return &UserCleanupService{
		userRepo:        userRepo,
		cacheService:    cacheService,
		inactiveTTL:     inactiveTTL,
		cleanupInterval: cleanupInterval,
	}
}

func (s *UserCleanupService) Start() {
	if s == nil || s.userRepo == nil {
		return
	}

	s.startOnce.Do(func() {
		go func() {
			log.Printf("[用户清理] 已启动, 未活跃阈值: %v, 检查间隔: %v", s.inactiveTTL, s.cleanupInterval)

			s.cleanupInactiveUsers()

			ticker := time.NewTicker(s.cleanupInterval)
			defer ticker.Stop()

			for range ticker.C {
				s.cleanupInactiveUsers()
			}
		}()
	})
}

func (s *UserCleanupService) cleanupInactiveUsers() {
	inactiveBefore := time.Now().Add(-s.inactiveTTL)

	userIDs, err := s.userRepo.ListInactiveUserIDs(inactiveBefore, userCleanupBatchSize)
	if err != nil {
		log.Printf("[用户清理] 查询未活跃用户失败: %v", err)
		return
	}

	if len(userIDs) == 0 {
		return
	}

	if err := s.userRepo.DeleteUserDataByIDs(userIDs); err != nil {
		log.Printf("[用户清理] 删除未活跃用户数据失败: %v", err)
		return
	}

	s.deleteUserCaches(userIDs)
	log.Printf("[用户清理] 已清理 %d 个超过 %v 未活跃的用户", len(userIDs), s.inactiveTTL)
}

func (s *UserCleanupService) deleteUserCaches(userIDs []int64) {
	if s.cacheService == nil {
		return
	}

	for _, userID := range userIDs {
		if err := s.cacheService.DeleteBioHistory(userID); err != nil {
			log.Printf("[用户清理] 删除简介缓存失败: userID=%d err=%v", userID, err)
		}
	}
}
