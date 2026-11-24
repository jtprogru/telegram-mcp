// Package telegram implements bounded user-account MTProto operations and QR authentication.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/jtprogru/telegram-mcp/internal/credentials"
)

var errUnauthorized = errors.New("user session is not authorized")

// LoginUI is implemented only by an interactive terminal, never by a logger.
type LoginUI interface {
	QR(context.Context, string) error
	ClearQR() error
	Secret(context.Context, string) (string, error)
	Message(string) error
}

// SafeError removes low-level error context which can include RPC inputs.
// In particular QR tokens, API hashes, session data and passwords must not be logged.
func SafeError(ctx context.Context, err error) error {
	return safeTelegramError(ctx, err, "")
}

// loginStage is a fixed local label, never an RPC message or user input.
func safeTelegramError(ctx context.Context, err error, loginStage string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("telegram operation timed out; no interactive login was started automatically")
	}
	if wait, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Errorf("telegram rate limit: retry after %s", wait)
	}
	if rpc, ok := tgerr.As(err); ok && rpc.IsOneOf("AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "SESSION_EXPIRED", "AUTH_KEY_DUPLICATED", "USER_DEACTIVATED") {
		if loginStage != "" {
			// Only the allowlisted RPC type above is safe to display.
			return fmt.Errorf("QR login failed during %s: Telegram returned %s", loginStage, rpc.Type)
		}
		err = errUnauthorized
	}
	if errors.Is(err, errUnauthorized) {
		return errors.New("telegram session is no longer usable; stop the Python MCP and run telegram-mcp auth login --qr")
	}
	for _, known := range []struct{ code, message string }{
		{"CHANNEL_PRIVATE", "peer is private or no longer accessible to this account"},
		{"PEER_ID_INVALID", "peer could not be addressed; verify its ID/username and access permissions"},
		{"CHAT_WRITE_FORBIDDEN", "this account does not have permission to write to the peer"},
		{"CHAT_SEND_PLAIN_FORBIDDEN", "plain text messages are not permitted in this peer"},
		{"USER_PRIVACY_RESTRICTED", "the recipient's privacy settings prevent this operation"},
		{"MESSAGE_ID_INVALID", "message/reply ID is invalid or no longer visible in this peer"},
		{"MESSAGE_TOO_LONG", "Telegram rejected the formatted message as too long"},
		{"FILE_REFERENCE_EXPIRED", "media reference expired; retry the download with a fresh message lookup"},
		{"PEER_FLOOD", "Telegram restricted sending from this account; do not retry automatically"},
	} {
		if tgerr.Is(err, known.code) {
			return errors.New(known.message)
		}
	}
	if loginStage != "" {
		if rpc, ok := tgerr.As(err); ok {
			return fmt.Errorf("QR login failed during %s: Telegram RPC error %d (details suppressed)", loginStage, rpc.Code)
		}
		var dns *net.DNSError
		var netErr net.Error
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
			return fmt.Errorf("QR login failed during %s: connection closed", loginStage)
		case errors.As(err, &dns):
			return fmt.Errorf("QR login failed during %s: DNS lookup failed", loginStage)
		case errors.As(err, &netErr):
			return fmt.Errorf("QR login failed during %s: network error (%T)", loginStage, netErr)
		}
		// Type names identify library/decoder failures without printing values.
		cause := err
		for next := errors.Unwrap(cause); next != nil; next = errors.Unwrap(cause) {
			cause = next
		}
		return fmt.Errorf("QR login failed during %s: non-RPC error (%T; details suppressed)", loginStage, cause)
	}
	return errors.New("telegram operation failed; check connectivity/session and retry explicitly (secret-bearing details suppressed)")
}

// Login creates a fresh auth key; it never reuses an old session for replacement.
// Dispatcher registration precedes token export, avoiding the scan/handler race.
func Login(ctx context.Context, api credentials.API, staged *credentials.Session, ui LoginUI) error {
	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatcher)
	client := telegram.NewClient(api.ID, api.Hash, telegram.Options{
		SessionStorage: staged,
		UpdateHandler:  dispatcher,
		Device: telegram.DeviceConfig{
			DeviceModel: "telegram-mcp", AppVersion: "go",
		},
	})
	saved := false
	stage := "connection setup"
	err := client.Run(ctx, func(ctx context.Context) error {
		stage = "QR authorization"
		err := authorizeQR(ctx,
			func(ctx context.Context) error {
				qr := newLoginQR(client.API().Invoker(), api, client.MigrateTo, &stage)
				_, err := qr.Auth(ctx, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
					stage = "QR display"
					if err := ui.QR(ctx, token.URL()); err != nil {
						return err
					}
					stage = "waiting for QR scan"
					return nil
				})
				if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
					stage = "cloud password prompt"
				}
				return err
			},
			func(ctx context.Context, password string) error {
				stage = "cloud password verification"
				_, err := client.Auth().Password(ctx, password)
				return err
			}, ui)
		if err != nil {
			return err
		}
		stage = "account verification"
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		if self.Bot {
			return errors.New("a user-account session is required, not a bot")
		}
		stage = "session save"
		if err := staged.Commit(ctx); err != nil {
			return err
		}
		saved = true
		return nil
	})
	err = requireSaved(ctx, err, saved)
	// Clear even after a failed export, invalid password or cancellation.
	clearErr := ui.ClearQR()
	if err != nil {
		// Storage failures must remain distinguishable from network failures.
		if errors.Is(err, credentials.ErrUnavailable) {
			return errors.New("could not save session in macOS Keychain; login was not saved, previous session is unchanged")
		}
		return safeTelegramError(ctx, err, stage)
	}
	return clearErr
}

func authorizeQR(ctx context.Context, qr func(context.Context) error, password func(context.Context, string) error, ui LoginUI) error {
	err := qr(ctx)
	if !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return err
	}
	if err := ui.ClearQR(); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		secret, err := ui.Secret(ctx, "Telegram cloud password (hidden; Ctrl+C cancels): ")
		if err != nil {
			return err
		}
		err = password(ctx, secret)
		if !errors.Is(err, auth.ErrPasswordInvalid) && !tgerr.Is(err, "PASSWORD_HASH_INVALID") {
			return err
		}
		if err := ui.Message("Incorrect cloud password. Try again, or press Ctrl+C to cancel."); err != nil {
			return err
		}
	}
}

// Check verifies an existing/imported session online, then commits it to Go Keychain.
// It never starts login, sends messages, or marks any history as read.
func Check(ctx context.Context, api credentials.API, staged *credentials.Session) error {
	client := telegram.NewClient(api.ID, api.Hash, telegram.Options{
		SessionStorage: staged,
		NoUpdates:      true,
		Device: telegram.DeviceConfig{
			DeviceModel: "telegram-mcp", AppVersion: "go",
		},
	})
	var saveErr error
	saved := false
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized || status.User == nil || status.User.Bot {
			return errUnauthorized
		}
		saveErr = staged.Commit(ctx)
		saved = saveErr == nil
		return saveErr
	})
	err = requireSaved(ctx, err, saved)
	if saveErr != nil {
		return saveErr
	}
	return SafeError(ctx, err)
}

// gotd Run deliberately normalizes context.Canceled to nil. That is not proof
// that a login/check reached authorization and committed its staged session.
func requireSaved(ctx context.Context, runErr error, saved bool) error {
	if runErr == nil && !saved {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return context.Canceled
	}
	return runErr
}
