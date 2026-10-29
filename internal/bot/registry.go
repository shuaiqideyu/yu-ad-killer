package bot

import "sync"

// Registry 维护进程内所有 Bot 实例(主 Bot + 克隆 Bot),用于:
//  1. /add 创建克隆 Bot 时启动并登记
//  2. /admin 列表/启停克隆 Bot
//  3. 主 Bot 作者作为全局超管,被克隆 Bot 的 Handler 反查
//
// key=bot user id;主 Bot 与克隆 Bot 同样以自己的 BotUserID 作为 key
type Registry struct {
	mu          sync.RWMutex
	mainBotID   int64
	mainAuthor  int64
	runtimes    map[int64]*BotRuntime
}

// NewRegistry 创建空注册表
func NewRegistry() *Registry {
	return &Registry{
		runtimes: make(map[int64]*BotRuntime),
	}
}

// SetMainAuthor 记录主 Bot 作者 ID(用于全局超管判断)
func (r *Registry) SetMainAuthor(authorID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mainAuthor = authorID
}

// MainAuthor 获取主 Bot 作者 ID
func (r *Registry) MainAuthor() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mainAuthor
}

// IsGlobalRoot 判断 userID 是否为全局超管(即主 Bot 作者)
func (r *Registry) IsGlobalRoot(userID int64) bool {
	if userID == 0 {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return userID == r.mainAuthor
}

// SetMainBotID 记录主 Bot 的 telegram bot user id
func (r *Registry) SetMainBotID(botUserID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mainBotID = botUserID
}

// MainBotID 获取主 Bot 的 telegram bot user id
func (r *Registry) MainBotID() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mainBotID
}

// Add 登记一个 Bot Runtime(主 Bot/克隆 Bot 都用此接口)
func (r *Registry) Add(rt *BotRuntime) {
	if rt == nil || rt.BotUserID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes[rt.BotUserID] = rt
}

// Remove 从注册表移除一个 Bot Runtime(不停止 Runtime 本身)
func (r *Registry) Remove(botUserID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runtimes, botUserID)
}

// Get 按 bot user id 取出 Runtime
func (r *Registry) Get(botUserID int64) (*BotRuntime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.runtimes[botUserID]
	return rt, ok
}

// List 返回所有 Runtime(包含主 Bot)的快照
func (r *Registry) List() []*BotRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*BotRuntime, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		out = append(out, rt)
	}
	return out
}

// ListClones 返回所有非主 Bot 的 Runtime 快照
func (r *Registry) ListClones() []*BotRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*BotRuntime, 0, len(r.runtimes))
	for id, rt := range r.runtimes {
		if id == r.mainBotID {
			continue
		}
		out = append(out, rt)
	}
	return out
}
