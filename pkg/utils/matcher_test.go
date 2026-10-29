package utils

import "testing"

func TestMatchKeywordEnglishFold(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		keywords []string
		want     bool
		wantKw   string
	}{
		{"小写命中大写词", "join vip group", []string{"VIP"}, true, "VIP"},
		{"大写命中小写词", "JOIN VIP GROUP", []string{"vip"}, true, "vip"},
		{"全角英文命中半角词", "加我ＶＩＰ群", []string{"vip"}, true, "vip"},
		{"半角词命中全角消息", "加我vip群", []string{"ＶＩＰ"}, true, "ＶＩＰ"},
		{"繁体命中简体词", "加我廣告群", []string{"广告"}, true, "广告"},
		{"简体命中繁体词", "加我广告群", []string{"廣告"}, true, "廣告"},
		{"异体群字", "进羣聊", []string{"群"}, true, "群"},
		{"中文不受影响", "加我广告群", []string{"广告"}, true, "广告"},
		{"不相关", "hello", []string{"vip"}, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, kw := MatchKeyword(c.text, c.keywords)
			if got != c.want || kw != c.wantKw {
				t.Fatalf("MatchKeyword(%q,%v)=(%v,%q), 期望 (%v,%q)", c.text, c.keywords, got, kw, c.want, c.wantKw)
			}
		})
	}
}

func TestSameKeyword(t *testing.T) {
	if !SameKeyword("VIP", "vip") {
		t.Fatal("VIP 与 vip 应视为同一词")
	}
	if !SameKeyword("广告", "廣告") {
		t.Fatal("广告 与 廣告 应视为同一词")
	}
	if SameKeyword("广告", "微信") {
		t.Fatal("广告 与 微信 不应视为同一词")
	}
}

func TestKeywordFoldCompatibilityIdeograph(t *testing.T) {
	// U+F90A 是兼容汉字 金，NFKC 可折到 金
	if KeywordFold("\uF90A") != KeywordFold("金") {
		t.Fatalf("兼容字未折叠: %q vs %q", KeywordFold("\uF90A"), KeywordFold("金"))
	}
}
