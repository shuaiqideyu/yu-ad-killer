package utils

import (
	"log"
	"strings"
	"sync"

	"github.com/longbridgeapp/opencc"
	"golang.org/x/text/unicode/norm"
)

var (
	t2sOnce          sync.Once
	t2s              *opencc.OpenCC
	keywordFoldCache sync.Map
)

func t2sConverter() *opencc.OpenCC {
	t2sOnce.Do(func() {
		cc, err := opencc.New("t2s")
		if err != nil {
			log.Printf("[关键词] 简繁转换初始化失败,仅做大小写/兼容字折叠: %v", err)
			return
		}
		t2s = cc
	})
	return t2s
}

// MatchKeyword 关键词匹配：英文不区分大小写/全角，中文不区分简繁，兼容字按 Unicode 折叠
func MatchKeyword(text string, keywords []string) (bool, string) {
	if text == "" || len(keywords) == 0 {
		return false, ""
	}

	foldedText := KeywordFold(text)

	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		if strings.Contains(foldedText, cachedKeywordFold(keyword)) {
			return true, keyword
		}
	}

	return false, ""
}

// KeywordFold 把关键词/文本折成同一比较形态：NFKC 兼容字、繁体→简体、英文小写
func KeywordFold(s string) string {
	s = norm.NFKC.String(s)
	if cc := t2sConverter(); cc != nil {
		if out, err := cc.Convert(s); err == nil {
			s = out
		}
	}
	return strings.ToLower(s)
}

// cachedKeywordFold 缓存关键词折叠结果。词表稳定，消息正文不进这张表，避免无界增长。
func cachedKeywordFold(keyword string) string {
	if keyword == "" {
		return ""
	}
	if folded, ok := keywordFoldCache.Load(keyword); ok {
		return folded.(string)
	}
	folded := KeywordFold(keyword)
	actual, _ := keywordFoldCache.LoadOrStore(keyword, folded)
	return actual.(string)
}

// SameKeyword 两个词在折叠后是否视为同一关键词
func SameKeyword(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return KeywordFold(a) == KeywordFold(b)
}

// MatchAnyBio 匹配任意一个简介
func MatchAnyBio(bios []string, keywords []string) (bool, string, string) {
	for _, bio := range bios {
		if matched, keyword := MatchKeyword(bio, keywords); matched {
			return true, keyword, bio
		}
	}
	return false, "", ""
}
