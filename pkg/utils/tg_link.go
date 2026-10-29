package utils

import (
	"regexp"
	"strings"
)

const (
	TGLinkInvite = "invite"
	TGLinkUser   = "user"
)

// TGChatLink 是 t.me 上的一个群/频道入口。
// Kind=invite 时 Key 是邀请哈希（区分大小写）；Kind=user 时 Key 是小写用户名。
type TGChatLink struct {
	Kind string
	Key  string
}

var (
	tgHTTP       = regexp.MustCompile(`(?i)(?:https?://)?(?:t\.me|telegram\.me|telegram\.dog)/[^\s<>"'，。、）)\]\}]+`)
	tgJoin       = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9.])tg://join\?invite=([A-Za-z0-9_-]{8,64})`)
	tgResolve    = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9.])tg://resolve\?domain=([A-Za-z][A-Za-z0-9_]{2,31})`)
	inviteHashRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	tgUsernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,31}$`)
)

// 这些路径不是群或频道。
var reservedTGPaths = map[string]struct{}{
	"share": {}, "addstickers": {}, "addemoji": {}, "addtheme": {},
	"proxy": {}, "socks": {}, "iv": {}, "login": {}, "boost": {},
	"invoice": {}, "bg": {}, "setlanguage": {}, "confirmphone": {},
	"addlist": {}, "giftcode": {}, "contact": {}, "folder": {},
	"msg": {}, "apps": {}, "c": {},
}

var invisibleRunes = strings.NewReplacer(
	"\u200b", "",
	"\u200c", "",
	"\u200d", "",
	"\ufeff", "",
	"\u2060", "",
	"\u180e", "",
)

// ExtractTGChatLinks 从一段文本里抽出 Telegram 群链接和公开用户名链接。
// 同一邀请哈希或用户名只保留一次。
func ExtractTGChatLinks(text string) []TGChatLink {
	text = invisibleRunes.Replace(text)
	if text == "" || !mightHaveTGLink(text) {
		return nil
	}

	var out []TGChatLink
	seen := make(map[string]struct{})
	add := func(link TGChatLink) {
		if link.Key == "" {
			return
		}
		id := link.Kind + "\n" + link.Key
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, link)
	}

	for _, loc := range tgHTTP.FindAllStringIndex(text, -1) {
		if loc[0] > 0 && isTGHostContinue(text[loc[0]-1]) {
			continue
		}
		if link, ok := classifyTGPath(pathFromTGURL(text[loc[0]:loc[1]])); ok {
			add(link)
		}
	}
	for _, m := range tgJoin.FindAllStringSubmatch(text, -1) {
		add(TGChatLink{Kind: TGLinkInvite, Key: m[1]})
	}
	for _, m := range tgResolve.FindAllStringSubmatch(text, -1) {
		if name, ok := validTGUsername(m[1]); ok {
			add(TGChatLink{Kind: TGLinkUser, Key: name})
		}
	}
	return out
}

func mightHaveTGLink(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "t.me/") ||
		strings.Contains(lower, "telegram.me/") ||
		strings.Contains(lower, "telegram.dog/") ||
		strings.Contains(lower, "tg://")
}

func isTGHostContinue(prev byte) bool {
	return (prev >= 'A' && prev <= 'Z') ||
		(prev >= 'a' && prev <= 'z') ||
		(prev >= '0' && prev <= '9') ||
		prev == '.'
}

func pathFromTGURL(raw string) string {
	lower := strings.ToLower(raw)
	hosts := []string{
		"https://telegram.me/", "http://telegram.me/",
		"https://telegram.dog/", "http://telegram.dog/",
		"https://t.me/", "http://t.me/",
		"telegram.me/", "telegram.dog/", "t.me/",
	}
	for _, host := range hosts {
		if strings.HasPrefix(lower, host) {
			return raw[len(host):]
		}
	}
	return ""
}

func classifyTGPath(path string) (TGChatLink, bool) {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	path = strings.Trim(path, "/")
	path = strings.TrimRight(path, ".,;:!>)]}")
	if path == "" {
		return TGChatLink{}, false
	}
	if strings.HasPrefix(path, "+") {
		hash := path[1:]
		if inviteHashRe.MatchString(hash) {
			return TGChatLink{Kind: TGLinkInvite, Key: hash}, true
		}
		return TGChatLink{}, false
	}

	parts := strings.Split(path, "/")
	head := strings.ToLower(parts[0])
	if head == "joinchat" && len(parts) >= 2 && inviteHashRe.MatchString(parts[1]) {
		return TGChatLink{Kind: TGLinkInvite, Key: parts[1]}, true
	}
	if head == "s" && len(parts) >= 2 {
		if name, ok := validTGUsername(parts[1]); ok {
			return TGChatLink{Kind: TGLinkUser, Key: name}, true
		}
		return TGChatLink{}, false
	}
	if _, reserved := reservedTGPaths[head]; reserved {
		return TGChatLink{}, false
	}
	if name, ok := validTGUsername(parts[0]); ok {
		return TGChatLink{Kind: TGLinkUser, Key: name}, true
	}
	return TGChatLink{}, false
}

func validTGUsername(name string) (string, bool) {
	if !tgUsernameRe.MatchString(name) {
		return "", false
	}
	return strings.ToLower(name), true
}
