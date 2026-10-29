package detector

import (
	"context"
	"log"
	"strings"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/repository"
	"telegram-ban-bot/pkg/utils"

	"github.com/mymmrac/telego"
)

// BioDetector 简介检测器
type BioDetector struct {
	cache   *cache.CacheService
	bioRepo *repository.BioRepository
	bot     *telego.Bot
}

func NewBioDetector(cache *cache.CacheService, bioRepo *repository.BioRepository, bot *telego.Bot) *BioDetector {
	return &BioDetector{
		cache:   cache,
		bioRepo: bioRepo,
		bot:     bot,
	}
}

func (d *BioDetector) Check(message *telego.Message) (bool, string, error) {
	return d.check(message, false)
}

// CheckFresh 强制刷新简介后再检测(用于进群)
func (d *BioDetector) CheckFresh(message *telego.Message) (bool, string, error) {
	return d.check(message, true)
}

func (d *BioDetector) check(message *telego.Message, forceRefresh bool) (bool, string, error) {
	if message.From == nil {
		return false, "", nil
	}

	userID := message.From.ID

	currentBio, extras, synced, err := d.getProfileTexts(userID, forceRefresh)
	if err != nil {
		log.Printf("[BioDetector] 获取简介失败: ID=%d Err=%v", userID, err)
		return false, "", nil
	}

	if !synced {
		if _, err := d.bioRepo.AddHistoryIfChanged(userID, currentBio); err != nil {
			log.Printf("[BioDetector] 更新简介失败: ID=%d, Err=%v", userID, err)
		} else if d.cache != nil {
			d.cache.MarkBioSynced(userID)
		}
	}

	if matched, keyword := matchBioText(currentBio, nil); matched {
		log.Printf("[BioDetector] 匹配: ID=%d | 关键词=%s", userID, keyword)
		return true, keyword, nil
	}
	if d.cache != nil {
		if matched, keyword := d.cache.MatchKeyword("bio", currentBio); matched {
			log.Printf("[BioDetector] 匹配: ID=%d | 关键词=%s", userID, keyword)
			return true, keyword, nil
		}
	}
	if extras != "" && utils.RussianHit(extras) != "" {
		log.Printf("[BioDetector] 匹配: ID=%d | 关键词=资料含俄罗斯元素", userID)
		return true, "资料含俄罗斯元素", nil
	}
	return false, "", nil
}

func matchBioText(bio string, keywords []string) (bool, string) {
	switch utils.RussianHit(bio) {
	case "cyrillic":
		return true, "简介含俄语"
	case "place":
		return true, "简介含俄罗斯地区"
	}
	return utils.MatchKeyword(bio, keywords)
}

func (d *BioDetector) getProfileTexts(userID int64, forceRefresh bool) (bio string, extras string, synced bool, err error) {
	if !forceRefresh {
		if cached, ok, alreadySynced := d.cache.GetCachedBio(userID); ok {
			return cached, "", alreadySynced, nil
		}
	} else {
		d.cache.InvalidateCachedBio(userID)
	}

	chat, err := d.bot.GetChat(context.Background(), &telego.GetChatParams{
		ChatID: telego.ChatID{ID: userID},
	})
	if err != nil {
		return "", "", false, err
	}

	if chat != nil {
		bio = chat.Bio
		extras = profileExtraText(chat)
	}
	d.cache.SetCachedBio(userID, bio)
	return bio, extras, false, nil
}

func profileExtraText(chat *telego.ChatFullInfo) string {
	if chat == nil {
		return ""
	}
	var parts []string
	if chat.BusinessLocation != nil {
		if addr := strings.TrimSpace(chat.BusinessLocation.Address); addr != "" {
			parts = append(parts, addr)
		}
	}
	if chat.BusinessIntro != nil {
		if title := strings.TrimSpace(chat.BusinessIntro.Title); title != "" {
			parts = append(parts, title)
		}
		if msg := strings.TrimSpace(chat.BusinessIntro.Message); msg != "" {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, " ")
}
