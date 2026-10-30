package detector

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const groupPreview = `<meta property="og:title" content="公群8810已押20000U 画家小额洗资">
<div class="tgme_page_extra">14 450 members, 1 174 online</div>`

const userPreview = `<meta property="og:title" content="小额洗资客服">
<div class="tgme_page_extra">@cashier</div>`

const invalidPreview = `<meta property="og:title" content="Join group chat on Telegram">`

func TestParseTGPreview(t *testing.T) {
	title, ok := parseTGPreview(groupPreview)
	if !ok || title != "公群8810已押20000U 画家小额洗资" {
		t.Fatalf("group = %q %v", title, ok)
	}
	if title, ok = parseTGPreview(`<meta property="og:title" content="A &amp; B 小额洗资"><div class="tgme_page_extra">3 subscribers</div>`); !ok || title != "A & B 小额洗资" {
		t.Fatalf("entity = %q %v", title, ok)
	}
	if _, ok = parseTGPreview(userPreview); ok {
		t.Fatal("user profile should not count as a chat")
	}
	if _, ok = parseTGPreview(invalidPreview); ok {
		t.Fatal("invalid invite should not count")
	}
}

func TestMessageInviteKeyword(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "AbcdEfgh12345678"):
			fmt.Fprint(w, groupPreview)
		case strings.Contains(r.URL.Path, "cashier"):
			fmt.Fprint(w, userPreview)
		default:
			fmt.Fprint(w, invalidPreview)
		}
	}))
	defer srv.Close()
	resolver := newInviteResolver(srv.Client(), srv.URL)
	c := cache.NewCacheService("t")
	msgDetector := &MessageDetector{cache: c, invites: resolver}
	link := "https://t.me/+AbcdEfgh12345678"

	hit, _, _, err := msgDetector.Check(&telego.Message{Text: link})
	if err != nil || hit || hits.Load() != 0 {
		t.Fatalf("no keyword: hit=%v err=%v lookups=%d", hit, err, hits.Load())
	}

	if err := c.AddKeyword(string(models.KeywordTypeInvite), "小额洗资"); err != nil {
		t.Fatal(err)
	}

	hit, banType, keyword, err := msgDetector.Check(&telego.Message{Text: "来 " + link})
	if err != nil || !hit || banType != string(models.BanTypeInvite) || keyword != "小额洗资" {
		t.Fatalf("message hit = %v %s %s err=%v", hit, banType, keyword, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("lookups = %d", hits.Load())
	}

	hit, _, _, err = msgDetector.Check(&telego.Message{Text: link})
	if err != nil || !hit || hits.Load() != 1 {
		t.Fatalf("cache miss: hit=%v lookups=%d err=%v", hit, hits.Load(), err)
	}

	buttonOnly := &telego.Message{
		ReplyMarkup: &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{
			tu.InlineKeyboardButton("进群").WithURL(link),
		}}},
	}
	hit, banType, keyword, err = msgDetector.Check(buttonOnly)
	if err != nil || !hit || banType != string(models.BanTypeInvite) || keyword != "小额洗资" {
		t.Fatalf("button hit = %v %s %s err=%v", hit, banType, keyword, err)
	}

	hidden := &telego.Message{
		Text: "点这里",
		Entities: []telego.MessageEntity{{
			Type: telego.EntityTypeTextLink,
			URL:  link,
		}},
	}
	hit, _, keyword, err = msgDetector.Check(hidden)
	if err != nil || !hit || keyword != "小额洗资" {
		t.Fatalf("hidden link hit = %v %s err=%v", hit, keyword, err)
	}

	hit, _, _, err = msgDetector.Check(&telego.Message{Text: "https://t.me/cashier"})
	if err != nil || hit {
		t.Fatalf("user profile hit = %v err=%v", hit, err)
	}

	descOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<meta property="og:title" content="每日闲聊"><meta property="og:description" content="这里有小额洗资"><div class="tgme_page_extra">12 members</div>`)
	}))
	defer descOnly.Close()
	descDetector := &MessageDetector{cache: c, invites: newInviteResolver(descOnly.Client(), descOnly.URL)}
	hit, _, _, err = descDetector.Check(&telego.Message{Text: "https://t.me/+DescOnlyHash1234"})
	if err != nil || hit {
		t.Fatalf("group description should not hit: hit=%v err=%v", hit, err)
	}
	if err := c.AddKeyword(string(models.KeywordTypeInvite), "闲聊"); err != nil {
		t.Fatal(err)
	}
	hit, banType, keyword, err = descDetector.Check(&telego.Message{Text: "https://t.me/+DescOnlyHash1234"})
	if err != nil || !hit || banType != string(models.BanTypeInvite) || keyword != "闲聊" {
		t.Fatalf("title hit = %v %s %s err=%v", hit, banType, keyword, err)
	}
}

func TestBioInviteLinkUsesGroupTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, groupPreview)
	}))
	defer srv.Close()

	c := cache.NewCacheService("bio")
	if err := c.AddKeyword(string(models.KeywordTypeInvite), "小额洗资"); err != nil {
		t.Fatal(err)
	}
	c.SetCachedBio(7, "https://t.me/+AbcdEfgh12345678")
	c.MarkBioSynced(7)

	bio := &BioDetector{cache: c, invites: newInviteResolver(srv.Client(), srv.URL)}
	hit, banType, keyword, err := bio.Check(&telego.Message{From: &telego.User{ID: 7}})
	if err != nil || !hit || banType != string(models.BanTypeInvite) || keyword != "小额洗资" {
		t.Fatalf("bio link = %v %s %s err=%v", hit, banType, keyword, err)
	}
}
