package utils

import (
	"fmt"
	"testing"
)

func BenchmarkMatchKeyword500(b *testing.B) {
	keywords := make([]string, 500)
	for i := range keywords {
		keywords[i] = fmt.Sprintf("廣告詞%d", i)
	}
	keywords[100] = "加微信"
	text := "今天天气不错，欢迎加微信领取福利"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MatchKeyword(text, keywords)
	}
}

func BenchmarkRussianHitPlace(b *testing.B) {
	text := "现居莫斯科，欢迎来玩"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RussianHit(text)
	}
}
