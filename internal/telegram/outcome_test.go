package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGotdCancellationCannotReportSuccessfulLogin(t *testing.T) {
	for _, saved := range []bool{false, true} {
		err := requireSaved(context.Background(), nil, saved)
		if saved && err != nil {
			t.Fatal("confirmed session was rejected")
		}
		if !saved && !errors.Is(err, context.Canceled) {
			t.Fatal("gotd's nil cancellation outcome became login success")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := requireSaved(ctx, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatal("external cancellation was lost")
	}
}

func TestOperationOutcomeSurvivesGotdRunCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := finishOperation(ctx, "tg_me", "", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled/unrun read became success")
	}
	text, err := finishOperation(ctx, "tg_send", "", userError("UNKNOWN; inspect history"), nil)
	if err == nil || !strings.Contains(err.Error(), "UNKNOWN") || text != "" {
		t.Fatal("UNKNOWN was hidden by normalized Run error")
	}
	text, err = finishOperation(ctx, "tg_send", "sent [99]", nil, errors.New("DO-NOT-PRINT"))
	if err != nil || !strings.Contains(text, "sent [99]") || !strings.Contains(text, "do not resend") || strings.Contains(text, "DO-NOT-PRINT") {
		t.Fatal("acknowledged send became an apparent failure")
	}
}
