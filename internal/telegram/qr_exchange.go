package telegram

import (
	"context"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"

	"github.com/jtprogru/telegram-mcp/internal/credentials"
)

// Use the same gotd QR flow and migration as Client.QR. Observe only the QR
// helper's synchronous invocations, not the client's background requests.
// Labels describe protocol steps and reply kinds, never their contents.
func newLoginQR(next tg.Invoker, api credentials.API, migrate func(context.Context, int) error, stage *string) qrlogin.QR {
	exported := false
	invoke := telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.AuthExportLoginTokenRequest:
			*stage = "QR token export"
			if exported {
				*stage = "QR confirmation or refresh"
			}
		case *tg.AuthImportLoginTokenRequest:
			*stage = "QR token import on the destination DC"
		}
		if err := next.Invoke(ctx, in, out); err != nil {
			return err
		}
		if box, ok := out.(*tg.AuthLoginTokenBox); ok {
			switch box.LoginToken.(type) {
			case *tg.AuthLoginToken:
				exported = true
				*stage += " (token still pending)"
			case *tg.AuthLoginTokenSuccess:
				*stage += " (authorization received)"
			}
		}
		return nil
	})
	return qrlogin.NewQR(tg.NewClient(invoke), api.ID, api.Hash, qrlogin.Options{
		Migrate: func(ctx context.Context, dc int) error {
			*stage = "QR data-center migration"
			return migrate(ctx, dc)
		},
	})
}
