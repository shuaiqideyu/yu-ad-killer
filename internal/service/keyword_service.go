package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
	"telegram-ban-bot/pkg/utils"

	"github.com/mymmrac/telego"
)

// BatchBanResult 批量封禁结果
type BatchBanResult struct {
	BannedCount   int              // 成功封禁数量
	SkippedCount  int              // 跳过数量(已封禁)
	FailedCount   int              // 失败数量
	FailedUsers   []int64          // 失败的用户ID列表
	FailedReasons map[int64]string // 失败原因
}

// KeywordService 关键词服务
type KeywordService struct {
	cache       *cache.CacheService
	keywordRepo *repository.KeywordRepository
	bioRepo     *repository.BioRepository
	userRepo    *repository.UserRepository
	banService  *BanService
}

func NewKeywordService(
	cache *cache.CacheService,
	keywordRepo *repository.KeywordRepository,
	bioRepo *repository.BioRepository,
	userRepo *repository.UserRepository,
	banService *BanService,
) *KeywordService {
	return &KeywordService{
		cache:       cache,
		keywordRepo: keywordRepo,
		bioRepo:     bioRepo,
		userRepo:    userRepo,
		banService:  banService,
	}
}

// AddKeyword 添加关键词(支持 name/bio/msg/forward/invite/dup_white)
func (s *KeywordService) AddKeyword(keywordType models.KeywordType, keyword string, createdBy int64) error {
	existing, err := s.findEquivalentKeyword(keywordType, keyword)
	if err != nil {
		return fmt.Errorf("failed to check keyword existence: %w", err)
	}

	if existing != nil {
		log.Printf("[关键词服务] 关键词已存在,跳过插入: 类型=%s, 关键词=%s 已有=%s", keywordType, keyword, existing.Word)
		return nil
	}

	if err := s.keywordRepo.Add(keywordType, keyword, createdBy); err != nil {
		return fmt.Errorf("添加关键词到数据库失败: %w", err)
	}

	if keywordType == models.KeywordTypeDupWhite {
		if err := s.cache.AddDuplicateWhitelistKeyword(keyword); err != nil {
			log.Printf("[关键词服务] 添加白名单关键词到缓存失败: %v", err)
		}
	} else {
		if err := s.cache.AddKeyword(string(keywordType), keyword); err != nil {
			log.Printf("[关键词服务] 添加关键词到缓存失败: %v", err)
		}
	}

	log.Printf("[关键词服务] 关键词已添加: 类型=%s, 关键词=%s", keywordType, keyword)
	return nil
}

func (s *KeywordService) findEquivalentKeyword(keywordType models.KeywordType, keyword string) (*models.Keyword, error) {
	kws, err := s.keywordRepo.GetByType(keywordType)
	if err != nil {
		return nil, err
	}
	for i := range kws {
		if utils.SameKeyword(kws[i].Word, keyword) {
			return &kws[i], nil
		}
	}
	return nil, nil
}

func (s *KeywordService) mergeEquivalentKeywords(keywords []models.Keyword) []models.Keyword {
	type groupKey struct {
		typ  models.BanType
		fold string
	}
	groups := make(map[groupKey][]models.Keyword)
	order := make([]groupKey, 0)
	for _, kw := range keywords {
		k := groupKey{kw.Type, utils.KeywordFold(kw.Word)}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], kw)
	}

	kept := make([]models.Keyword, 0, len(order))
	for _, k := range order {
		group := groups[k]
		keep := group[0]
		for _, kw := range group[1:] {
			if kw.ID < keep.ID {
				keep = kw
			}
		}
		for _, kw := range group {
			if kw.ID == keep.ID {
				continue
			}
			if err := s.keywordRepo.Delete(kw.ID); err != nil {
				log.Printf("[关键词服务] 合并重复词失败: keep=%s drop=%s err=%v", keep.Word, kw.Word, err)
				continue
			}
			log.Printf("[关键词服务] 合并重复词: 类型=%s 保留=%s 删除=%s", keep.Type, keep.Word, kw.Word)
		}
		kept = append(kept, keep)
	}
	return kept
}

// DeleteKeywordByWord 按类型和词删除关键词(用于跨 Bot 同步删除)
func (s *KeywordService) DeleteKeywordByWord(keywordType models.KeywordType, word string) error {
	if err := s.keywordRepo.DeleteByTypeAndWord(keywordType, word); err != nil {
		return fmt.Errorf("删除关键词失败: %w", err)
	}
	if keywordType == models.KeywordTypeDupWhite {
		if err := s.cache.RemoveDuplicateWhitelistKeyword(word); err != nil {
			log.Printf("[关键词服务] 从缓存删除白名单关键词失败: %v", err)
		}
	} else {
		if err := s.cache.RemoveKeyword(string(keywordType), word); err != nil {
			log.Printf("[关键词服务] 从缓存删除关键词失败: %v", err)
		}
	}
	return nil
}

// DeleteKeyword 删除关键词
func (s *KeywordService) DeleteKeyword(id int64) error {
	// 先获取关键词信息
	kw, err := s.keywordRepo.GetByID(id)
	if err != nil {
		return fmt.Errorf("获取关键词失败: %w", err)
	}

	// 从数据库删除
	if err := s.keywordRepo.Delete(id); err != nil {
		return fmt.Errorf("从数据库删除关键词失败: %w", err)
	}

	// 从缓存删除
	if err := s.cache.RemoveKeyword(string(kw.Type), kw.Word); err != nil {
		log.Printf("[关键词服务] 从缓存删除关键词失败: %v", err)
	}

	log.Printf("[关键词服务] 关键词已删除: ID=%d, 关键词=%s", id, kw.Word)
	return nil
}

// UpdateKeyword 更新关键词
func (s *KeywordService) UpdateKeyword(id int64, newKeyword string) error {
	// 先获取旧关键词
	oldKw, err := s.keywordRepo.GetByID(id)
	if err != nil {
		return fmt.Errorf("获取关键词失败: %w", err)
	}
	if existing, err := s.findEquivalentKeyword(models.KeywordType(oldKw.Type), newKeyword); err != nil {
		return fmt.Errorf("检查关键词重复失败: %w", err)
	} else if existing != nil && existing.ID != id {
		return fmt.Errorf("已存在等价关键词: %s", existing.Word)
	}

	// 更新数据库
	if err := s.keywordRepo.Update(id, newKeyword); err != nil {
		return fmt.Errorf("更新数据库关键词失败: %w", err)
	}

	// 更新缓存（先删除旧的，再添加新的）
	if err := s.cache.RemoveKeyword(string(oldKw.Type), oldKw.Word); err != nil {
		log.Printf("[关键词服务] 从缓存删除旧关键词失败: %v", err)
	}
	if err := s.cache.AddKeyword(string(oldKw.Type), newKeyword); err != nil {
		log.Printf("[关键词服务] 添加新关键词到缓存失败: %v", err)
	}

	log.Printf("[关键词服务] 关键词已更新: ID=%d, 旧=%s, 新=%s", id, oldKw.Word, newKeyword)
	return nil
}

// GetAllKeywords 获取全部关键词(用于跨 Bot 初始同步)
func (s *KeywordService) GetAllKeywords() ([]models.Keyword, error) {
	return s.keywordRepo.GetAll()
}

// GetKeywords 获取指定类型的关键词列表
func (s *KeywordService) GetKeywords(keywordType models.KeywordType) ([]models.Keyword, error) {
	return s.keywordRepo.GetByType(keywordType)
}

// GetKeywordsPaginated 分页获取指定类型的关键词列表
func (s *KeywordService) GetKeywordsPaginated(keywordType models.KeywordType, offset, limit int) ([]models.Keyword, int64, error) {
	return s.keywordRepo.GetByTypePaginated(keywordType, offset, limit)
}

// LoadAllKeywordsToCache 加载所有关键词到缓存
//
// 检测类型(name/bio/msg/forward/invite) 一律用 DB 结果重置(含空表),防止 /reload 后残留旧词;
// 水群/刷屏白名单(dup_white) 走独立 map,不污染检测路径.
func (s *KeywordService) LoadAllKeywordsToCache() error {
	keywords, err := s.keywordRepo.GetAll()
	if err != nil {
		return fmt.Errorf("failed to get all keywords: %w", err)
	}
	keywords = s.mergeEquivalentKeywords(keywords)

	keywordMap := make(map[string][]string)
	for _, kw := range keywords {
		keywordType := string(kw.Type)
		keywordMap[keywordType] = append(keywordMap[keywordType], kw.Word)
	}

	dupWhite := keywordMap[string(models.KeywordTypeDupWhite)]

	for _, keywordType := range []string{
		string(models.KeywordTypeName),
		string(models.KeywordTypeBio),
		string(models.KeywordTypeMsg),
		string(models.KeywordTypeForward),
		string(models.KeywordTypeInvite),
	} {
		kws := keywordMap[keywordType]
		if kws == nil {
			kws = []string{}
		}
		if err := s.cache.LoadKeywords(keywordType, kws); err != nil {
			return fmt.Errorf("加载关键词到缓存失败: %w", err)
		}
		log.Printf("[关键词服务] 已加载 %d 个类型为 %s 的关键词到缓存", len(kws), keywordType)
	}

	if err := s.cache.LoadDuplicateWhitelistKeywords(dupWhite); err != nil {
		return fmt.Errorf("加载水群白名单到缓存失败: %w", err)
	}
	log.Printf("[关键词服务] 已加载 %d 个水群/刷屏白名单到缓存", len(dupWhite))

	return nil
}

// preloadBioHistories 批量预加载简介历史到内存缓存(优化查询性能)
func (s *KeywordService) preloadBioHistories(userIDs []int64) {
	const batchSize = 200
	log.Printf("[关键词服务] 开始批量预加载 %d 个用户的简介历史", len(userIDs))

	for i := 0; i < len(userIDs); i += batchSize {
		end := i + batchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		missing := make([]int64, 0, end-i)
		for _, userID := range userIDs[i:end] {
			bios, err := s.cache.GetBioHistory(userID)
			if err != nil || len(bios) == 0 {
				missing = append(missing, userID)
			}
		}
		if len(missing) == 0 {
			continue
		}
		grouped, err := s.bioRepo.GetHistoriesByUserIDs(missing)
		if err != nil {
			log.Printf("[关键词服务] 批量加载简介历史失败: %v", err)
			continue
		}
		for userID, bios := range grouped {
			if len(bios) == 0 {
				continue
			}
			if err := s.cache.LoadBioHistory(userID, bios); err != nil {
				log.Printf("[关键词服务] 写入简介缓存失败: user=%d err=%v", userID, err)
			}
		}
	}

	log.Printf("[关键词服务] 简介历史预加载完成")
}

// BatchBanByBioKeyword 根据简介关键词批量封禁用户(支持进度更新)
func (s *KeywordService) BatchBanByBioKeyword(keyword string, bannedBy int64, bot interface{}, chatID int64, messageID int) *BatchBanResult {
	result := &BatchBanResult{
		FailedUsers:   make([]int64, 0),
		FailedReasons: make(map[int64]string),
	}

	log.Printf("[关键词服务] 开始批量封禁简介关键词: %s", keyword)

	// 1. 获取所有有简介历史的用户ID
	allUserIDs, err := s.bioRepo.GetAllUserIDs()
	if err != nil {
		log.Printf("[关键词服务] 获取用户ID列表失败: %v", err)
		result.FailedReasons[0] = fmt.Sprintf("获取用户列表失败: %v", err)
		return result
	}

	log.Printf("[关键词服务] 正在从 %d 个用户中过滤匹配关键词的用户", len(allUserIDs))

	// 2. 批量预加载简介历史到内存缓存(优化查询性能)
	s.preloadBioHistories(allUserIDs)

	// 3. 并发过滤:只保留简介匹配关键词的用户
	matchedUserIDs := make([]int64, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	userChan := make(chan int64, len(allUserIDs))

	// 启动并发worker进行过滤
	const filterWorkers = 20
	for i := 0; i < filterWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for userID := range userChan {
				// 优先从缓存读取简介历史
				bios, err := s.cache.GetBioHistory(userID)
				if err != nil || len(bios) == 0 {
					// 缓存未命中,从数据库读取
					bios, err = s.bioRepo.GetAllHistory(userID)
					if err != nil {
						continue
					}
					// 回写内存缓存
					if len(bios) > 0 {
						s.cache.LoadBioHistory(userID, bios)
					}
				}

				// 检查是否有任何历史简介匹配关键词
				matched := false
				for _, bio := range bios {
					if bio == "" {
						continue
					}
					if isMatch, _ := matchSingleKeyword(bio, keyword); isMatch {
						matched = true
						break
					}
				}

				if matched {
					mu.Lock()
					matchedUserIDs = append(matchedUserIDs, userID)
					mu.Unlock()
				}
			}
		}()
	}

	// 分发任务
	for _, userID := range allUserIDs {
		userChan <- userID
	}
	close(userChan)
	wg.Wait()

	totalUsers := len(matchedUserIDs)
	log.Printf("[关键词服务] 找到 %d 个匹配关键词的用户: %s", totalUsers, keyword)

	// 4. 分批并发处理匹配的用户
	const batchSize = 100
	const concurrency = 10

	var processed int

	for batchStart := 0; batchStart < totalUsers; batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > totalUsers {
			batchEnd = totalUsers
		}

		batchUsers := matchedUserIDs[batchStart:batchEnd]

		// Worker pool处理当前批次
		var wg sync.WaitGroup
		userChan := make(chan int64, len(batchUsers))

		// 启动workers
		for i := 0; i < concurrency && i < len(batchUsers); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for userID := range userChan {
					s.processUserForBan(userID, keyword, bannedBy, result, &mu)
				}
			}()
		}

		// 分发任务
		for _, userID := range batchUsers {
			userChan <- userID
		}
		close(userChan)
		wg.Wait()

		// 更新进度
		mu.Lock()
		processed += len(batchUsers)
		mu.Unlock()

		// 更新Telegram消息进度
		s.updateProgress(bot, chatID, messageID, keyword, processed, totalUsers, result)
	}

	log.Printf("[关键词服务] 批量封禁完成: 已封禁=%d, 已跳过=%d, 失败=%d",
		result.BannedCount, result.SkippedCount, result.FailedCount)
	return result
}

// processUserForBan 处理单个用户的封禁检查(用于并发,用户已预过滤匹配关键词)
func (s *KeywordService) processUserForBan(userID int64, keyword string, bannedBy int64, result *BatchBanResult, mu *sync.Mutex) {
	// 批量处理时直接从数据库查询封禁状态,避免缓存脏数据
	isBanned, err := s.userRepo.IsBanned(userID)
	if err != nil {
		log.Printf("[关键词服务] 检查用户 %d 封禁状态失败: %v", userID, err)
		mu.Lock()
		result.FailedCount++
		result.FailedUsers = append(result.FailedUsers, userID)
		result.FailedReasons[userID] = fmt.Sprintf("检查封禁状态失败: %v", err)
		mu.Unlock()
		return
	}

	// 如果已封禁，跳过
	if isBanned {
		mu.Lock()
		result.SkippedCount++
		mu.Unlock()
		log.Printf("[关键词服务] 用户 %d 已被封禁,跳过", userID)
		return
	}

	// 获取匹配的简介内容(用于记录)
	bios, err := s.cache.GetBioHistory(userID)
	if err != nil || len(bios) == 0 {
		bios, err = s.bioRepo.GetAllHistory(userID)
		if err != nil {
			log.Printf("[关键词服务] 获取用户 %d 简介历史失败: %v", userID, err)
			mu.Lock()
			result.FailedCount++
			result.FailedUsers = append(result.FailedUsers, userID)
			result.FailedReasons[userID] = fmt.Sprintf("获取简介历史失败: %v", err)
			mu.Unlock()
			return
		}
	}

	// 找到匹配的简介内容
	var matchedBio string
	for _, bio := range bios {
		if bio == "" {
			continue
		}
		if isMatch, _ := matchSingleKeyword(bio, keyword); isMatch {
			matchedBio = bio
			break
		}
	}

	// 执行封禁
	reason := fmt.Sprintf("简介包含关键词: %s", keyword)
	err = s.banService.BanUser(
		0, // chatID 设为 0，批量封禁不需要踢出群组
		userID,
		models.BanTypeBio,
		reason,
		keyword,
		matchedBio, // 使用匹配的简介作为消息文本
		bannedBy,
		0,    // 批量封禁没有具体消息ID
		true, // 批量封禁,跳过踢出操作
	)
	if err != nil {
		log.Printf("[关键词服务] 封禁用户 %d 失败: %v", userID, err)
		mu.Lock()
		result.FailedCount++
		result.FailedUsers = append(result.FailedUsers, userID)
		result.FailedReasons[userID] = fmt.Sprintf("封禁操作失败: %v", err)
		mu.Unlock()
		return
	}

	mu.Lock()
	result.BannedCount++
	mu.Unlock()
	log.Printf("[关键词服务] 用户 %d 因简介关键词被封禁: %s", userID, keyword)
}

// updateProgress 更新Telegram消息进度
func (s *KeywordService) updateProgress(bot interface{}, chatID int64, messageID int, keyword string, processed, total int, result *BatchBanResult) {
	if bot == nil || messageID == 0 {
		return
	}

	// 类型断言获取bot实例
	telegoBot, ok := bot.(*telego.Bot)
	if !ok {
		return
	}

	text := fmt.Sprintf(
		"✅ 已拉黑简介关键词: <code>%s</code>\n\n📊 批量封禁结果:\n  • 正在封禁 %d/%d 用户",
		keyword, processed, total,
	)

	telegoBot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:    telego.ChatID{ID: chatID},
		MessageID: messageID,
		Text:      text,
		ParseMode: "HTML",
	})
}

// matchSingleKeyword 匹配单个关键词,与消息/昵称/简介检测共用 MatchKeyword
func matchSingleKeyword(text, keyword string) (bool, string) {
	return utils.MatchKeyword(text, []string{keyword})
}
