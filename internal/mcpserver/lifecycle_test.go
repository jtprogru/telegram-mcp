package mcpserver

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/telegram"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type blockingBackend struct{ started, stopped chan struct{} }

func (b *blockingBackend) Call(ctx context.Context, _ telegram.Request) (string, error) {
	close(b.started)
	<-ctx.Done()
	close(b.stopped)
	return "", ctx.Err()
}

func TestActiveCallCancelledOnEOFAndShutdown(t *testing.T) {
	for _, mode := range []string{"eof", "context"} {
		t.Run(mode, func(t *testing.T) {
			deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx, stop := context.WithCancel(deadline)
			defer stop()
			serverRead, clientWrite := io.Pipe()
			clientRead, serverWrite := io.Pipe()
			t.Cleanup(func() {
				for _, c := range []io.Closer{serverRead, clientWrite, clientRead, serverWrite} {
					if err := c.Close(); err != nil {
						t.Error(err)
					}
				}
			})
			b := &blockingBackend{started: make(chan struct{}), stopped: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				done <- RunStdio(ctx, b, config.Load(func(string) string { return "" }), serverRead, serverWrite)
			}()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			cs, err := client.Connect(deadline, &mcp.IOTransport{Reader: clientRead, Writer: clientWrite}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := cs.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
					t.Error(err)
				}
			}()
			callDone := make(chan struct{})
			go func() {
				defer close(callDone)
				_, _ = cs.CallTool(deadline, &mcp.CallToolParams{Name: "tg_me", Arguments: map[string]any{}})
			}() // The result/error depends on whether disconnect wins the race.
			select {
			case <-b.started:
			case <-deadline.Done():
				t.Fatal("tool did not start")
			}
			if mode == "eof" {
				if err := clientWrite.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				stop()
			}
			select {
			case <-b.stopped:
			case <-deadline.Done():
				t.Fatal("active call survived disconnect/shutdown")
			}
			select {
			case err := <-done:
				if mode == "eof" && err != nil {
					t.Errorf("EOF shutdown: %v", err)
				}
				if mode == "context" && !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation shutdown: %v", err)
				}
			case <-deadline.Done():
				t.Fatal("server failed to close")
			}
			select {
			case <-callDone:
			case <-deadline.Done():
				t.Fatal("client call leaked")
			}
		})
	}
}
