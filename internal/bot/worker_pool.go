package bot

import (
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// WorkerPool Worker池,用于控制并发goroutine数量
type WorkerPool struct {
	taskQueue      chan func()
	workerCount    int
	activeWorkers  int32
	totalProcessed int64
	stopped        atomic.Bool
	wg             sync.WaitGroup
	stopChan       chan struct{}
}

// NewWorkerPool 创建Worker池
func NewWorkerPool(workerCount int) *WorkerPool {
	if workerCount <= 0 {
		workerCount = 100
	}

	pool := &WorkerPool{
		taskQueue:   make(chan func(), workerCount*2),
		workerCount: workerCount,
		stopChan:    make(chan struct{}),
	}

	return pool
}

// Start 启动Worker池
func (p *WorkerPool) Start() {
	log.Printf("[WorkerPool] 启动 %d 个worker", p.workerCount)

	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// worker 工作协程
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case task := <-p.taskQueue:
			if task == nil {
				return
			}

			atomic.AddInt32(&p.activeWorkers, 1)

			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[WorkerPool] Worker %d panic恢复: %v\n%s", id, r, debug.Stack())
					}
					atomic.AddInt32(&p.activeWorkers, -1)
					atomic.AddInt64(&p.totalProcessed, 1)
				}()
				task()
			}()

		case <-p.stopChan:
			return
		}
	}
}

// Submit 提交任务到Worker池; 池已停止时静默丢弃
func (p *WorkerPool) Submit(task func()) {
	if p.stopped.Load() {
		return
	}

	select {
	case p.taskQueue <- task:
	default:
		log.Printf("[WorkerPool] 队列已满,降级为直接创建goroutine")
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[WorkerPool] 降级任务panic恢复: %v", r)
				}
			}()
			task()
		}()
	}
}

// Stop 停止Worker池
func (p *WorkerPool) Stop() {
	log.Println("[WorkerPool] 正在停止...")
	p.stopped.Store(true)
	close(p.stopChan)
	close(p.taskQueue)
	p.wg.Wait()
	log.Println("[WorkerPool] 已停止")
}

// GetStats 获取统计信息
func (p *WorkerPool) GetStats() (activeWorkers int32, totalProcessed int64, queueSize int) {
	return atomic.LoadInt32(&p.activeWorkers),
		atomic.LoadInt64(&p.totalProcessed),
		len(p.taskQueue)
}
