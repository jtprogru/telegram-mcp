package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"
)

type fakeUI struct {
	attempts int
	prompts  int
	clears   int
	inputErr error
}

func (*fakeUI) QR(context.Context, string) error { return nil }
func (u *fakeUI) ClearQR() error                 { u.clears++; return nil }
func (u *fakeUI) Secret(context.Context, string) (string, error) {
	u.prompts++
	return "synthetic-password", u.inputErr
}
func (u *fakeUI) Message(string) error { u.attempts++; return nil }

func TestAuthorizeQR(t *testing.T) {
	for _, tt := range []struct {
		name        string
		qrErr       error
		wrong       int
		inputErr    error
		wantPrompts int
		wantErr     error
	}{
		{"without 2FA", nil, 0, nil, 0, nil},
		{"with 2FA", &tgerr.Error{Type: "SESSION_PASSWORD_NEEDED"}, 0, nil, 1, nil},
		{"retry wrong password", &tgerr.Error{Type: "SESSION_PASSWORD_NEEDED"}, 2, nil, 3, nil},
		{"cancel password", &tgerr.Error{Type: "SESSION_PASSWORD_NEEDED"}, 0, context.Canceled, 1, context.Canceled},
		{"cancel QR", context.Canceled, 0, nil, 0, context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := &fakeUI{inputErr: tt.inputErr}
			calls := 0
			err := authorizeQR(context.Background(), func(context.Context) error { return tt.qrErr }, func(context.Context, string) error {
				calls++
				if calls <= tt.wrong {
					return auth.ErrPasswordInvalid
				}
				return nil
			}, ui)
			if !errors.Is(err, tt.wantErr) || ui.prompts != tt.wantPrompts || ui.attempts != tt.wrong {
				t.Fatalf("unexpected auth: prompts=%d attempts=%d err=%v", ui.prompts, ui.attempts, err)
			}
		})
	}
}

func TestSafeErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"network", errors.New("DO-NOT-PRINT"), "secret-bearing details suppressed"},
		{"RPC context", &tgerr.Error{Type: "UNKNOWN", Message: "DO-NOT-PRINT"}, "secret-bearing details suppressed"},
		{"flood", &tgerr.Error{Type: "FLOOD_WAIT", Argument: 30}, "30s"},
		{"revoked", &tgerr.Error{Type: "SESSION_REVOKED", Message: "DO-NOT-PRINT"}, "auth login --qr"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := SafeError(context.Background(), tt.err)
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "DO-NOT-PRINT") {
				t.Fatalf("unsafe or unexpected error: %v", err)
			}
		})
	}
}
