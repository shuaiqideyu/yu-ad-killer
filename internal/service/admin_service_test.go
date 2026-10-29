package service

import (
	"path/filepath"
	"testing"

	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
)

func TestAdminCacheTracksAddAndRemove(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "admins.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	adminRepo := repository.NewAdminRepository(db)
	userRepo := repository.NewUserRepository(db, cache.NewCacheService("test:"))
	svc := NewAdminService(adminRepo, userRepo)

	if svc.IsAdmin(1) || svc.IsAuthor(1) {
		t.Fatal("空库不应有管理员")
	}
	if err := svc.EnsureAuthor(1); err != nil {
		t.Fatal(err)
	}
	if !svc.IsAdmin(1) || !svc.IsAuthor(1) {
		t.Fatal("作者应同时是管理员")
	}
	if err := svc.AddAdmin(2, models.RoleConfig, 1); err != nil {
		t.Fatal(err)
	}
	if !svc.IsAdmin(2) || svc.IsAuthor(2) {
		t.Fatal("配置管理员不应是作者")
	}
	if err := svc.RemoveAdmin(2, 1); err != nil {
		t.Fatal(err)
	}
	if svc.IsAdmin(2) {
		t.Fatal("移除后不应仍是管理员")
	}
	if !svc.IsAuthor(1) {
		t.Fatal("移除他人不应影响作者")
	}
}
