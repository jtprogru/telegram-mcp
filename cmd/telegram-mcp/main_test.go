package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("TELEGRAM_MCP_STDIO_TEST") == "1" {
		os.Args = []string{os.Args[0], "serve"}
		os.Exit(run())
	}
	os.Exit(m.Run())
}
func childCommand(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "TELEGRAM_MCP_STDIO_TEST=1", "TELEGRAM_MCP_SEND_ALLOWLIST=me")
	return cmd
}

func TestRealStdioLifecycle(t *testing.T) {
	for _, mode := range []string{"eof", "sigterm"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := childCommand(ctx)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Use the actual CLI/stdin/stdout, including legacy initialize negotiation.
			send := func(v any) {
				t.Helper()
				if err := json.NewEncoder(stdin).Encode(v); err != nil {
					t.Fatal(err)
				}
			}
			receive := json.NewDecoder(stdout)
			call := func(id int, method string, params any) map[string]json.RawMessage {
				t.Helper()
				send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
				var response map[string]json.RawMessage
				if err := receive.Decode(&response); err != nil {
					t.Fatalf("invalid stdout/protocol: %v stderr=%s", err, stderr.String())
				}
				return response
			}
			response := call(1, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "offline-test", "version": "1"}})
			if response["error"] != nil {
				t.Fatalf("initialize failed: %s", response["error"])
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
			response = call(2, "tools/list", map[string]any{})
			var listed struct{ Tools []struct{ Name string } }
			if err := json.Unmarshal(response["result"], &listed); err != nil || len(listed.Tools) != 7 {
				t.Fatal("stdio did not list seven tools")
			}
			response = call(3, "tools/call", map[string]any{"name": "tg_send", "arguments": map[string]any{"peer": "not-allowed", "text": "must never send"}})
			var result mcp.CallToolResult
			if err := json.Unmarshal(response["result"], &result); err != nil || !result.IsError {
				t.Fatal("denied send failed")
			}
			if mode == "eof" {
				if err := stdin.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			// A clean stdout must end at the last JSON frame, with no startup/exit chatter.
			rest, readErr := io.ReadAll(stdout)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.TrimSpace(string(rest)) != "" {
				t.Fatalf("non-protocol stdout: %q", rest)
			}
			waitErr := cmd.Wait()
			if mode == "eof" && waitErr != nil {
				t.Fatalf("EOF not graceful: %v stderr=%s", waitErr, stderr.String())
			}
			if ctx.Err() != nil {
				t.Fatal("stdio process did not shut down")
			}
			if mode == "sigterm" {
				if waitErr == nil {
					t.Fatal("expected cancellation exit status")
				}
			}
		})
	}
}
