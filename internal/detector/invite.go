package detector

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/pkg/utils"

	"github.com/mymmrac/telego"
)

const (
	inviteLookupTimeout = 3 * time.Second
	inviteHitTTL        = 6 * time.Hour
	inviteMissTTL       = 10 * time.Minute
	maxInviteLookups    = 3
	inviteBodyLimit     = 64 * 1024
	inviteCacheCap      = 2000
	// 同时只放 4 个预览请求。忙时跳过本次，避免把消息 worker 堵在排队上。
	inviteParallel = 4
)

var (
	ogTitleMeta = regexp.MustCompile(`(?i)<meta\s+[^>]*property="og:title"[^>]*>`)
	metaContent = regexp.MustCompile(`(?i)\bcontent="([^"]*)"`)
	pageExtra   = regexp.MustCompile(`(?is)<div class="tgme_page_extra"[^>]*>(.*?)</div>`)
	htmlTag     = regexp.MustCompile(`<[^>]*>`)

	sharedInviteResolver = newInviteResolver(nil, "")
)

type inviteCacheItem struct {
	title string
	ok    bool
	until time.Time
}

// InviteResolver 把 t.me 链接解析成群或频道名称。只取群名，不取群简介。结果只留在进程内存里。
type InviteResolver struct {
	client *http.Client
	base   string
	sem    chan struct{}
	mu     sync.Mutex
	items  map[string]inviteCacheItem
	lastOK atomic.Int64
}

func newInviteResolver(client *http.Client, base string) *InviteResolver {
	if client == nil {
		client = &http.Client{Timeout: inviteLookupTimeout + time.Second}
	}
	return &InviteResolver{
		client: client,
		base:   base,
		sem:    make(chan struct{}, inviteParallel),
		items:  make(map[string]inviteCacheItem),
	}
}

func (r *InviteResolver) Title(link utils.TGChatLink) (string, bool) {
	if r == nil || link.Key == "" {
		return "", false
	}
	key := link.Kind + "\n" + link.Key
	if title, ok, fresh := r.cached(key); fresh {
		return title, ok
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		log.Printf("[群名链接] 解析忙，跳过 key=%s 上次成功=%s", link.Key, r.lastOKText())
		return "", false
	}
	if title, ok, fresh := r.cached(key); fresh {
		return title, ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), inviteLookupTimeout)
	defer cancel()
	title, err := r.fetch(ctx, link)
	if err != nil {
		r.store(key, "", false, inviteMissTTL)
		log.Printf("[群名链接] 解析失败 key=%s err=%v 上次成功=%s", link.Key, err, r.lastOKText())
		return "", false
	}
	r.lastOK.Store(time.Now().Unix())
	if title == "" {
		r.store(key, "", false, inviteMissTTL)
		return "", false
	}
	r.store(key, title, true, inviteHitTTL)
	return title, true
}

func (r *InviteResolver) cached(key string) (string, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[key]
	if !ok || time.Now().After(item.until) {
		return "", false, false
	}
	return item.title, item.ok, true
}

func (r *InviteResolver) store(key, title string, ok bool, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if len(r.items) >= inviteCacheCap {
		for k, item := range r.items {
			if now.After(item.until) {
				delete(r.items, k)
			}
		}
	}
	if len(r.items) >= inviteCacheCap {
		r.items = make(map[string]inviteCacheItem, 64)
	}
	r.items[key] = inviteCacheItem{title: title, ok: ok, until: now.Add(ttl)}
}

func (r *InviteResolver) lastOKText() string {
	sec := r.lastOK.Load()
	if sec == 0 {
		return "无"
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

func (r *InviteResolver) fetch(ctx context.Context, link utils.TGChatLink) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.previewURL(link), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept-Language", "en")
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, inviteBodyLimit))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", &inviteStatusError{code: resp.StatusCode}
	}
	title, isChat := parseTGPreview(string(body))
	if !isChat {
		return "", nil
	}
	return title, nil
}

type inviteStatusError struct{ code int }

func (e *inviteStatusError) Error() string {
	return fmt.Sprintf("预览页状态 %d", e.code)
}

func (r *InviteResolver) previewURL(link utils.TGChatLink) string {
	base := r.base
	if base == "" {
		base = "https://t.me"
	}
	if link.Kind == utils.TGLinkInvite {
		return base + "/+" + link.Key
	}
	return base + "/" + link.Key
}

func parseTGPreview(body string) (string, bool) {
	tag := ogTitleMeta.FindString(body)
	if tag == "" {
		return "", false
	}
	content := metaContent.FindStringSubmatch(tag)
	if content == nil {
		return "", false
	}
	title := strings.TrimSpace(html.UnescapeString(content[1]))
	title = strings.TrimSpace(htmlTag.ReplaceAllString(title, ""))
	if !usableChatTitle(title) {
		return "", false
	}
	extra := ""
	if m := pageExtra.FindStringSubmatch(body); m != nil {
		extra = strings.ToLower(htmlTag.ReplaceAllString(html.UnescapeString(m[1]), ""))
	}
	if !isGroupOrChannel(extra) {
		return "", false
	}
	return title, true
}

func usableChatTitle(title string) bool {
	if title == "" {
		return false
	}
	switch strings.ToLower(title) {
	case "telegram", "join group chat on telegram", "join channel on telegram":
		return false
	default:
		return true
	}
}

func isGroupOrChannel(extra string) bool {
	return strings.Contains(extra, "member") ||
		strings.Contains(extra, "subscriber") ||
		strings.Contains(extra, "成员") ||
		strings.Contains(extra, "订阅")
}

func matchInviteKeyword(c *cache.CacheService, resolver *InviteResolver, blobs ...string) (bool, string) {
	if c == nil || resolver == nil || !c.HasKeywords(string(models.KeywordTypeInvite)) {
		return false, ""
	}
	var links []utils.TGChatLink
	seen := make(map[string]struct{})
	for _, blob := range blobs {
		for _, link := range utils.ExtractTGChatLinks(blob) {
			id := link.Kind + "\n" + link.Key
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			links = append(links, link)
			if len(links) >= maxInviteLookups {
				break
			}
		}
		if len(links) >= maxInviteLookups {
			break
		}
	}
	for _, link := range links {
		title, ok := resolver.Title(link)
		if !ok || title == "" {
			continue
		}
		if hit, keyword := c.MatchKeyword(string(models.KeywordTypeInvite), title); hit {
			log.Printf("[群名链接] 命中 keyword=%s title=%s", keyword, title)
			return true, keyword
		}
	}
	return false, ""
}

func messageLinkBlobs(message *telego.Message) []string {
	if message == nil {
		return nil
	}
	blobs := make([]string, 0, 4)
	if message.Text != "" {
		blobs = append(blobs, message.Text)
	}
	if message.Caption != "" {
		blobs = append(blobs, message.Caption)
	}
	blobs = appendEntityURLs(blobs, message.Entities)
	blobs = appendEntityURLs(blobs, message.CaptionEntities)
	if message.ReplyMarkup != nil {
		for _, row := range message.ReplyMarkup.InlineKeyboard {
			for _, btn := range row {
				if btn.URL != "" {
					blobs = append(blobs, btn.URL)
				}
			}
		}
	}
	return blobs
}

func appendEntityURLs(blobs []string, entities []telego.MessageEntity) []string {
	for _, entity := range entities {
		if entity.Type == telego.EntityTypeTextLink && entity.URL != "" {
			blobs = append(blobs, entity.URL)
		}
	}
	return blobs
}
