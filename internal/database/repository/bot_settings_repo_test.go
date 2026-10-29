package repository

import (
	"path/filepath"
	"telegram-ban-bot/internal/database"
	"testing"
)

func newTestDB(t *testing.T) *BotSettingsRepository {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{IsMain: true})
	if err != nil {
		t.Fatalf("InitSQLite 失败: %v", err)
	}
	return NewBotSettingsRepository(db)
}

func TestBotSettings_DefaultIsZero(t *testing.T) {
	repo := newTestDB(t)
	got, err := repo.Get()
	if err != nil {
		t.Fatalf("Get 返回错误: %v", err)
	}
	if got.WorkGroupID != 0 || got.BroadcastChatID != 0 {
		t.Fatalf("默认期望全 0, 实际 work=%d broadcast=%d", got.WorkGroupID, got.BroadcastChatID)
	}
}

func TestBotSettings_SetAndClear(t *testing.T) {
	repo := newTestDB(t)

	if err := repo.SetWorkGroup(-1001234567); err != nil {
		t.Fatalf("SetWorkGroup err: %v", err)
	}
	if err := repo.SetBroadcastChat(-1009999999); err != nil {
		t.Fatalf("SetBroadcastChat err: %v", err)
	}
	got, err := repo.Get()
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if got.WorkGroupID != -1001234567 {
		t.Errorf("work 期望 -1001234567, 实际 %d", got.WorkGroupID)
	}
	if got.BroadcastChatID != -1009999999 {
		t.Errorf("broadcast 期望 -1009999999, 实际 %d", got.BroadcastChatID)
	}

	if err := repo.ClearWorkGroup(); err != nil {
		t.Fatalf("ClearWorkGroup err: %v", err)
	}
	got, _ = repo.Get()
	if got.WorkGroupID != 0 {
		t.Errorf("ClearWorkGroup 后期望 0, 实际 %d", got.WorkGroupID)
	}
	if got.BroadcastChatID != -1009999999 {
		t.Errorf("ClearWorkGroup 不应影响播报频道, 实际 %d", got.BroadcastChatID)
	}

	if err := repo.ClearBroadcastChat(); err != nil {
		t.Fatalf("ClearBroadcastChat err: %v", err)
	}
	got, _ = repo.Get()
	if got.BroadcastChatID != 0 {
		t.Errorf("ClearBroadcastChat 后期望 0, 实际 %d", got.BroadcastChatID)
	}
}

func TestBotSettings_GetReturnsCopy(t *testing.T) {
	repo := newTestDB(t)
	if err := repo.SetWorkGroup(-100); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get()
	if err != nil {
		t.Fatal(err)
	}
	got.WorkGroupID = 1
	again, err := repo.Get()
	if err != nil {
		t.Fatal(err)
	}
	if again.WorkGroupID != -100 {
		t.Fatalf("Get 应返回副本, work=%d", again.WorkGroupID)
	}
}

func TestBotSettings_DetectionFlagsDefaultOn(t *testing.T) {
	repo := newTestDB(t)
	got, err := repo.Get()
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if got.WaterDetectDisabled || got.SpamDetectDisabled || got.ForwardBurstDisabled {
		t.Fatalf("默认期望全部开启(零值), 实际 water=%v spam=%v forward=%v",
			got.WaterDetectDisabled, got.SpamDetectDisabled, got.ForwardBurstDisabled)
	}
}

func TestBotSettings_DetectionFlagsToggle(t *testing.T) {
	repo := newTestDB(t)

	if err := repo.SetWaterDetectDisabled(true); err != nil {
		t.Fatalf("SetWaterDetectDisabled err: %v", err)
	}
	if err := repo.SetSpamDetectDisabled(true); err != nil {
		t.Fatalf("SetSpamDetectDisabled err: %v", err)
	}
	if err := repo.SetForwardBurstDisabled(true); err != nil {
		t.Fatalf("SetForwardBurstDisabled err: %v", err)
	}
	got, _ := repo.Get()
	if !got.WaterDetectDisabled {
		t.Errorf("water 期望关闭, 实际开启")
	}
	if !got.SpamDetectDisabled {
		t.Errorf("spam 期望关闭, 实际开启")
	}
	if !got.ForwardBurstDisabled {
		t.Errorf("并发转发期望关闭, 实际开启")
	}

	if err := repo.SetWaterDetectDisabled(false); err != nil {
		t.Fatalf("恢复 water err: %v", err)
	}
	got, _ = repo.Get()
	if got.WaterDetectDisabled {
		t.Errorf("water 期望恢复开启, 实际仍关闭")
	}
	if !got.SpamDetectDisabled {
		t.Errorf("恢复 water 不应影响 spam, 实际 spam 已开启")
	}
	if !got.ForwardBurstDisabled {
		t.Errorf("恢复 water 不应影响并发转发, 实际并发转发已开启")
	}

	if err := repo.SetForwardBurstDisabled(false); err != nil {
		t.Fatalf("恢复并发转发 err: %v", err)
	}
	got, _ = repo.Get()
	if got.ForwardBurstDisabled {
		t.Errorf("并发转发期望恢复开启, 实际仍关闭")
	}
	if !got.SpamDetectDisabled {
		t.Errorf("恢复并发转发不应影响 spam, 实际 spam 已开启")
	}
}

func TestBotSettings_EnsureWorkGroupSeed(t *testing.T) {
	repo := newTestDB(t)

	if err := repo.EnsureWorkGroupSeed(0); err != nil {
		t.Fatalf("EnsureWorkGroupSeed(0) err: %v", err)
	}
	got, _ := repo.Get()
	if got.WorkGroupID != 0 {
		t.Errorf("seed=0 应保持 0, 实际 %d", got.WorkGroupID)
	}

	if err := repo.EnsureWorkGroupSeed(-1001111111); err != nil {
		t.Fatalf("EnsureWorkGroupSeed err: %v", err)
	}
	got, _ = repo.Get()
	if got.WorkGroupID != -1001111111 {
		t.Errorf("seed 写入后期望 -1001111111, 实际 %d", got.WorkGroupID)
	}

	// 再次 seed: 已有值时不应覆盖
	if err := repo.EnsureWorkGroupSeed(-1009999999); err != nil {
		t.Fatalf("EnsureWorkGroupSeed err: %v", err)
	}
	got, _ = repo.Get()
	if got.WorkGroupID != -1001111111 {
		t.Errorf("seed 应不覆盖已有值, 期望 -1001111111, 实际 %d", got.WorkGroupID)
	}
}
