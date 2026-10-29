package bot

import (
	"context"
	"fmt"
	"log"
	"telegram-ban-bot/internal/bot/handlers"
	"telegram-ban-bot/internal/database/models"
)

// registryAdapter 将 *Registry 封装为 handlers.BotRegistry 接口实现
// 通过这个接口让 handlers 包不必反向依赖 bot 包,避免循环依赖
type registryAdapter struct {
	reg *Registry
}

func newRegistryAdapter(reg *Registry) handlers.BotRegistry {
	if reg == nil {
		return nil
	}
	return &registryAdapter{reg: reg}
}

func (a *registryAdapter) IsGlobalRoot(userID int64) bool {
	return a.reg.IsGlobalRoot(userID)
}

func (a *registryAdapter) MainAuthor() int64 {
	return a.reg.MainAuthor()
}

func (a *registryAdapter) MainBotID() int64 {
	return a.reg.MainBotID()
}

func (a *registryAdapter) ListClones() []handlers.CloneSummary {
	mainRT, ok := a.reg.Get(a.reg.MainBotID())
	out := []handlers.CloneSummary{}
	if !ok || mainRT == nil || !mainRT.IsMain || mainRT.CloneBotRepo == nil {
		// 没有主 Runtime 的注册表时, 仅依据 Registry 内存中的克隆做展示
		for _, rt := range a.reg.ListClones() {
			out = append(out, handlers.CloneSummary{
				BotUserID:   rt.BotUserID,
				BotUsername: rt.BotUsername,
				Enabled:     true,
				Running:     true,
			})
		}
		return out
	}

	records, err := mainRT.CloneBotRepo.ListAll()
	if err != nil {
		return out
	}
	for _, r := range records {
		_, running := a.reg.Get(r.BotUserID)
		out = append(out, handlers.CloneSummary{
			BotUserID:    r.BotUserID,
			BotUsername:  r.BotUsername,
			LocalAdminID: r.LocalAdminID,
			Enabled:      r.Enabled,
			Running:      running,
		})
	}
	return out
}

func (a *registryAdapter) SpawnClone(token string, localAdminID int64, createdBy int64) (*handlers.CloneSummary, error) {
	mainRT, ok := a.reg.Get(a.reg.MainBotID())
	if !ok || mainRT == nil || !mainRT.IsMain {
		return nil, fmt.Errorf("找不到主 Bot")
	}
	rt, err := mainRT.SpawnClone(context.Background(), token, localAdminID, createdBy)
	if err != nil {
		return nil, err
	}
	return &handlers.CloneSummary{
		BotUserID:    rt.BotUserID,
		BotUsername:  rt.BotUsername,
		LocalAdminID: localAdminID,
		Enabled:      true,
		Running:      true,
	}, nil
}

func (a *registryAdapter) SetCloneEnabled(botUserID int64, enabled bool) error {
	mainRT, ok := a.reg.Get(a.reg.MainBotID())
	if !ok || mainRT == nil || !mainRT.IsMain || mainRT.CloneBotRepo == nil {
		return fmt.Errorf("找不到主 Bot")
	}
	if enabled {
		return mainRT.EnableClone(context.Background(), botUserID)
	}
	return mainRT.DisableClone(botUserID)
}

func (a *registryAdapter) SyncKeywordAdd(keywordType string, keyword string, createdBy int64) {
	kt := models.KeywordType(keywordType)
	for _, rt := range a.reg.List() {
		if err := rt.KeywordSvc.AddKeyword(kt, keyword, createdBy); err != nil {
			log.Printf("[关键词同步] 同步添加到 @%s 失败: type=%s word=%s err=%v", rt.BotUsername, keywordType, keyword, err)
		}
	}
}

func (a *registryAdapter) SyncKeywordDelete(keywordType string, keyword string) {
	kt := models.KeywordType(keywordType)
	for _, rt := range a.reg.List() {
		if err := rt.KeywordSvc.DeleteKeywordByWord(kt, keyword); err != nil {
			log.Printf("[关键词同步] 同步删除从 @%s 失败: type=%s word=%s err=%v", rt.BotUsername, keywordType, keyword, err)
		}
	}
}

func (a *registryAdapter) SyncWhitelistAdd(userID int64, addedBy int64) {
	for _, rt := range a.reg.List() {
		if rt.WhitelistSvc == nil {
			continue
		}
		if err := rt.WhitelistSvc.Add(userID, addedBy); err != nil {
			log.Printf("[白名单同步] 同步添加到 @%s 失败: user=%d err=%v", rt.BotUsername, userID, err)
		}
	}
}

func (a *registryAdapter) SyncWhitelistDelete(userID int64) {
	for _, rt := range a.reg.List() {
		if rt.WhitelistSvc == nil {
			continue
		}
		if err := rt.WhitelistSvc.Remove(userID); err != nil {
			log.Printf("[白名单同步] 同步删除从 @%s 失败: user=%d err=%v", rt.BotUsername, userID, err)
		}
	}
}

func (a *registryAdapter) SyncBatchBanByBio(keyword string, bannedBy int64) (banned int, skipped int) {
	for _, rt := range a.reg.List() {
		if rt.KeywordSvc == nil {
			continue
		}
		result := rt.KeywordSvc.BatchBanByBioKeyword(keyword, bannedBy, nil, 0, 0)
		if result != nil {
			banned += result.BannedCount
			skipped += result.SkippedCount
		}
		log.Printf("[跨Bot简介扫描] @%s 扫描完成: 封禁=%d 跳过=%d", rt.BotUsername, result.BannedCount, result.SkippedCount)
	}
	return
}
