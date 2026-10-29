package bot

import (
	"fmt"
	"log"
	"runtime"
	"time"
)

// monitorTarget 监控目标(暴露 Worker 池统计 + 缓存命中率)
type monitorTarget interface {
	workerStats() (active int32, processed int64, queueSize int)
	cacheStats() (hits int64, misses int64, hitRate float64, hasCache bool)
	displayName() string
}

// Monitor 性能监控器
type Monitor struct {
	target   monitorTarget
	stopChan chan struct{}
	interval time.Duration
}

// NewRuntimeMonitor 基于 BotRuntime 创建监控器
func NewRuntimeMonitor(rt *BotRuntime, interval time.Duration) *Monitor {
	return &Monitor{
		target:   runtimeMonitorTarget{rt: rt},
		stopChan: make(chan struct{}),
		interval: interval,
	}
}

// Start 启动监控
func (m *Monitor) Start() {
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()

		log.Printf("[Monitor] %s 性能监控已启动,间隔: %v", m.target.displayName(), m.interval)

		for {
			select {
			case <-ticker.C:
				m.logStats()
			case <-m.stopChan:
				return
			}
		}
	}()
}

// Stop 停止监控
func (m *Monitor) Stop() {
	select {
	case <-m.stopChan:
	default:
		close(m.stopChan)
	}
}

// logStats 记录统计信息
func (m *Monitor) logStats() {
	activeWorkers, totalProcessed, queueSize := m.target.workerStats()

	numGoroutine := runtime.NumGoroutine()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	cacheStats := ""
	if hits, misses, hitRate, ok := m.target.cacheStats(); ok {
		cacheStats = fmt.Sprintf(" | 缓存: %.1f%% (%d/%d)", hitRate, hits, hits+misses)
	}

	log.Printf("[监控] %s | 协程:%d | 工作线程:%d/%d | 已处理:%d | 内存:%.1fMB%s",
		m.target.displayName(),
		numGoroutine, activeWorkers, queueSize, totalProcessed,
		float64(memStats.Alloc)/1024/1024, cacheStats)
}

type runtimeMonitorTarget struct {
	rt *BotRuntime
}

func (t runtimeMonitorTarget) workerStats() (int32, int64, int) {
	return t.rt.WorkerPool.GetStats()
}

func (t runtimeMonitorTarget) cacheStats() (int64, int64, float64, bool) {
	if t.rt.Cache == nil {
		return 0, 0, 0, false
	}
	hits, misses, hitRate := t.rt.Cache.GetStats()
	return hits, misses, hitRate, true
}

func (t runtimeMonitorTarget) displayName() string {
	if t.rt.IsMain {
		return fmt.Sprintf("主@%s", t.rt.BotUsername)
	}
	return fmt.Sprintf("克隆@%s", t.rt.BotUsername)
}
