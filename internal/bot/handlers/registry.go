package handlers

// CloneSummary Bot 克隆实例的概要信息(供 /admin 列表展示)
type CloneSummary struct {
	BotUserID    int64
	BotUsername  string
	LocalAdminID int64
	Enabled      bool
	Running      bool
}

// BotRegistry 抽象 bot 包内的 Registry 与多 Runtime 控制能力,
// 让 handlers 包不需要反向引入 bot 包,避免循环依赖
type BotRegistry interface {
	// IsGlobalRoot 该用户是否为主 Bot 作者(全局超管)
	IsGlobalRoot(userID int64) bool
	// MainAuthor 主 Bot 作者 ID(仅供展示,可能为 0)
	MainAuthor() int64
	// MainBotID 主 Bot 的 telegram bot user id
	MainBotID() int64
	// ListClones 列出所有(已注册的)克隆 Bot 概要
	ListClones() []CloneSummary
	// SpawnClone 创建并启动一个新克隆 Bot, 返回成功后的概要
	SpawnClone(token string, localAdminID int64, createdBy int64) (*CloneSummary, error)
	// SetCloneEnabled 启用或停用一个克隆 Bot(影响 clone_bots.enabled 与运行状态)
	SetCloneEnabled(botUserID int64, enabled bool) error

	// SyncKeywordAdd 向所有运行中的 Bot 广播添加关键词(已存在则跳过)
	SyncKeywordAdd(keywordType string, keyword string, createdBy int64)
	// SyncKeywordDelete 向所有运行中的 Bot 广播删除关键词
	SyncKeywordDelete(keywordType string, keyword string)

	// SyncWhitelistAdd 向所有运行中的 Bot 广播添加白名单用户
	SyncWhitelistAdd(userID int64, addedBy int64)
	// SyncWhitelistDelete 向所有运行中的 Bot 广播移除白名单用户
	SyncWhitelistDelete(userID int64)

	// SyncBatchBanByBio 在所有其他 Bot 上执行简介关键词批量封禁(跨库扫描)
	// 返回其他 Bot 累计封禁数和跳过数
	SyncBatchBanByBio(keyword string, bannedBy int64) (banned int, skipped int)
}
