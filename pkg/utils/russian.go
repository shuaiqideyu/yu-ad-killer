package utils

import "strings"

var russianPlaceFolded []string

func init() {
	russianPlaceFolded = make([]string, 0, len(russianPlaceKeywords))
	for _, kw := range russianPlaceKeywords {
		if folded := KeywordFold(kw); folded != "" {
			russianPlaceFolded = append(russianPlaceFolded, folded)
		}
	}
}

// 中文群可识别的俄罗斯地区/国名；短词如 ru、perm 不收录，避免误伤。
var russianPlaceKeywords = []string{
	"russia", "russian", "россия", "российск",
	"俄罗斯", "俄国", "俄联邦",
	"🇷🇺",
	"moscow", "москва", "莫斯科",
	"petersburg", "петербург", "питер", "圣彼得堡",
	"novosibirsk", "новосибирск",
	"yekaterinburg", "екатеринбург",
	"казань", "kazan",
	"sochi", "сочи", "索契",
	"vladivostok", "владивосток", "符拉迪沃斯托克",
	"kaliningrad", "калининград",
	"krasnodar", "краснодар",
	"volgograd", "волгоград",
	"nizhny novgorod", "нижний новгород",
	"chelyabinsk", "челябинск",
	"самара",
	"омск",
	"ростов",
	"krasnoyarsk", "красноярск",
	"voronezh", "воронеж",
}

// RussianHit 资料文本里的俄罗斯元素: cyrillic / place / 空
func RussianHit(text string) string {
	if text == "" {
		return ""
	}
	if ContainsCyrillic(text) {
		return "cyrillic"
	}
	folded := KeywordFold(text)
	for _, kw := range russianPlaceFolded {
		if strings.Contains(folded, kw) {
			return "place"
		}
	}
	return ""
}

// IsRussianLanguage 客户端语言是否为俄语 (ru / ru-RU)
func IsRussianLanguage(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	return code == "ru" || strings.HasPrefix(code, "ru-")
}
