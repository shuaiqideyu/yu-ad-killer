package detector

import (
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
	"telegram-ban-bot/pkg/utils"

	"github.com/mymmrac/telego"
)

// DetectorManager 检测器管理器
type DetectorManager struct {
	nameDetector          *NameDetector
	bioDetector           *BioDetector
	messageDetector       *MessageDetector
	externalReplyDetector *ExternalReplyDetector
}

func NewDetectorManager(
	cache *cache.CacheService,
	bioRepo *repository.BioRepository,
	bot *telego.Bot,
) *DetectorManager {
	return &DetectorManager{
		nameDetector:          NewNameDetector(cache),
		bioDetector:           NewBioDetector(cache, bioRepo, bot),
		messageDetector:       NewMessageDetector(cache),
		externalReplyDetector: NewExternalReplyDetector(),
	}
}

// CheckAll 检查所有规则(消息路径,简介可走短时缓存)
func (dm *DetectorManager) CheckAll(message *telego.Message) (violated bool, banType, reason string, err error) {
	return dm.checkAll(message, false)
}

// CheckAllFreshBio 进群等场景强制刷新简介后再检测,避免复用过期缓存
func (dm *DetectorManager) CheckAllFreshBio(message *telego.Message) (violated bool, banType, reason string, err error) {
	return dm.checkAll(message, true)
}

func (dm *DetectorManager) checkAll(message *telego.Message, freshBio bool) (violated bool, banType, reason string, err error) {
	if violated, reason, err := dm.externalReplyDetector.Check(message); err != nil {
		return false, "", "", err
	} else if violated {
		return true, "external_reply", reason, nil
	}

	if violated, keyword, err := dm.nameDetector.Check(message); err != nil {
		return false, "", "", err
	} else if violated {
		return true, "name", keyword, nil
	}

	var bioViolated bool
	var bioType, bioKeyword string
	var bioErr error
	if freshBio {
		bioViolated, bioType, bioKeyword, bioErr = dm.bioDetector.CheckFresh(message)
	} else {
		bioViolated, bioType, bioKeyword, bioErr = dm.bioDetector.Check(message)
	}
	if bioErr != nil {
		return false, "", "", bioErr
	} else if bioViolated {
		return true, bioType, bioKeyword, nil
	}

	if violated, banType, keyword, err := dm.messageDetector.Check(message); err != nil {
		return false, "", "", err
	} else if violated {
		return true, banType, keyword, nil
	}

	return false, "", "", nil
}

// NameDetector 昵称(+用户名)检测器
type NameDetector struct {
	cache *cache.CacheService
}

func NewNameDetector(cache *cache.CacheService) *NameDetector {
	return &NameDetector{cache: cache}
}

func (d *NameDetector) Check(message *telego.Message) (bool, string, error) {
	if message.From == nil {
		return false, "", nil
	}

	if utils.IsRussianLanguage(message.From.LanguageCode) {
		return true, "账号语言为俄语", nil
	}

	name := utils.GetFullName(message.From.FirstName, message.From.LastName)
	switch utils.RussianHit(name) {
	case "cyrillic":
		return true, "俄罗斯名字", nil
	case "place":
		return true, "昵称含俄罗斯地区", nil
	}
	if utils.RussianHit(message.From.Username) == "place" {
		return true, "用户名含俄罗斯地区", nil
	}

	checkText := name
	if message.From.Username != "" {
		if checkText != "" {
			checkText += " "
		}
		checkText += message.From.Username
	}
	if checkText == "" {
		return false, "", nil
	}

	if d.cache == nil {
		return false, "", nil
	}
	matched, keyword := d.cache.MatchKeyword("name", checkText)
	return matched, keyword, nil
}

// MessageDetector 消息内容检测器
type MessageDetector struct {
	cache   *cache.CacheService
	invites *InviteResolver
}

func NewMessageDetector(cache *cache.CacheService) *MessageDetector {
	return &MessageDetector{cache: cache, invites: sharedInviteResolver}
}

func (d *MessageDetector) Check(message *telego.Message) (bool, string, string, error) {
	text := ""
	if message != nil {
		text = message.Text
		if text == "" && message.Caption != "" {
			text = message.Caption
		}
	}
	if d.cache == nil {
		return false, "", "", nil
	}
	if text != "" {
		if matched, keyword := d.cache.MatchKeyword("msg", text); matched {
			return true, string(models.BanTypeMsg), keyword, nil
		}
	}
	if matched, keyword := matchInviteKeyword(d.cache, d.invites, messageLinkBlobs(message)...); matched {
		return true, string(models.BanTypeInvite), keyword, nil
	}
	return false, "", "", nil
}

// ExternalReplyDetector 频道回复检测器
type ExternalReplyDetector struct{}

func NewExternalReplyDetector() *ExternalReplyDetector {
	return &ExternalReplyDetector{}
}

func (d *ExternalReplyDetector) Check(message *telego.Message) (bool, string, error) {
	if message.ExternalReply == nil || message.ExternalReply.Origin == nil {
		return false, "", nil
	}

	channelOrigin, ok := message.ExternalReply.Origin.(*telego.MessageOriginChannel)
	if !ok || channelOrigin == nil {
		return false, "", nil
	}

	hasText := message.Text != "" || message.Caption != ""
	if !hasText {
		return false, "", nil
	}

	return true, "回复频道消息并附带文字", nil
}
