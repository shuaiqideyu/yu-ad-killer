package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"telegram-ban-bot/internal/bot/handlers"
	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/config"
	"telegram-ban-bot/internal/database"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
	"telegram-ban-bot/internal/detector"
	"telegram-ban-bot/internal/service"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

const (
	// getUpdates 长轮询秒数。telego 只在参数为 nil 时才自己填 8 秒，这里必须显式给出。
	getUpdatesTimeout = 25
	// 读超时必须盖住长轮询，否则连接会在服务端返回前被掐断。
	telegramReadTimeout = 40 * time.Second
)

// BotRuntime 单个 Bot 的运行时容器(主 Bot 和克隆 Bot 共用)
type BotRuntime struct {
	BotUserID   int64
	BotUsername string
	IsMain      bool
	Token       string
	DBPath      string
	CachePrefix string

	Bot            *telego.Bot
	DB             *gorm.DB
	Cache          *cache.CacheService
	WorkerPool     *WorkerPool
	Monitor        *Monitor
	Handler        *handlers.Handler
	BanService     *service.BanService
	BroadcastSvc   *service.BroadcastService
	NotifySvc      *service.NotificationService
	KeywordSvc     *service.KeywordService
	UserSvc        *service.UserService
	AdminSvc       *service.AdminService
	WhitelistSvc   *service.WhitelistService
	UserCleanupSvc *service.UserCleanupService
	SettingsRepo   *repository.BotSettingsRepository
	CloneBotRepo   *repository.CloneBotRepository

	cfg      *config.Config
	registry *Registry

	mu         sync.Mutex
	started    bool
	lpCancel   context.CancelFunc
	botHandler *th.BotHandler

	cloneOpsMu sync.Mutex // 串行化克隆生命周期操作 (Spawn/Enable/Disable/Restore)
}

// RuntimeOptions 创建 Runtime 的入参
type RuntimeOptions struct {
	IsMain      bool
	Token       string
	DBPath      string
	CachePrefix string
	// LocalAuthorIDs 这些用户在新 DB 内会被 EnsureAuthor(用于克隆 Bot 初始化时把
	// /add 输入的本地 admin_id 与主作者一并写入 admins 表)
	LocalAuthorIDs []int64
	// WorkGroupSeed 当 bot_settings 为空时, 用此值作为工作群初值(向后兼容老 .env)
	WorkGroupSeed int64
}

// NewRuntime 构造 Runtime: 打开 DB、装配所有 service/handler、登记 Registry
// 不会启动 long-polling, 调用方需要再调 Start()
func NewRuntime(cfg *config.Config, registry *Registry, opts RuntimeOptions) (*BotRuntime, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("bot token 不能为空")
	}

	tgBot, err := newTelegramBot(opts.Token)
	if err != nil {
		return nil, fmt.Errorf("创建 telego.Bot 失败: %w", err)
	}

	me, err := tgBot.GetMe(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getMe 失败,token 可能无效: %w", err)
	}

	db, err := database.InitSQLiteWithOptions(opts.DBPath, database.InitOptions{IsMain: opts.IsMain})
	if err != nil {
		return nil, fmt.Errorf("初始化 SQLite 失败: %w", err)
	}

	cachePrefix := opts.CachePrefix
	if cachePrefix == "" {
		cachePrefix = "tgban:"
	}
	cacheSvc := cache.NewCacheService(cachePrefix)

	userRepo := repository.NewUserRepository(db, cacheSvc)
	if err := userRepo.WarmBannedCache(); err != nil {
		log.Printf("[Runtime] 加载封禁名单失败,封禁判断回退到数据库: %v", err)
	}
	bioRepo := repository.NewBioRepository(db, cacheSvc)
	keywordRepo := repository.NewKeywordRepository(db)
	banRepo := repository.NewBanRepository(db)
	adminRepo := repository.NewAdminRepository(db)
	settingsRepo := repository.NewBotSettingsRepository(db)
	var cloneBotRepo *repository.CloneBotRepository
	if opts.IsMain {
		cloneBotRepo = repository.NewCloneBotRepository(db)
	}

	if err := settingsRepo.EnsureWorkGroupSeed(opts.WorkGroupSeed); err != nil {
		log.Printf("[Runtime] 写入工作群 seed 失败: %v", err)
	}

	whitelistRepo := repository.NewWhitelistRepository(db)

	banService := service.NewBanService(tgBot, userRepo, banRepo, cacheSvc)
	userService := service.NewUserService(userRepo)
	notifyService := service.NewNotificationService(tgBot, settingsRepo)
	keywordService := service.NewKeywordService(cacheSvc, keywordRepo, bioRepo, userRepo, banService)
	adminService := service.NewAdminService(adminRepo, userRepo)
	whitelistService := service.NewWhitelistService(cacheSvc, whitelistRepo)
	userCleanupService := service.NewUserCleanupService(userRepo, cacheSvc, 30*24*time.Hour, 24*time.Hour)
	broadcastService := service.NewBroadcastService(tgBot, settingsRepo, userRepo)

	for _, authorID := range opts.LocalAuthorIDs {
		if authorID <= 0 {
			continue
		}
		if err := adminService.EnsureAuthor(authorID); err != nil {
			log.Printf("[Runtime] 写入作者 %d 失败: %v", authorID, err)
		}
	}

	detectorMgr := detector.NewDetectorManager(cacheSvc, bioRepo, tgBot)

	handler := handlers.NewHandler(
		tgBot, cfg,
		banService, keywordService, userService, notifyService, adminService, broadcastService,
		whitelistService,
		detectorMgr,
		userRepo, keywordRepo,
		settingsRepo, cloneBotRepo,
		cacheSvc,
		opts.IsMain,
		newRegistryAdapter(registry),
	)

	if err := keywordService.LoadAllKeywordsToCache(); err != nil {
		log.Printf("[Runtime] 加载关键词到缓存失败: %v", err)
	}

	if err := whitelistService.LoadToCache(); err != nil {
		log.Printf("[Runtime] 加载白名单到缓存失败: %v", err)
	}

	userCleanupService.Start()

	pool := NewWorkerPool(cfg.App.WorkerPoolSize)

	rt := &BotRuntime{
		BotUserID:      me.ID,
		BotUsername:    me.Username,
		IsMain:         opts.IsMain,
		Token:          opts.Token,
		DBPath:         opts.DBPath,
		CachePrefix:    cachePrefix,
		Bot:            tgBot,
		DB:             db,
		Cache:          cacheSvc,
		WorkerPool:     pool,
		Handler:        handler,
		BanService:     banService,
		BroadcastSvc:   broadcastService,
		NotifySvc:      notifyService,
		KeywordSvc:     keywordService,
		UserSvc:        userService,
		AdminSvc:       adminService,
		WhitelistSvc:   whitelistService,
		UserCleanupSvc: userCleanupService,
		SettingsRepo:   settingsRepo,
		CloneBotRepo:   cloneBotRepo,
		cfg:            cfg,
		registry:       registry,
	}

	rt.Monitor = NewRuntimeMonitor(rt, 60*time.Second)

	if registry != nil {
		registry.Add(rt)
		setupBanSync(rt, registry)
	}

	return rt, nil
}

// setupBanSync 为 rt 注入跨 Bot 封禁/解封同步回调
// 当 rt 封禁或解封用户时, 自动同步到 Registry 中所有其他运行中的 Bot
func setupBanSync(rt *BotRuntime, registry *Registry) {
	if rt.BanService == nil {
		return
	}
	selfID := rt.BotUserID
	rt.BanService.SetPeerSync(func(action string, userID int64, reason string) {
		for _, peer := range registry.List() {
			if peer.BotUserID == selfID || peer.BanService == nil {
				continue
			}
			switch action {
			case "ban":
				peer.BanService.MarkBanned(userID, reason)
				go peer.BanService.BanFromAllChats(userID)
				log.Printf("[封禁同步] 用户 %d 已同步封禁到 @%s (含全群踢出)", userID, peer.BotUsername)
			case "unban":
				peer.BanService.MarkUnbanned(userID)
				go peer.BanService.UnbanFromAllChats(userID)
				log.Printf("[封禁同步] 用户 %d 已同步解封到 @%s (含全群解除)", userID, peer.BotUsername)
			}
		}
	})
}

// Start 启动该 Bot: long-polling + handler + worker pool + monitor
// 非阻塞: 返回后内部会以 goroutine 运行, 调用 Stop() 才会真正停下
// telego v1.x 用 ctx 关闭长轮询(无 StopLongPolling),这里包一层 cancel 给 Stop() 使用
func (r *BotRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return fmt.Errorf("runtime 已启动")
	}
	r.started = true

	parentCtx := ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	lpCtx, cancel := context.WithCancel(parentCtx)
	r.lpCancel = cancel
	r.mu.Unlock()

	r.WorkerPool.Start()
	r.Monitor.Start()

	// AllowedUpdates 必须显式包含 chat_member / edited_message, 否则 Telegram 默认不推送;
	// 一旦设置 allowed_updates 就只推列出的类型, 因此 message/callback_query 也要一并列出
	updates, err := r.Bot.UpdatesViaLongPolling(lpCtx, &telego.GetUpdatesParams{
		Offset:  -1,
		Timeout: getUpdatesTimeout,
		AllowedUpdates: []string{
			"message",
			"edited_message",
			"callback_query",
			"chat_member",
		},
	})
	if err != nil {
		cancel()
		r.rollbackStart()
		return fmt.Errorf("启动 long-polling 失败: %w", err)
	}

	bh, err := th.NewBotHandler(r.Bot, updates)
	if err != nil {
		cancel()
		r.rollbackStart()
		return fmt.Errorf("创建 telego BotHandler 失败: %w", err)
	}

	r.mu.Lock()
	r.botHandler = bh
	r.mu.Unlock()

	r.registerHandlers(bh)

	go func() {
		if err := bh.Start(); err != nil {
			log.Printf("[Runtime] @%s bot handler 退出: %v", r.BotUsername, err)
		}
	}()

	if ctx != nil {
		go func() {
			<-ctx.Done()
			r.Stop()
		}()
	}

	log.Printf("[Runtime] @%s 启动成功 (id=%d, main=%v)", r.BotUsername, r.BotUserID, r.IsMain)
	return nil
}

// rollbackStart Start 失败时回滚: 恢复 started 标志、停止 Monitor/WorkerPool
func (r *BotRuntime) rollbackStart() {
	r.Monitor.Stop()
	r.WorkerPool.Stop()
	r.mu.Lock()
	r.started = false
	r.lpCancel = nil
	r.mu.Unlock()
}

// Stop 停止该 Bot 的 long-polling, 等待 worker pool 完成
func (r *BotRuntime) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	r.started = false
	cancel := r.lpCancel
	r.lpCancel = nil
	bh := r.botHandler
	r.botHandler = nil
	r.mu.Unlock()

	log.Printf("[Runtime] @%s 正在停止...", r.BotUsername)
	r.Monitor.Stop()
	if r.Handler != nil {
		r.Handler.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if bh != nil {
		if err := bh.Stop(); err != nil {
			log.Printf("[Runtime] @%s 停止 bot handler 失败: %v", r.BotUsername, err)
		}
	}
	r.WorkerPool.Stop()
	if r.registry != nil {
		r.registry.Remove(r.BotUserID)
	}
	log.Printf("[Runtime] @%s 已停止", r.BotUsername)
}

// registerHandlers 注册全部 telego handler(从原 bot.go 迁移过来,只调用 Runtime 内的依赖)
func (r *BotRuntime) registerHandlers(bh *th.BotHandler) {
	commands := []string{
		"start", "f", "t", "lh", "unlh", "jy", "unjy", "un",
		"name", "bio", "msg", "no", "z", "q", "admin", "reload", "add", "cancel",
	}

	for _, cmd := range commands {
		c := cmd
		bh.Handle(func(ctx *th.Context, update telego.Update) error {
			if update.Message == nil {
				return nil
			}
			msg := update.Message
			r.WorkerPool.Submit(func() {
				r.Handler.HandleCommand(msg)
			})
			return nil
		}, th.CommandEqual(c))
	}

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.CallbackQuery == nil {
			return nil
		}
		query := update.CallbackQuery
		r.WorkerPool.Submit(func() {
			go r.Handler.CollectUserInfo(&query.From)
			r.Handler.HandleCallback(query)
		})
		return nil
	}, th.AnyCallbackQuery())

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.Message == nil {
			return nil
		}
		msg := update.Message
		r.WorkerPool.Submit(func() {
			if msg.From != nil {
				go r.Handler.CollectUserInfo(msg.From)
			}

			r.Handler.ScheduleCommandCleanup(msg)

			if msg.Text == "f" {
				r.Handler.HandleCommand(msg)
				return
			}

			if isWhitelistTextCommand(msg.Text) {
				r.Handler.HandleCommand(msg)
				return
			}

			if msg.Chat.Type == "private" && msg.From != nil && r.Handler.IsAdmin(msg.From.ID) {
				r.Handler.HandleAdminMessage(msg)
				return
			}

			if msg.Chat.Type == "private" {
				return
			}

			if msg.Chat.Type == "supergroup" || msg.Chat.Type == "group" {
				r.Handler.HandleMessage(msg)
			}
		})
		return nil
	}, th.AnyMessage())

	// 编辑消息: 广告号常先发干净内容再改成广告, 必须走同一套检测
	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.EditedMessage == nil {
			return nil
		}
		msg := update.EditedMessage
		r.WorkerPool.Submit(func() {
			if msg.Chat.Type == "supergroup" || msg.Chat.Type == "group" {
				r.Handler.HandleMessage(msg)
			}
		})
		return nil
	}, th.AnyEditedMessage())

	// chat_member 更新: 覆盖邀请链接/管理员审批/加入请求等不产生"加入群组"系统消息的进群方式
	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.ChatMember == nil {
			return nil
		}
		cm := update.ChatMember
		r.WorkerPool.Submit(func() {
			r.Handler.HandleChatMemberUpdated(cm)
		})
		return nil
	}, th.AnyChatMember())
}

// SpawnClone /add 流程的核心: 校验 token, 启动 Runtime, 写 clone_bots 表
func (r *BotRuntime) SpawnClone(ctx context.Context, token string, localAdminID, createdBy int64) (*BotRuntime, error) {
	if r == nil || !r.IsMain {
		return nil, fmt.Errorf("仅主 Bot 支持创建克隆")
	}
	r.cloneOpsMu.Lock()
	defer r.cloneOpsMu.Unlock()
	if r.CloneBotRepo == nil {
		return nil, fmt.Errorf("主 Bot CloneBotRepo 未就绪")
	}
	if token == "" {
		return nil, fmt.Errorf("token 为空")
	}
	if localAdminID <= 0 {
		return nil, fmt.Errorf("管理员 ID 无效")
	}

	probe, err := newTelegramBot(token)
	if err != nil {
		return nil, fmt.Errorf("token 无效: %w", err)
	}
	me, err := probe.GetMe(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getMe 失败: %w", err)
	}
	if me.ID == r.BotUserID {
		return nil, fmt.Errorf("不可用主 Bot 的 token 创建克隆")
	}
	if existing, exists := r.registry.Get(me.ID); exists && existing != nil {
		return nil, fmt.Errorf("该 Bot 已经在运行 (id=%d)", me.ID)
	}
	if rec, _ := r.CloneBotRepo.GetByBotUserID(me.ID); rec != nil {
		return nil, fmt.Errorf("该 Bot 已注册过, 请到克隆列表中启用")
	}

	dbPath := clonePathFor(r.cfg, me.ID)
	cachePrefix := fmt.Sprintf("tgban:clone:%d:", me.ID)

	authors := []int64{localAdminID}
	if r.registry != nil && r.registry.MainAuthor() != 0 && r.registry.MainAuthor() != localAdminID {
		authors = append(authors, r.registry.MainAuthor())
	}

	cloneRT, err := NewRuntime(r.cfg, r.registry, RuntimeOptions{
		IsMain:         false,
		Token:          token,
		DBPath:         dbPath,
		CachePrefix:    cachePrefix,
		LocalAuthorIDs: authors,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化克隆 Runtime 失败: %w", err)
	}

	cloneRT.syncDataFromMain()

	rec := &models.CloneBot{
		BotUserID:    me.ID,
		BotUsername:  me.Username,
		Token:        token,
		LocalAdminID: localAdminID,
		DBPath:       dbPath,
		Enabled:      true,
		CreatedBy:    createdBy,
	}
	if err := r.CloneBotRepo.Create(rec); err != nil {
		cloneRT.Stop()
		return nil, fmt.Errorf("写入 clone_bots 失败: %w", err)
	}

	if err := cloneRT.Start(ctx); err != nil {
		// Start 失败时回滚: 删 clone_bots 记录, 停 runtime
		_ = r.CloneBotRepo.Delete(me.ID)
		cloneRT.Stop()
		return nil, fmt.Errorf("启动克隆 Runtime 失败: %w", err)
	}

	return cloneRT, nil
}

// EnableClone 启用一个已注册但未运行的克隆 Bot
func (r *BotRuntime) EnableClone(ctx context.Context, botUserID int64) error {
	if r == nil || !r.IsMain || r.CloneBotRepo == nil {
		return fmt.Errorf("仅主 Bot 支持启用克隆")
	}
	r.cloneOpsMu.Lock()
	defer r.cloneOpsMu.Unlock()
	if existing, ok := r.registry.Get(botUserID); ok && existing != nil {
		if err := r.CloneBotRepo.SetEnabled(botUserID, true); err != nil {
			return err
		}
		return nil
	}
	rec, err := r.CloneBotRepo.GetByBotUserID(botUserID)
	if err != nil {
		return fmt.Errorf("查找克隆失败: %w", err)
	}
	if err := r.CloneBotRepo.SetEnabled(botUserID, true); err != nil {
		return err
	}
	rec.Enabled = true
	cloneRT, err := r.bootClone(rec)
	if err != nil {
		return err
	}
	cloneRT.syncDataFromMain()
	if err := cloneRT.Start(ctx); err != nil {
		cloneRT.Stop()
		return fmt.Errorf("启动克隆 Bot 失败: %w", err)
	}
	return nil
}

// DisableClone 停用一个克隆 Bot(写入 enabled=false 并停止 Runtime)
func (r *BotRuntime) DisableClone(botUserID int64) error {
	if r == nil || !r.IsMain || r.CloneBotRepo == nil {
		return fmt.Errorf("仅主 Bot 支持停用克隆")
	}
	r.cloneOpsMu.Lock()
	defer r.cloneOpsMu.Unlock()
	if err := r.CloneBotRepo.SetEnabled(botUserID, false); err != nil {
		return err
	}
	if rt, ok := r.registry.Get(botUserID); ok && rt != nil {
		rt.Stop()
	}
	return nil
}

// bootClone 把 clone_bots 一行还原为内存 Runtime, 不启动 long-polling
func (r *BotRuntime) bootClone(rec *models.CloneBot) (*BotRuntime, error) {
	if rec == nil {
		return nil, fmt.Errorf("克隆记录为空")
	}
	cachePrefix := fmt.Sprintf("tgban:clone:%d:", rec.BotUserID)
	authors := []int64{rec.LocalAdminID}
	if r.registry != nil && r.registry.MainAuthor() != 0 && r.registry.MainAuthor() != rec.LocalAdminID {
		authors = append(authors, r.registry.MainAuthor())
	}
	return NewRuntime(r.cfg, r.registry, RuntimeOptions{
		IsMain:         false,
		Token:          rec.Token,
		DBPath:         rec.DBPath,
		CachePrefix:    cachePrefix,
		LocalAuthorIDs: authors,
	})
}

// RestoreClones 主 Bot 启动后, 把 clone_bots 中所有 enabled=true 的记录还原并启动
func (r *BotRuntime) RestoreClones(ctx context.Context) error {
	if r == nil || !r.IsMain || r.CloneBotRepo == nil {
		return nil
	}
	r.cloneOpsMu.Lock()
	defer r.cloneOpsMu.Unlock()
	records, err := r.CloneBotRepo.ListEnabled()
	if err != nil {
		return fmt.Errorf("列出已启用克隆失败: %w", err)
	}
	for i := range records {
		rec := records[i]
		if existing, ok := r.registry.Get(rec.BotUserID); ok && existing != nil {
			continue
		}
		cloneRT, err := r.bootClone(&rec)
		if err != nil {
			log.Printf("[Runtime] 恢复克隆 Bot %d 失败: %v", rec.BotUserID, err)
			continue
		}
		cloneRT.syncDataFromMain()
		if err := cloneRT.Start(ctx); err != nil {
			log.Printf("[Runtime] 克隆 Bot @%s 启动失败: %v", cloneRT.BotUsername, err)
			cloneRT.Stop()
			continue
		}
	}
	return nil
}

// syncDataFromMain 从主 Bot 同步关键词和白名单到当前 Runtime,
// 确保新建/恢复的克隆 Bot 拥有与主 Bot 一致的违规检测能力
func (r *BotRuntime) syncDataFromMain() {
	if r.IsMain || r.registry == nil {
		return
	}
	mainRT, ok := r.registry.Get(r.registry.MainBotID())
	if !ok || mainRT == nil {
		log.Printf("[同步] 找不到主 Bot Runtime，跳过数据同步")
		return
	}

	keywords, err := mainRT.KeywordSvc.GetAllKeywords()
	if err != nil {
		log.Printf("[同步] 获取主 Bot 关键词失败: %v", err)
	} else {
		synced := 0
		for _, kw := range keywords {
			if err := r.KeywordSvc.AddKeyword(models.KeywordType(kw.Type), kw.Word, kw.CreatedBy); err != nil {
				log.Printf("[同步] 同步关键词到 @%s 失败: type=%s word=%s err=%v",
					r.BotUsername, kw.Type, kw.Word, err)
			} else {
				synced++
			}
		}
		log.Printf("[同步] @%s 从主 Bot 同步了 %d/%d 个关键词", r.BotUsername, synced, len(keywords))
	}

	if mainRT.WhitelistSvc == nil || r.WhitelistSvc == nil {
		return
	}
	ids, err := mainRT.WhitelistSvc.GetAllUserIDs()
	if err != nil {
		log.Printf("[同步] 获取主 Bot 白名单失败: %v", err)
		return
	}
	wlSynced := 0
	for _, uid := range ids {
		if err := r.WhitelistSvc.Add(uid, r.registry.MainAuthor()); err != nil {
			log.Printf("[同步] 同步白名单用户 %d 到 @%s 失败: %v", uid, r.BotUsername, err)
		} else {
			wlSynced++
		}
	}
	log.Printf("[同步] @%s 从主 Bot 同步了 %d/%d 个白名单用户", r.BotUsername, wlSynced, len(ids))
}

// clonePathFor 计算克隆 Bot 的 SQLite 文件路径
func clonePathFor(cfg *config.Config, botUserID int64) string {
	dir := cfg.Database.CloneDir
	if dir == "" {
		dir = "data/clones"
	}
	return fmt.Sprintf("%s/clone-%d.db", dir, botUserID)
}

func newTelegramBot(token string) (*telego.Bot, error) {
	client := &fasthttp.Client{
		ReadTimeout:         telegramReadTimeout,
		WriteTimeout:        15 * time.Second,
		MaxIdleConnDuration: 90 * time.Second,
		MaxConnsPerHost:     128,
	}
	return telego.NewBot(token, telego.WithFastHTTPClient(client))
}

// isWhitelistTextCommand 判断消息是否为白名单相关的中文文本命令
func isWhitelistTextCommand(text string) bool {
	if text == "" {
		return false
	}
	return strings.HasPrefix(text, "加白") ||
		strings.HasPrefix(text, "移白") ||
		text == "白名单"
}
