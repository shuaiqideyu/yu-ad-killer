package cache

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"telegram-ban-bot/pkg/utils"
)

// bioCurrentTTL 当前简介 API 缓存时长。
// 简介变更不频繁,10 分钟内复用可显著降低 GetChat 调用,避免限流拖垮检测。
const bioCurrentTTL = 10 * time.Minute

type compiledKeywords struct {
	words  []string
	folded []string
}

type CacheService struct {
	prefix             string
	keywords           map[string]map[string]struct{}
	compiled           map[string]compiledKeywords
	duplicateWhitelist map[string]struct{}
	dupCompiled        []string
	userWhitelist      map[int64]struct{}
	bioHistory         map[int64][]string
	bioCurrent         map[int64]cachedBio
	chatAdmins         map[int64]cachedChatAdmins
	mu                 sync.RWMutex

	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
}

type cachedBio struct {
	bio       string
	expiresAt time.Time
	synced    bool
}

// cachedChatAdmins 缓存某个群组的 Telegram 管理员 user_id 集合。
// 群组管理员变更不频繁,5 分钟 TTL 既能跟上变更,也避免每条消息都打 getChatAdministrators API。
type cachedChatAdmins struct {
	ids       map[int64]struct{}
	expiresAt time.Time
}

func NewCacheService(prefix string) *CacheService {
	return &CacheService{
		prefix:             prefix,
		keywords:           make(map[string]map[string]struct{}),
		compiled:           make(map[string]compiledKeywords),
		duplicateWhitelist: make(map[string]struct{}),
		userWhitelist:      make(map[int64]struct{}),
		bioHistory:         make(map[int64][]string),
		bioCurrent:         make(map[int64]cachedBio),
		chatAdmins:         make(map[int64]cachedChatAdmins),
	}
}

// AddKeyword 添加关键词到缓存
func (s *CacheService) AddKeyword(keywordType, keyword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.keywords[keywordType] == nil {
		s.keywords[keywordType] = make(map[string]struct{})
	}
	s.keywords[keywordType][keyword] = struct{}{}
	s.recompileKeywordsLocked(keywordType)
	return nil
}

// RemoveKeyword 从缓存中删除关键词
func (s *CacheService) RemoveKeyword(keywordType, keyword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if keywords := s.keywords[keywordType]; keywords != nil {
		delete(keywords, keyword)
	}
	s.recompileKeywordsLocked(keywordType)
	return nil
}

// LoadKeywords 批量加载关键词到缓存
func (s *CacheService) LoadKeywords(keywordType string, keywords []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.keywords[keywordType] = make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		s.keywords[keywordType][keyword] = struct{}{}
	}
	s.recompileKeywordsLocked(keywordType)
	return nil
}

// HasKeywords 该类型是否已有关键词。没有时检测路径不必再解析链接。
func (s *CacheService) HasKeywords(keywordType string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.compiled[keywordType].words) > 0
}

// MatchKeyword 用启动/变更时折好的词表匹配。返回的是库里的原词。
func (s *CacheService) MatchKeyword(keywordType, text string) (bool, string) {
	if text == "" {
		return false, ""
	}
	s.mu.RLock()
	compiled := s.compiled[keywordType]
	s.mu.RUnlock()
	if len(compiled.words) == 0 {
		return false, ""
	}
	foldedText := utils.KeywordFold(text)
	for i, folded := range compiled.folded {
		if folded != "" && strings.Contains(foldedText, folded) {
			return true, compiled.words[i]
		}
	}
	return false, ""
}

func (s *CacheService) recompileKeywordsLocked(keywordType string) {
	s.compiled[keywordType] = compileKeywords(s.keywords[keywordType])
}

func compileKeywords(src map[string]struct{}) compiledKeywords {
	if len(src) == 0 {
		return compiledKeywords{}
	}
	words := make([]string, 0, len(src))
	folded := make([]string, 0, len(src))
	for word := range src {
		if word == "" {
			continue
		}
		words = append(words, word)
		folded = append(folded, utils.KeywordFold(word))
	}
	return compiledKeywords{words: words, folded: folded}
}

// AddDuplicateWhitelistKeyword 添加水群/刷屏检测白名单关键词。
func (s *CacheService) AddDuplicateWhitelistKeyword(keyword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.duplicateWhitelist[keyword] = struct{}{}
	s.recompileDupLocked()
	return nil
}

// RemoveDuplicateWhitelistKeyword 删除水群/刷屏检测白名单关键词。
func (s *CacheService) RemoveDuplicateWhitelistKeyword(keyword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.duplicateWhitelist, keyword)
	s.recompileDupLocked()
	return nil
}

// LoadDuplicateWhitelistKeywords 启动时批量重置水群/刷屏白名单缓存(用于从 DB 恢复)。
func (s *CacheService) LoadDuplicateWhitelistKeywords(keywords []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.duplicateWhitelist = make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		s.duplicateWhitelist[keyword] = struct{}{}
	}
	s.recompileDupLocked()
	return nil
}

// MatchesDuplicateWhitelist 空白无关、大小写无关地判断文本是否含白名单词。
func (s *CacheService) MatchesDuplicateWhitelist(text string) bool {
	normalizedText := utils.NormalizeLoose(text)
	if normalizedText == "" {
		return false
	}
	s.mu.RLock()
	norms := s.dupCompiled
	s.mu.RUnlock()
	for _, normalized := range norms {
		if strings.Contains(normalizedText, normalized) {
			return true
		}
	}
	return false
}

func (s *CacheService) recompileDupLocked() {
	if len(s.duplicateWhitelist) == 0 {
		s.dupCompiled = nil
		return
	}
	norms := make([]string, 0, len(s.duplicateWhitelist))
	for keyword := range s.duplicateWhitelist {
		normalized := utils.NormalizeLoose(keyword)
		if normalized == "" {
			continue
		}
		norms = append(norms, normalized)
	}
	s.dupCompiled = norms
}

// GetDuplicateWhitelistKeywords 获取水群/刷屏检测白名单关键词。
func (s *CacheService) GetDuplicateWhitelistKeywords() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.duplicateWhitelist) == 0 {
		s.cacheMisses.Add(1)
		return []string{}, nil
	}

	keywords := make([]string, 0, len(s.duplicateWhitelist))
	for keyword := range s.duplicateWhitelist {
		keywords = append(keywords, keyword)
	}

	s.cacheHits.Add(1)
	return keywords, nil
}

// GetCachedBio 读取未过期的当前简介缓存; found=false 表示未命中或已过期。
// synced=true 表示这条简介已经和数据库最新记录对齐，调用方不必再查库。
func (s *CacheService) GetCachedBio(userID int64) (bio string, found bool, synced bool) {
	s.mu.RLock()
	entry, ok := s.bioCurrent[userID]
	s.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		s.cacheMisses.Add(1)
		return "", false, false
	}
	s.cacheHits.Add(1)
	return entry.bio, true, entry.synced
}

// SetCachedBio 写入当前简介缓存
func (s *CacheService) SetCachedBio(userID int64, bio string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bioCurrent[userID] = cachedBio{
		bio:       bio,
		expiresAt: time.Now().Add(bioCurrentTTL),
	}
}

// MarkBioSynced 标记当前缓存简介已与数据库对齐。
func (s *CacheService) MarkBioSynced(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.bioCurrent[userID]
	if !ok || time.Now().After(entry.expiresAt) {
		return
	}
	entry.synced = true
	s.bioCurrent[userID] = entry
}

// InvalidateCachedBio 使当前简介缓存失效(进群检测前强制刷新)
func (s *CacheService) InvalidateCachedBio(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bioCurrent, userID)
}

// AddBioHistory 添加用户简介历史到缓存
func (s *CacheService) AddBioHistory(userID int64, bio string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 最新的简介放在最前面，保持和数据库 detected_at DESC 查询一致。
	s.bioHistory[userID] = append([]string{bio}, s.bioHistory[userID]...)
	s.bioCurrent[userID] = cachedBio{
		bio:       bio,
		expiresAt: time.Now().Add(bioCurrentTTL),
		synced:    true,
	}
	return nil
}

// GetBioHistory 获取用户简介历史
func (s *CacheService) GetBioHistory(userID int64) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bios := s.bioHistory[userID]
	if len(bios) == 0 {
		s.cacheMisses.Add(1)
		return []string{}, nil
	}

	result := append([]string(nil), bios...)
	s.cacheHits.Add(1)
	return result, nil
}

// DeleteBioHistory 删除用户简介历史缓存
func (s *CacheService) DeleteBioHistory(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.bioHistory, userID)
	delete(s.bioCurrent, userID)
	return nil
}

// LoadBioHistory 批量加载用户简介历史到缓存
func (s *CacheService) LoadBioHistory(userID int64, bios []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bioHistory[userID] = append([]string(nil), bios...)
	return nil
}

// GetAllBioUserIDs 获取所有有简介历史缓存的用户ID
func (s *CacheService) GetAllBioUserIDs() ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userIDs := make([]int64, 0, len(s.bioHistory))
	for userID := range s.bioHistory {
		userIDs = append(userIDs, userID)
	}

	return userIDs, nil
}

// GetStats 获取缓存统计信息
func (s *CacheService) GetStats() (hits int64, misses int64, hitRate float64) {
	hits = s.cacheHits.Load()
	misses = s.cacheMisses.Load()
	total := hits + misses

	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	return hits, misses, hitRate
}

func isExpired(expiresAt time.Time) bool {
	return !expiresAt.IsZero() && time.Now().After(expiresAt)
}

// ========== 群组管理员缓存 ==========

const chatAdminsTTL = 5 * time.Minute

// IsChatAdmin 判断用户是否在未过期的群管理员缓存中。
// known=false 表示未命中或已过期，调用方需要重新拉管理员列表。
func (s *CacheService) IsChatAdmin(chatID, userID int64) (isAdmin bool, known bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.chatAdmins[chatID]
	if !ok || isExpired(entry.expiresAt) {
		s.cacheMisses.Add(1)
		return false, false
	}
	_, isAdmin = entry.ids[userID]
	s.cacheHits.Add(1)
	return isAdmin, true
}

// SetChatAdmins 写入群组管理员缓存,统一 5 分钟 TTL
func (s *CacheService) SetChatAdmins(chatID int64, ids map[int64]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make(map[int64]struct{}, len(ids))
	for id := range ids {
		copied[id] = struct{}{}
	}
	s.chatAdmins[chatID] = cachedChatAdmins{
		ids:       copied,
		expiresAt: time.Now().Add(chatAdminsTTL),
	}
}

// InvalidateChatAdmins 主动失效某个群组的管理员缓存(例如收到 chat_member 更新后)
func (s *CacheService) InvalidateChatAdmins(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.chatAdmins, chatID)
}

// ========== 用户白名单缓存 ==========

// AddWhitelistUser 添加用户到白名单缓存
func (s *CacheService) AddWhitelistUser(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.userWhitelist[userID] = struct{}{}
}

// RemoveWhitelistUser 从白名单缓存移除用户
func (s *CacheService) RemoveWhitelistUser(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.userWhitelist, userID)
}

// IsWhitelistUser 检查用户是否在白名单缓存中
func (s *CacheService) IsWhitelistUser(userID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.userWhitelist[userID]
	return ok
}

// LoadWhitelistUsers 启动时批量加载白名单用户到缓存
func (s *CacheService) LoadWhitelistUsers(userIDs []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.userWhitelist = make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		s.userWhitelist[id] = struct{}{}
	}
}

// AddChatAdmin 单点追加一个管理员到缓存,不动 TTL(继续沿用既有过期时间,避免被频繁刷新)
// 用于运行期"自愈": 调封禁/删消息时 Telegram 返回 user is an administrator,
// 而本地缓存里却没有该用户(GetChatAdministrators 一次性拉取漏掉/失败),直接补上避免下一条再触发
func (s *CacheService) AddChatAdmin(chatID, userID int64) {
	if chatID == 0 || userID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.chatAdmins[chatID]
	if !ok || isExpired(entry.expiresAt) {
		entry = cachedChatAdmins{
			ids:       make(map[int64]struct{}),
			expiresAt: time.Now().Add(chatAdminsTTL),
		}
	}
	if entry.ids == nil {
		entry.ids = make(map[int64]struct{})
	}
	entry.ids[userID] = struct{}{}
	s.chatAdmins[chatID] = entry
}
