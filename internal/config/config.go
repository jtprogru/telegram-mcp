// Package config contains non-secret, environment-only behavior settings.
package config

import "strings"

const (
	AllowlistEnv  = "TELEGRAM_MCP_SEND_ALLOWLIST"
	MarkerEnv     = "TELEGRAM_MCP_AGENT_MARKER"
	DefaultMarker = "🤖"
)

// NormalizePeer canonicalizes a single peer reference, not a message URL.
func NormalizePeer(peer string) string {
	value := strings.ToLower(strings.TrimSpace(peer))
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/"} {
		if after, ok := strings.CutPrefix(value, prefix); ok {
			value = after
			break
		}
	}
	return strings.TrimPrefix(strings.Trim(value, "/"), "@")
}

// Settings is independent of credentials so an allowlist check never connects.
type Settings struct {
	Allowlist map[string]struct{}
	Marker    string
}

// Load uses a default allowlist of Saved Messages only; empty markers cannot disable it.
func Load(getenv func(string) string) Settings {
	raw := strings.TrimSpace(getenv(AllowlistEnv))
	if raw == "" {
		raw = "me"
	}
	s := Settings{Allowlist: make(map[string]struct{}), Marker: strings.TrimSpace(getenv(MarkerEnv))}
	if s.Marker == "" {
		s.Marker = DefaultMarker
	}
	for peer := range strings.SplitSeq(raw, ",") {
		if peer = NormalizePeer(peer); peer != "" {
			s.Allowlist[peer] = struct{}{}
		}
	}
	return s
}

// SendAllowed is a purely local gate and must precede credential loading.
func (s Settings) SendAllowed(peer string) bool {
	_, ok := s.Allowlist[NormalizePeer(peer)]
	return ok
}
