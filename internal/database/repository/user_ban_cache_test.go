package repository

import (
	"path/filepath"
	"testing"

	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database"
	"telegram-ban-bot/internal/database/models"
)

func TestBannedCacheFollowsWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "users.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewUserRepository(db, cache.NewCacheService("test:"))
	if err := repo.CreateOrUpdate(&models.User{ID: 42, Username: "u", FirstName: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.WarmBannedCache(); err != nil {
		t.Fatal(err)
	}

	banned, err := repo.IsBanned(42)
	if err != nil || banned {
		t.Fatalf("初始不应封禁, banned=%v err=%v", banned, err)
	}
	if err := repo.Ban(42, "测试"); err != nil {
		t.Fatal(err)
	}
	banned, err = repo.IsBanned(42)
	if err != nil || !banned {
		t.Fatalf("封禁后应为 true, banned=%v err=%v", banned, err)
	}
	if err := repo.Unban(42); err != nil {
		t.Fatal(err)
	}
	banned, err = repo.IsBanned(42)
	if err != nil || banned {
		t.Fatalf("解封后应为 false, banned=%v err=%v", banned, err)
	}

	if err := repo.Ban(42, "再封"); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteUserDataByIDs([]int64{42}); err != nil {
		t.Fatal(err)
	}
	banned, err = repo.IsBanned(42)
	if err != nil || banned {
		t.Fatalf("删除用户后不应仍视为封禁, banned=%v err=%v", banned, err)
	}
}

func TestTouchActivityMissingUser(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "users.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewUserRepository(db, cache.NewCacheService("test:"))
	if err := repo.CreateOrUpdate(&models.User{ID: 42, FirstName: "A"}); err != nil {
		t.Fatal(err)
	}
	ok, err := repo.TouchActivity(42)
	if err != nil || !ok {
		t.Fatalf("已有用户应刷新成功, ok=%v err=%v", ok, err)
	}
	ok, err = repo.TouchActivity(99)
	if err != nil || ok {
		t.Fatalf("不存在的用户不应刷新成功, ok=%v err=%v", ok, err)
	}
}

func TestBanMissingUserDoesNotStick(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "users.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewUserRepository(db, cache.NewCacheService("test:"))
	if err := repo.WarmBannedCache(); err != nil {
		t.Fatal(err)
	}
	if err := repo.Ban(99, "没有这行"); err != nil {
		t.Fatal(err)
	}
	banned, err := repo.IsBanned(99)
	if err != nil || banned {
		t.Fatalf("不存在的用户不应被记成封禁, banned=%v err=%v", banned, err)
	}
}
