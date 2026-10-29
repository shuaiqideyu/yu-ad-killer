package detector

import (
	"testing"

	"github.com/mymmrac/telego"
)

func TestMatchBioTextRussian(t *testing.T) {
	tests := []struct {
		name       string
		bio        string
		keywords   []string
		wantHit    bool
		wantReason string
	}{
		{"empty", "", nil, false, ""},
		{"chinese", "欢迎来玩", []string{"广告"}, false, ""},
		{"russian bio no keywords", "Привет, заходи", nil, true, "简介含俄语"},
		{"mixed", "合作 Андрей", []string{"广告"}, true, "简介含俄语"},
		{"moscow latin", "From Moscow", nil, true, "简介含俄罗斯地区"},
		{"chinese city", "现居莫斯科", nil, true, "简介含俄罗斯地区"},
		{"latin not hit", "Hello Ivan", nil, false, ""},
		{"keyword still works", "加我广告群", []string{"广告"}, true, "广告"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit, reason := matchBioText(tt.bio, tt.keywords)
			if hit != tt.wantHit || reason != tt.wantReason {
				t.Fatalf("matchBioText() = (%v, %q), 期望 (%v, %q)", hit, reason, tt.wantHit, tt.wantReason)
			}
		})
	}
}

func TestProfileExtraText(t *testing.T) {
	chat := &telego.ChatFullInfo{
		BusinessLocation: &telego.BusinessLocation{Address: "Moscow, Russia"},
		BusinessIntro:    &telego.BusinessIntro{Title: "Shop", Message: "Hello"},
	}
	if got := profileExtraText(chat); got != "Moscow, Russia Shop Hello" {
		t.Fatalf("profileExtraText() = %q", got)
	}
}

func TestNameDetectorRussianName(t *testing.T) {
	d := NewNameDetector(nil)

	tests := []struct {
		name       string
		from       *telego.User
		wantHit    bool
		wantReason string
	}{
		{"nil from", nil, false, ""},
		{"russian", &telego.User{FirstName: "Александр", LastName: "Иванов"}, true, "俄罗斯名字"},
		{"cyrillic first only", &telego.User{FirstName: "Мария"}, true, "俄罗斯名字"},
		{"mixed", &telego.User{FirstName: "Alex", LastName: "Андрей"}, true, "俄罗斯名字"},
		{"language ru", &telego.User{FirstName: "Tom", LanguageCode: "ru"}, true, "账号语言为俄语"},
		{"name moscow", &telego.User{FirstName: "Moscow King"}, true, "昵称含俄罗斯地区"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit, reason, err := d.Check(&telego.Message{From: tt.from})
			if err != nil {
				t.Fatalf("Check() err = %v", err)
			}
			if hit != tt.wantHit || reason != tt.wantReason {
				t.Fatalf("Check() = (%v, %q), 期望 (%v, %q)", hit, reason, tt.wantHit, tt.wantReason)
			}
		})
	}
}
