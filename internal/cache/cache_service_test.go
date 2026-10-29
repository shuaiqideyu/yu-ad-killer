package cache

import (
	"fmt"
	"sync"
	"testing"
)

func TestMatchKeywordKeepsOriginalAndFold(t *testing.T) {
	s := NewCacheService("test:")
	if err := s.LoadKeywords("msg", []string{"廣告", "VIP"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		text string
		want string
		hit  bool
	}{
		{"加我广告群", "廣告", true},
		{"join vip", "VIP", true},
		{"你好", "", false},
	}
	for _, c := range cases {
		hit, kw := s.MatchKeyword("msg", c.text)
		if hit != c.hit || kw != c.want {
			t.Fatalf("MatchKeyword(%q)=(%v,%q), 期望 (%v,%q)", c.text, hit, kw, c.hit, c.want)
		}
	}

	if err := s.RemoveKeyword("msg", "VIP"); err != nil {
		t.Fatal(err)
	}
	if hit, _ := s.MatchKeyword("msg", "vip group"); hit {
		t.Fatal("删除后不应再命中 VIP")
	}
}

func TestDuplicateWhitelistNormalized(t *testing.T) {
	s := NewCacheService("test:")
	if err := s.LoadDuplicateWhitelistKeywords([]string{"双 组2"}); err != nil {
		t.Fatal(err)
	}
	if !s.MatchesDuplicateWhitelist("双组 2") {
		t.Fatal("带空格的发言应命中白名单")
	}
	if s.MatchesDuplicateWhitelist("今天天气真好") {
		t.Fatal("无关文本不应命中")
	}
	if err := s.RemoveDuplicateWhitelistKeyword("双 组2"); err != nil {
		t.Fatal(err)
	}
	if s.MatchesDuplicateWhitelist("双组2") {
		t.Fatal("删除后不应再命中")
	}
}

func TestChatAdminLookupDoesNotCopy(t *testing.T) {
	s := NewCacheService("test:")
	_, known := s.IsChatAdmin(-100, 7)
	if known {
		t.Fatal("空缓存不应 known")
	}
	s.SetChatAdmins(-100, map[int64]struct{}{7: {}})
	isAdmin, known := s.IsChatAdmin(-100, 7)
	if !known || !isAdmin {
		t.Fatalf("管理员应命中, known=%v isAdmin=%v", known, isAdmin)
	}
	isAdmin, known = s.IsChatAdmin(-100, 8)
	if !known || isAdmin {
		t.Fatalf("非管理员应 known 且不是管理员, known=%v isAdmin=%v", known, isAdmin)
	}
}

func TestBioSyncedFlag(t *testing.T) {
	s := NewCacheService("test:")
	s.SetCachedBio(1, "hello")
	bio, found, synced := s.GetCachedBio(1)
	if !found || synced || bio != "hello" {
		t.Fatalf("新缓存不应已对齐, found=%v synced=%v bio=%q", found, synced, bio)
	}
	s.MarkBioSynced(1)
	_, found, synced = s.GetCachedBio(1)
	if !found || !synced {
		t.Fatalf("标记后应已对齐, found=%v synced=%v", found, synced)
	}
}

func TestMatchKeywordConcurrentWithUpdates(t *testing.T) {
	s := NewCacheService("test:")
	if err := s.LoadKeywords("msg", []string{"广告"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				hit, kw := s.MatchKeyword("msg", "这是广告")
				if !hit || kw != "广告" {
					t.Errorf("并发匹配结果异常: hit=%v kw=%q", hit, kw)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			if err := s.AddKeyword("name", fmt.Sprintf("词%d", j)); err != nil {
				t.Errorf("添加关键词失败: %v", err)
			}
		}
	}()
	wg.Wait()
}

func BenchmarkCompiledMatch500(b *testing.B) {
	s := NewCacheService("bench:")
	words := make([]string, 500)
	for i := range words {
		words[i] = fmt.Sprintf("廣告詞%d", i)
	}
	words[100] = "加微信"
	if err := s.LoadKeywords("msg", words); err != nil {
		b.Fatal(err)
	}
	text := "今天天气不错，欢迎加微信领取福利"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.MatchKeyword("msg", text)
	}
}
