package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"telegram-ban-bot/internal/bot"
	"telegram-ban-bot/internal/config"
)

func main() {
	log.SetFlags(log.LstdFlags)
	log.Println("=== 雨の广告杀手 ===")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[致命错误] 配置加载失败: %v", err)
	}
	log.Println("[配置] 加载成功")

	registry := bot.NewRegistry()
	registry.SetMainAuthor(cfg.Bot.AdminID)

	mainCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mainRT, err := bot.NewRuntime(cfg, registry, bot.RuntimeOptions{
		IsMain:         true,
		Token:          cfg.Bot.Token,
		DBPath:         cfg.Database.Path,
		CachePrefix:    cfg.App.CachePrefix,
		LocalAuthorIDs: []int64{cfg.Bot.AdminID},
		WorkGroupSeed:  cfg.Bot.WorkGroupID,
	})
	if err != nil {
		log.Fatalf("[致命错误] 主 Bot 初始化失败: %v", err)
	}
	registry.SetMainBotID(mainRT.BotUserID)

	if err := mainRT.Start(mainCtx); err != nil {
		log.Fatalf("[致命错误] 主 Bot 启动失败: %v", err)
	}

	if err := mainRT.RestoreClones(mainCtx); err != nil {
		log.Printf("[警告] 恢复克隆 Bot 失败: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Println("[运行中] Bot已启动,按 Ctrl+C 停止")
	<-sigChan

	log.Println("[关闭] 收到停止信号, 关闭所有 Bot...")
	cancel()
	for _, rt := range registry.List() {
		rt.Stop()
	}
	log.Println("[已停止] 再见!")
}
