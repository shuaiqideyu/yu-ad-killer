package handlers

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mymmrac/telego"
)

func TestParseTgUserIDFromURL(t *testing.T) {
	cases := []struct {
		in     string
		wantID int64
		wantOK bool
	}{
		{"tg://user?id=8506857633", 8506857633, true},
		{"prefix tg://user?id=123 tail", 123, true},
		{"https://t.me/username", 0, false},
		{"tg://user?id=", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		gotID, gotOK := parseTgUserIDFromURL(c.in)
		if gotID != c.wantID || gotOK != c.wantOK {
			t.Fatalf("parseTgUserIDFromURL(%q)=(%d,%v) 期望 (%d,%v)", c.in, gotID, gotOK, c.wantID, c.wantOK)
		}
	}
}

func TestExtractUserIDFromNoticeEntities(t *testing.T) {
	msg := &telego.Message{
		Text: "枫迪丫. 触发水群检测，有问题联系管理员。",
		Entities: []telego.MessageEntity{
			{
				Type:   telego.EntityTypeTextLink,
				Offset: 0,
				Length: 4,
				URL:    "tg://user?id=8506857633",
			},
		},
	}
	id, ok := extractUserIDFromNoticeEntities(msg)
	if !ok || id != 8506857633 {
		t.Fatalf("extractUserIDFromNoticeEntities = (%d,%v), 期望 (8506857633,true)", id, ok)
	}

	mention := &telego.Message{
		Text: "用户 触发刷屏限制，有问题联系管理员。",
		Entities: []telego.MessageEntity{
			{
				Type:   telego.EntityTypeTextMention,
				Offset: 0,
				Length: 2,
				User:   &telego.User{ID: 42},
			},
		},
	}
	id, ok = extractUserIDFromNoticeEntities(mention)
	if !ok || id != 42 {
		t.Fatalf("text_mention = (%d,%v), 期望 (42,true)", id, ok)
	}
}

func TestNormalizeDuplicateWhitelistText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"原样紧凑", "双组2", "双组2"},
		{"ASCII 空格", "双组 2", "双组2"},
		{"多个空格", "双 组  2", "双组2"},
		{"前后空白", "  双组2  ", "双组2"},
		{"Tab 与换行", "双\t组\n2", "双组2"},
		{"NBSP", "双组\u00a02", "双组2"},
		{"全角空格", "双组\u30002", "双组2"},
		{"英文大小写", "YE", "ye"},
		{"英文 + 空格", "Y E", "ye"},
		{"纯空白返回空", " \t\u3000\u00a0", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeDuplicateWhitelistText(c.in)
			if got != c.want {
				t.Fatalf("normalizeDuplicateWhitelistText(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

// TestDuplicateWhitelistMatchSemantics 锁定"空白无关 + Contains"匹配语义。
// 真人输入"双组 2"应能命中紧凑白名单"双组2",避免误伤合法高频指令。
func TestIsContactCardMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  *telego.Message
		want bool
	}{
		{"空消息", nil, false},
		{"普通文字", &telego.Message{Text: "hello"}, false},
		{"自己的名片", &telego.Message{Contact: &telego.Contact{FirstName: "A", UserID: 1, PhoneNumber: "+1"}}, true},
		{"仅电话名片", &telego.Message{Contact: &telego.Contact{FirstName: "B", PhoneNumber: "+86138"}}, true},
		{"别人的名片", &telego.Message{Contact: &telego.Contact{FirstName: "C", LastName: "D", UserID: 99}}, true},
		{"vcf 扩展名", &telego.Message{Document: &telego.Document{FileName: "card.vcf"}}, true},
		{"vcard 扩展名", &telego.Message{Document: &telego.Document{FileName: "Card.VCARD"}}, true},
		{"vcard mime", &telego.Message{Document: &telego.Document{MimeType: "text/vcard"}}, true},
		{"x-vcard mime", &telego.Message{Document: &telego.Document{MimeType: "text/x-vcard"}}, true},
		{"普通文件", &telego.Message{Document: &telego.Document{FileName: "note.txt", MimeType: "text/plain"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isContactCardMessage(c.msg); got != c.want {
				t.Fatalf("isContactCardMessage(%s)=%v, 期望 %v", c.name, got, c.want)
			}
		})
	}
}

func TestNextForwardBurst(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name         string
		times        []time.Time
		wantTrigger  int
		wantNotifyOn int
	}{
		{
			name:         "同一秒满4条",
			times:        []time.Time{base, base, base, base},
			wantTrigger:  4,
			wantNotifyOn: 4,
		},
		{
			name:         "跨1秒仍算同一次",
			times:        []time.Time{base, base, base.Add(time.Second), base.Add(time.Second)},
			wantTrigger:  4,
			wantNotifyOn: 4,
		},
		{
			name:         "刚好2秒边界仍算",
			times:        []time.Time{base, base, base.Add(2 * time.Second), base.Add(2 * time.Second)},
			wantTrigger:  4,
			wantNotifyOn: 4,
		},
		{
			name: "乱序到达",
			times: []time.Time{
				base.Add(time.Second),
				base,
				base.Add(time.Second),
				base,
			},
			wantTrigger:  4,
			wantNotifyOn: 4,
		},
		{
			name: "每3秒一条不算",
			times: []time.Time{
				base,
				base.Add(3 * time.Second),
				base.Add(6 * time.Second),
				base.Add(9 * time.Second),
			},
			wantTrigger:  0,
			wantNotifyOn: 0,
		},
		{
			name: "3条后隔3秒不算",
			times: []time.Time{
				base, base, base, base.Add(3 * time.Second),
			},
			wantTrigger:  0,
			wantNotifyOn: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var history []forwardBurstHit
			triggeredTotal := 0
			notifyOn := 0
			for i, ts := range c.times {
				var triggered []forwardBurstHit
				var notify bool
				history, triggered, notify = nextForwardBurst(history, forwardBurstHit{
					MessageID: i + 1,
					SentAt:    ts,
				})
				triggeredTotal += len(triggered)
				if notify {
					if notifyOn != 0 {
						t.Fatalf("第 %d 条重复通知", i+1)
					}
					notifyOn = i + 1
				}
			}
			if triggeredTotal != c.wantTrigger || notifyOn != c.wantNotifyOn {
				t.Fatalf("触发 %d 条、第 %d 条通知，期望触发 %d 条、第 %d 条通知",
					triggeredTotal, notifyOn, c.wantTrigger, c.wantNotifyOn)
			}
		})
	}
}

func TestNextForwardBurstContinuesWithoutSecondNotice(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	var history []forwardBurstHit
	var notify bool
	for i := 1; i <= 4; i++ {
		var triggered []forwardBurstHit
		history, triggered, notify = nextForwardBurst(history, forwardBurstHit{MessageID: i, SentAt: base})
		if i < 4 && (len(triggered) != 0 || notify) {
			t.Fatalf("第 %d 条不应处罚", i)
		}
	}
	if !notify {
		t.Fatal("第 4 条应通知")
	}

	var triggered []forwardBurstHit
	history, triggered, notify = nextForwardBurst(history, forwardBurstHit{MessageID: 4, SentAt: base})
	if len(triggered) != 0 || notify {
		t.Fatal("同一条重放不应再处罚")
	}

	history, triggered, notify = nextForwardBurst(history, forwardBurstHit{MessageID: 5, SentAt: base})
	if len(triggered) != 1 || triggered[0].MessageID != 5 || notify {
		t.Fatalf("第 5 条只删不重复通知, got len=%d notify=%v", len(triggered), notify)
	}
	if len(history) != 5 {
		t.Fatalf("窗口应保留 5 条, got %d", len(history))
	}
}

func TestForwardBurstConcurrentInserts(t *testing.T) {
	h := &Handler{forwardBursts: make(map[userChatKey][]forwardBurstHit)}
	base := time.Unix(1_700_000_000, 0)
	key := userChatKey{ChatID: 9, UserID: 8}
	const n = 20

	var wg sync.WaitGroup
	var notifyCount atomic.Int32
	var triggeredCount atomic.Int32
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			h.forwardBurstMu.Lock()
			next, triggered, notify := nextForwardBurst(h.forwardBursts[key], forwardBurstHit{
				MessageID: id,
				SentAt:    base,
			})
			h.forwardBursts[key] = next
			h.forwardBurstMu.Unlock()
			triggeredCount.Add(int32(len(triggered)))
			if notify {
				notifyCount.Add(1)
			}
		}(i + 1)
	}
	wg.Wait()

	if got := len(h.forwardBursts[key]); got != n {
		t.Fatalf("窗口内应有 %d 条, got %d", n, got)
	}
	if triggeredCount.Load() != n || notifyCount.Load() != 1 {
		t.Fatalf("应处罚 %d 条且只通知 1 次, got 处罚 %d 通知 %d", n, triggeredCount.Load(), notifyCount.Load())
	}
}

func TestContactCardDescription(t *testing.T) {
	if got := contactCardDescription(&telego.Message{Contact: &telego.Contact{FirstName: "雨", PhoneNumber: "+1", UserID: 8}}); got != "雨 +1 (uid=8)" {
		t.Fatalf("contact description = %q", got)
	}
	if got := contactCardDescription(&telego.Message{Document: &telego.Document{FileName: "me.vcf"}}); got != "me.vcf" {
		t.Fatalf("vcard description = %q", got)
	}
}

func TestDuplicateWhitelistMatchSemantics(t *testing.T) {
	cases := []struct {
		name    string
		keyword string
		text    string
		want    bool
	}{
		{"完全相同", "双组2", "双组2", true},
		{"text 加空格", "双组2", "双组 2", true},
		{"keyword 加空格", "双 组2", "双组2", true},
		{"两边都加空格", "双 组 2", "双 组2", true},
		{"keyword 是 text 子串", "双组", "双组 2", true},
		{"text 全角空格", "双组2", "双组\u30002", true},
		{"完全不相关", "双组2", "今天天气真好", false},
		{"text 比 keyword 短不算 Contains", "双组2", "双组", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			normalizedKeyword := normalizeDuplicateWhitelistText(c.keyword)
			normalizedText := normalizeDuplicateWhitelistText(c.text)
			got := normalizedKeyword != "" && normalizedText != "" &&
				strings.Contains(normalizedText, normalizedKeyword)
			if got != c.want {
				t.Fatalf("匹配 keyword=%q text=%q = %v, 期望 %v", c.keyword, c.text, got, c.want)
			}
		})
	}
}
