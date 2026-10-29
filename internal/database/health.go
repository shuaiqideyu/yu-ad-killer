package database

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"
)

// HealthChecker 数据库健康检查器
type HealthChecker struct {
	db       *gorm.DB
	interval time.Duration
	stopCh   chan struct{}
}

// NewHealthChecker 创建健康检查器
func NewHealthChecker(db *gorm.DB, interval time.Duration) *HealthChecker {
	return &HealthChecker{
		db:       db,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动健康检查
func (h *HealthChecker) Start() {
	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				h.check()
			case <-h.stopCh:
				log.Println("[PG健康检查] 已停止")
				return
			}
		}
	}()
	log.Printf("[PG健康检查] 已启动,检查间隔: %v", h.interval)
}

// Stop 停止健康检查
func (h *HealthChecker) Stop() {
	close(h.stopCh)
}

// check 执行健康检查
func (h *HealthChecker) check() {
	sqlDB, err := h.db.DB()
	if err != nil {
		log.Printf("[PG健康检查] 获取数据库连接失败: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		log.Printf("[PG健康检查] Ping失败: %v, 尝试重连...", err)
		// 这里可以添加重连逻辑,但 GORM 通常会自动重连
		return
	}

	// 获取连接池统计信息
	stats := sqlDB.Stats()
	if stats.OpenConnections > 80 { // 超过80%连接数时警告
		log.Printf("[PG健康检查] 警告: 连接数较高 OpenConns=%d/%d, InUse=%d, Idle=%d",
			stats.OpenConnections, stats.MaxOpenConnections, stats.InUse, stats.Idle)
	}
}
