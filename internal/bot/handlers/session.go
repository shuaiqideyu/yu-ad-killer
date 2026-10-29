package handlers

import (
	"time"
)

// SetSessionState 设置会话状态
func (h *Handler) SetSessionState(userID int64, state string, duration time.Duration) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()

	h.sessionStates[userID] = &SessionState{
		State:    state,
		ExpireAt: time.Now().Add(duration),
	}
}

// GetSessionState 获取会话状态
func (h *Handler) GetSessionState(userID int64) (string, bool) {
	h.sessionMu.RLock()
	defer h.sessionMu.RUnlock()

	state, exists := h.sessionStates[userID]
	if !exists {
		return "", false
	}

	// 检查是否过期
	if time.Now().After(state.ExpireAt) {
		return "", false
	}

	return state.State, true
}

// ClearSessionState 清除会话状态
func (h *Handler) ClearSessionState(userID int64) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()

	delete(h.sessionStates, userID)
}

// cleanSessionStates 定期清理过期的会话状态
func (h *Handler) cleanSessionStates() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.sessionMu.Lock()
			now := time.Now()
			for userID, state := range h.sessionStates {
				if now.After(state.ExpireAt) {
					delete(h.sessionStates, userID)
				}
			}
			h.sessionMu.Unlock()

			// 同时清理过期的 addBotPending
			h.addBotPendingMu.Lock()
			for uid, p := range h.addBotPending {
				if time.Since(p.StartedAt) > 10*time.Minute {
					delete(h.addBotPending, uid)
				}
			}
			h.addBotPendingMu.Unlock()
		case <-h.stopCh:
			return
		}
	}
}
