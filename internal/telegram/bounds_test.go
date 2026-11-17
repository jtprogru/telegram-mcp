package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestFilteredDialogsScanCap(t *testing.T) {
	calls := 0
	o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
		q := in.(*tg.MessagesGetDialogsRequest)
		calls++
		if calls > 5 || q.Limit != 100 {
			t.Fatal("dialog scan is not bounded")
		}
		if calls > 1 && (!q.ExcludePinned || q.OffsetID != (calls-1)*100) {
			t.Fatal("dialog continuation is wrong")
		}
		page := &tg.MessagesDialogsSlice{}
		for i := 1; i <= 100; i++ {
			id := int64((calls-1)*100 + i)
			page.Users = append(page.Users, &tg.User{ID: id, AccessHash: id + 500, FirstName: "User"})
			page.Dialogs = append(page.Dialogs, &tg.Dialog{Peer: &tg.PeerUser{UserID: id}, TopMessage: int(id)})
			page.Messages = append(page.Messages, &tg.Message{ID: int(id), PeerID: &tg.PeerUser{UserID: id}, Date: 1700000000 - int(id)})
		}
		return page, nil
	})
	text, err := o.dialogs(context.Background(), Request{Tool: "tg_dialogs", Query: "not present", Limit: 1})
	if err != nil || calls != 5 || !strings.Contains(text, "scan capped") {
		t.Fatalf("scan cap failed: %v", err)
	}
}

func TestHistoryExclusiveIDsInclusiveSinceExclusiveUntil(t *testing.T) {
	// Telegram boundaries are also enforced locally; malformed/repeated server
	// pages cannot leak data outside the documented request window.
	base := 1700000000
	o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
		q := in.(*tg.MessagesGetHistoryRequest)
		if q.OffsetDate != base+20 {
			t.Fatal("until not passed to Telegram")
		}
		ms := []tg.MessageClass{}
		for id := 21; id >= 9; id-- {
			ms = append(ms, &tg.Message{ID: id, Date: base + id, PeerID: &tg.PeerUser{UserID: 7}, Message: "body"})
		}
		return &tg.MessagesMessages{Messages: ms}, nil
	})
	text, err := o.messages(context.Background(), Request{Tool: "tg_history", Peer: "me", Limit: 50, MinID: 10, MaxID: 21, Since: time.Unix(int64(base+11), 0).UTC().Format(time.RFC3339), Until: time.Unix(int64(base+20), 0).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "[11]") || !strings.Contains(text, "[19]") {
		t.Fatal("valid boundary messages omitted")
	}
	for _, outside := range []string{"[9]", "[10]", "[20]", "[21]"} {
		if strings.Contains(text, outside) {
			t.Fatal("history boundary leaked")
		}
	}
}

func TestMissingReplyDoesNotSend(t *testing.T) {
	calls := 0
	o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
		calls++
		if _, ok := in.(*tg.MessagesGetMessagesRequest); !ok {
			t.Fatal("attempted to send without an existing reply target")
		}
		return &tg.MessagesMessages{}, nil
	})
	if _, err := o.send(context.Background(), Request{Tool: "tg_send", Peer: "me", Text: "reply", ParseMode: "none", ReplyTo: new(12)}); err == nil || calls != 1 {
		t.Fatal("missing reply was not rejected")
	}
}
