package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/telegram"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recordBackend struct{ requests []telegram.Request }

func (b *recordBackend) Call(_ context.Context, r telegram.Request) (string, error) {
	b.requests = append(b.requests, r)
	return "offline-result", nil
}

func TestSevenToolsAndContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	backend := &recordBackend{}
	server := New(backend, config.Load(func(string) string { return "" }))
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ss.Close(); err != nil {
			t.Error(err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Error(err)
		}
	}()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("tools/list failed: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.Name == "tg_send" && (!strings.Contains(tool.Description, "CommonMark") || !strings.Contains(tool.Description, "Business") || tool.Annotations.ReadOnlyHint) {
			t.Fatal("send delivery/dialect/annotation contract missing")
		}
	}
	for _, name := range []string{"tg_me", "tg_dialogs", "tg_history", "tg_search", "tg_message", "tg_send", "tg_download"} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}
	for _, tt := range []struct {
		name         string
		args         map[string]any
		bad          bool
		defaultLimit int
	}{
		{"tg_me", map[string]any{}, false, 0},
		{"tg_dialogs", map[string]any{}, false, 50},
		{"tg_dialogs", map[string]any{"limit": nil}, false, 50},
		{"tg_history", map[string]any{"peer": "me"}, false, 50},
		{"tg_search", map[string]any{"query": "hello"}, false, 30},
		{"tg_send", map[string]any{"peer": "me", "text": "hi"}, false, 0},
		{"tg_send", map[string]any{"peer": "me", "text": "hi", "reply_to": nil}, false, 0},
		{"tg_send", map[string]any{"peer": "me", "text": "hi", "parse_mode": nil}, false, 0},
		{"tg_send", map[string]any{"peer": "other", "text": "hi"}, true, 0},
		{"tg_send", map[string]any{"peer": "me", "text": "hi", "reply_to": 0}, true, 0},
		{"tg_send", map[string]any{"peer": "me", "text": "hi", "parse_mode": "invalid"}, true, 0},
		{"tg_history", map[string]any{"peer": "me", "limit": 0}, true, 0},
		{"tg_history", map[string]any{"peer": "me", "limit": 201}, true, 0},
		{"tg_history", map[string]any{"peer": "me", "since": "2026-12-01", "until": "2026-11-01"}, true, 0},
		{"tg_search", map[string]any{"query": " "}, true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(backend.requests)
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
			if err != nil {
				t.Fatalf("protocol error instead of tool result: %v", err)
			}
			if result.IsError != tt.bad {
				t.Fatalf("unexpected isError=%v: %#v", result.IsError, result.Content)
			}
			if tt.bad {
				if len(backend.requests) != before {
					t.Fatal("invalid request reached backend")
				}
			} else {
				r := backend.requests[len(backend.requests)-1]
				if r.Limit != tt.defaultLimit {
					t.Fatal("default limit lost")
				}
				if tt.name == "tg_send" && r.ParseMode != "md" {
					t.Fatal("default parse_mode lost")
				}
			}
		})
	}
}
