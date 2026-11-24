package telegram

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/jtprogru/telegram-mcp/internal/credentials"
)

// Exercise the gotd helper, including the second export triggered by updateLoginToken.
func TestQRConfirmationDiagnostic(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"pending token", nil, "token still pending"},
		{"broken connection", io.ErrUnexpectedEOF, "connection closed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			n := 0
			invoke := scriptedInvoker{func(context.Context, bin.Encoder) (bin.Encoder, error) {
				n++
				if n == 2 && tt.err != nil {
					return nil, tt.err
				}
				return &tg.AuthLoginToken{Expires: int(time.Now().Add(time.Minute).Unix()), Token: []byte("SECRET")}, nil
			}}
			signal := make(chan struct{}, 1)
			stage := "QR authorization"
			q := newLoginQR(invoke, credentials.API{ID: 1, Hash: "SECRET"}, func(context.Context, int) error { t.Fatal("unexpected migration"); return nil }, &stage)
			_, err := q.Auth(ctx, signal, func(context.Context, qrlogin.Token) error { signal <- struct{}{}; return nil })
			if n != 2 || err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("did not exercise the confirmation error: %d calls", n)
			}
			got := safeTelegramError(ctx, err, stage).Error()
			if !strings.Contains(got, "QR confirmation or refresh") || !strings.Contains(got, tt.want) || strings.Contains(got, "SECRET") {
				t.Fatalf("unhelpful or unsafe diagnostic: %s", got)
			}
		})
	}
}
