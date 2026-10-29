package utils

import "testing"

func TestContainsCyrillic(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"chinese", "张三", false},
		{"latin", "Ivan Petrov", false},
		{"russian first", "Александр", true},
		{"russian full", "Александр Иванов", true},
		{"mixed latin cyrillic", "Alex Андрей", true},
		{"ukrainian", "Іван", true},
		{"digits only", "12345", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsCyrillic(tt.in); got != tt.want {
				t.Fatalf("ContainsCyrillic(%q) = %v, 期望 %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestGetFullName(t *testing.T) {
	if got := GetFullName("Александр", "Иванов"); got != "Александр Иванов" {
		t.Fatalf("GetFullName() = %q", got)
	}
	if got := GetFullName("张三", ""); got != "张三" {
		t.Fatalf("GetFullName() = %q", got)
	}
}
