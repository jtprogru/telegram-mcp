package telegram

import (
	"context"
	"errors"
	"sync"
	"time"

	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/credentials"
	"github.com/jtprogru/telegram-mcp/internal/render"
)

type userError string

func (e userError) Error() string { return string(e) }

// Service lazily opens one bounded MTProto connection per authorized tool call.
// Listing tools and denied sends do not load credentials or contact Telegram.
// Calls serialize auth-key use, with a kernel lock excluding other Go processes.
type Service struct {
	Resolver credentials.Resolver
	Settings config.Settings
	once     sync.Once
	gate     chan struct{}
}

// Call validates a request before any secret access and never retries an operation itself.
func (s *Service) Call(ctx context.Context, r Request) (result string, err error) {
	if err := r.Validate(s.Settings); err != nil {
		return "", err
	}
	timeout := 60 * time.Second
	if r.Tool == "tg_download" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s.once.Do(func() { s.gate = make(chan struct{}, 1) })
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	lock, err := credentials.AcquireLock(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			if r.Tool == "tg_send" && result != "" && err == nil {
				result += "; lock cleanup failed, do not resend"
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	api, err := s.Resolver.LoadAPI(ctx)
	if err != nil {
		return "", err
	}
	staged, _, err := s.Resolver.PrepareSession(ctx)
	if err != nil {
		return "", err
	}
	client := gotd.NewClient(api.ID, api.Hash, gotd.Options{
		SessionStorage: staged, NoUpdates: true,
		// Negative disables RPC timeout resends (zero would restore gotd's default).
		// Protocol-required salt/connection retransmissions retain the SAME random_id.
		MaxRetries: -1,
		Device:     gotd.DeviceConfig{DeviceModel: "telegram-mcp", AppVersion: "go"},
	})
	var operationErr error
	err = client.Run(ctx, func(ctx context.Context) error {
		self, e := client.Self(ctx)
		if e != nil {
			return e
		}
		if self.Bot {
			return errUnauthorized
		}
		if e := staged.Commit(ctx); e != nil {
			operationErr = e
			return e
		}
		cat := render.Catalog{}
		cat.Add([]tg.UserClass{self}, nil)
		op := operation{api: client.API(), client: client, cat: cat, self: self, settings: s.Settings}
		result, operationErr = op.execute(ctx, r)
		return operationErr
	})
	return finishOperation(ctx, r.Tool, result, operationErr, err)
}

func finishOperation(ctx context.Context, tool, result string, operationErr, err error) (string, error) {
	// gotd Run can normalize callback/parent context.Canceled to nil. Preserve
	// the actual outcome and never interpret an unrun callback as success.
	if operationErr != nil {
		err = operationErr
	}
	if err == nil && result == "" {
		err = ctx.Err()
		if err == nil {
			err = errors.New("connection closed before the operation completed")
		}
	}
	if err != nil {
		// An acknowledged send must not become an apparently failed send just
		// because connection/session teardown reported another error.
		if tool == "tg_send" && result != "" && operationErr == nil {
			return result + "; connection/session teardown failed, do not resend", nil
		}
		if public, ok := errors.AsType[userError](operationErr); ok {
			return "", public
		}
		if errors.Is(operationErr, credentials.ErrUnavailable) {
			return "", errors.New("could not save Go session in macOS Keychain; no file fallback was used")
		}
		return "", SafeError(ctx, err)
	}
	return result, nil
}

type operation struct {
	api      *tg.Client
	client   *gotd.Client
	cat      render.Catalog
	self     *tg.User
	settings config.Settings
}

func (o *operation) execute(ctx context.Context, r Request) (string, error) {
	switch r.Tool {
	case "tg_me":
		return render.Fence(render.Dialog(o.cat[o.self.ID], 0)+"\nDelivery: direct MTProto from this user account, not Business/Bot API.", "me"), nil
	case "tg_dialogs":
		return o.dialogs(ctx, r)
	case "tg_history", "tg_search":
		return o.messages(ctx, r)
	case "tg_message":
		peer, id, err := render.ParseLink(r.Link)
		if err != nil {
			return "", err
		}
		e, err := o.resolve(ctx, peer)
		if err != nil {
			return "", err
		}
		m, err := o.message(ctx, e, id)
		if err != nil {
			return "", err
		}
		return render.Fence(render.Message(m, o.cat, true, false), peer), nil
	case "tg_send":
		return o.send(ctx, r)
	case "tg_download":
		return o.download(ctx, r)
	}
	return "", userError("unknown tool")
}
