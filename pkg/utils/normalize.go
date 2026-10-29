package utils

import (
	"strings"
	"unicode"
)

// NormalizeLoose 去掉全部空白并转小写，供刷屏/水群白名单做空白无关匹配。
func NormalizeLoose(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	return strings.ToLower(s)
}
