package utils

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ParseUserID 解析用户ID（纯数字）
func ParseUserID(text string) (int64, bool) {
	text = strings.TrimSpace(text)
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// ParseUsername 解析用户名（@username 或 username）
func ParseUsername(text string) (string, bool) {
	text = strings.TrimSpace(text)

	// 移除 @ 前缀
	username := strings.TrimPrefix(text, "@")

	// 验证用户名格式（只包含字母、数字、下划线）
	if matched, _ := regexp.MatchString(`^[a-zA-Z0-9_]{5,32}$`, username); matched {
		return username, true
	}

	return "", false
}

// ParseUserLink 解析用户主页链接
func ParseUserLink(text string) (string, bool) {
	text = strings.TrimSpace(text)

	// 匹配 t.me/username 或 https://t.me/username
	patterns := []string{
		`^https?://t\.me/([a-zA-Z0-9_]{5,32})$`,
		`^t\.me/([a-zA-Z0-9_]{5,32})$`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(text); len(matches) > 1 {
			return matches[1], true
		}
	}

	return "", false
}

// GetFullName 获取用户全名
func GetFullName(firstName, lastName string) string {
	name := firstName
	if lastName != "" {
		name += " " + lastName
	}
	return name
}

// ContainsCyrillic 显示名是否含西里尔字母(俄文等)
func ContainsCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// FormatUserLink 格式化用户主页链接
func FormatUserLink(userID int64, name string) string {
	return "[" + name + "](tg://user?id=" + strconv.FormatInt(userID, 10) + ")"
}
