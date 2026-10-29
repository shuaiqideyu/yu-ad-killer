package utils

import "testing"

func TestRussianHit(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"张三 欢迎来玩", ""},
		{"Ivan Petrov", ""},
		{"Александр", "cyrillic"},
		{"From Moscow", "place"},
		{"现居莫斯科", "place"},
		{"I am Russian", "place"},
		{"合作 🇷🇺", "place"},
		{"Saint Petersburg", "place"},
		{"true", ""},
		{"peru", ""},
	}
	for _, tt := range tests {
		if got := RussianHit(tt.in); got != tt.want {
			t.Fatalf("RussianHit(%q) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestIsRussianLanguage(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"zh", false},
		{"en", false},
		{"ru", true},
		{"RU", true},
		{"ru-RU", true},
		{"rue", false},
	}
	for _, tt := range tests {
		if got := IsRussianLanguage(tt.in); got != tt.want {
			t.Fatalf("IsRussianLanguage(%q) = %v, 期望 %v", tt.in, got, tt.want)
		}
	}
}
