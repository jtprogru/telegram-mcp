package config

import "testing"

func TestSettings(t *testing.T) {
	for _, tt := range []struct {
		name, list, marker, peer string
		allowed                  bool
	}{
		{"default saved", "", "", "me", true},
		{"default denies channel", "", "", "jtprogru_channel", false},
		{"explicit channel", "me,@Example", "", "https://t.me/EXAMPLE/", true},
		{"numeric", "-100123", "", "-100123", true},
		{"nonlisted", "me", "", "someone", false},
		{"no prefix match", "example", "", "example/42", false},
		{"no wildcard", "*", "", "anyone", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := Load(func(k string) string {
				if k == "TELEGRAM_MCP_SEND_ALLOWLIST" {
					return tt.list
				}
				return tt.marker
			})
			if s.SendAllowed(tt.peer) != tt.allowed || s.Marker != "🤖" {
				t.Fatal("unexpected settings")
			}
		})
	}
}
