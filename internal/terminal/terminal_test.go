package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// pty.Open's slave is not /dev/tty: on macOS poll works on the former
// and reports POLLNVAL on the latter. Run Open in a real foreground session.
func TestSecretFromControllingTTY(t *testing.T) {
	const helper = "TELEGRAM_MCP_TEST_CTTY"
	const label = "hidden: "
	const input = "synthetic-test-password"
	if os.Getenv(helper) == "1" {
		ui, err := Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := ui.Close(); err != nil {
				t.Error(err)
			}
		}()
		v, err := ui.Secret(t.Context(), label)
		if err != nil {
			t.Fatal(err)
		}
		if v != input {
			t.Fatal("hidden input mismatch")
		}
		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSecretFromControllingTTY$")
	cmd.Env = append(os.Environ(), helper+"=1")
	cmd.Stdout = io.Discard // keep test-runner output separate from /dev/tty
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cmd.Wait(); err != nil {
			t.Errorf("controlling-terminal prompt failed: %v", err)
		}
		if err := master.Close(); err != nil {
			t.Error(err)
		}
	}()
	prompt := make([]byte, len(label))
	if _, err := io.ReadFull(master, prompt); err != nil || string(prompt) != label {
		t.Fatal("missing password prompt")
	}
	if _, err := io.WriteString(master, input+"\r"); err != nil {
		t.Fatal(err)
	}
	// Drain the PTY while the child exits. Linux reports EIO on slave close.
	var tail strings.Builder
	if _, err := io.Copy(&tail, master); err != nil && !errors.Is(err, syscall.EIO) {
		t.Fatal(err)
	}
	if tail.String() != "\r\n" {
		t.Error("hidden input echoed or terminal was not restored")
	}
}

func TestSecretHiddenAndRestored(t *testing.T) {
	for _, tt := range []struct {
		name, input, want string
		cancel            bool
	}{
		{"input", "synthetic-password\r", "synthetic-password", false},
		{"backspace", "abc\x7fd\r", "abd", false},
		{"unicode", "абв\x7fг\r", "абг", false},
		{"CtrlC", "\x03", "", false},
		{"context cancellation", "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := master.Close(); err != nil {
					t.Error(err)
				}
				if err := slave.Close(); err != nil {
					t.Error(err)
				}
			})
			state, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type result struct {
				value string
				err   error
			}
			done := make(chan result, 1)
			go func() {
				v, err := (&UI{tty: slave}).Secret(ctx, "hidden: ")
				done <- result{v, err}
			}()
			// The label is written only once raw/no-echo mode is active.
			var label [8]byte
			if n, err := master.Read(label[:]); err != nil || n != len(label) {
				t.Fatalf("prompt not displayed: %v", err)
			}
			if tt.cancel {
				cancel()
			} else if _, err := master.Write([]byte(tt.input)); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				if got.value != tt.want || ((tt.cancel || tt.input == "\x03") && !errors.Is(got.err, context.Canceled)) {
					t.Fatalf("unexpected hidden input result: err=%v", got.err)
				}
				if tt.want != "" && got.err != nil {
					t.Fatal(got.err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("terminal reader did not cancel")
			}
			// Only the final newline should be emitted, never typed characters.
			var newline [128]byte
			if n, err := master.Read(newline[:]); err != nil || string(newline[:n]) != "\r\n" {
				t.Fatal("hidden input echoed or final newline failed")
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(state, after) {
				t.Fatal("terminal mode was not restored")
			}
		})
	}
}
