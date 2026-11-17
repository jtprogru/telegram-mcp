package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestDialogsContinueAcrossShortPages(t *testing.T) {
	for _, mode := range []string{"dialogs", "numeric resolution"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
				q := in.(*tg.MessagesGetDialogsRequest)
				folder, _ := q.GetFolderID()
				if folder != 0 || calls >= 2 {
					t.Fatal("left the main folder before exhausting its short pages")
				}
				if calls == 1 && (q.OffsetID != 11 || !q.ExcludePinned) {
					t.Fatal("dialog cursor lost")
				}
				page := &tg.MessagesDialogsSlice{Count: 4}
				for id := 12 - calls*2; id > 10-calls*2; id-- {
					peer := &tg.PeerUser{UserID: int64(id)}
					page.Users = append(page.Users, &tg.User{ID: int64(id), AccessHash: int64(id + 100), FirstName: fmt.Sprintf("User %d", id)})
					page.Dialogs = append(page.Dialogs, &tg.Dialog{Peer: peer, TopMessage: id})
					page.Messages = append(page.Messages, &tg.Message{ID: id, Date: 1700000000 + id, PeerID: peer})
				}
				calls++
				return page, nil
			})
			if mode == "dialogs" {
				text, err := o.dialogs(context.Background(), Request{Tool: "tg_dialogs", Limit: 3})
				if err != nil || calls != 2 || !strings.Contains(text, "User 10") {
					t.Fatalf("short dialog page ended iteration: calls=%d err=%v", calls, err)
				}
			} else {
				e, err := o.resolve(context.Background(), "10")
				if err != nil || calls != 2 || e.ID != 10 {
					t.Fatalf("short dialog page prevented resolution: calls=%d err=%v", calls, err)
				}
			}
		})
	}
}

func TestMessagesContinueAcrossShortPages(t *testing.T) {
	for _, mode := range []string{"history", "peer search", "global search"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
				var offset int
				switch q := in.(type) {
				case *tg.MessagesGetHistoryRequest:
					offset = q.OffsetID
				case *tg.MessagesSearchRequest:
					offset = q.OffsetID
				case *tg.MessagesSearchGlobalRequest:
					offset = q.OffsetID
					if calls > 0 && q.OffsetRate != offset*10 {
						t.Fatal("global next_rate lost across a short page")
					}
				default:
					t.Fatalf("unexpected RPC %T", in)
				}
				if calls >= 3 {
					t.Fatal("pagination did not stop at the requested limit")
				}
				first := 6 - calls*2
				if calls > 0 && offset != first+1 {
					t.Fatalf("wrong continuation offset %d", offset)
				}
				calls++
				page := &tg.MessagesMessagesSlice{Count: 6, Messages: []tg.MessageClass{msg(first), msg(first - 1)}}
				page.SetNextRate((first - 1) * 10)
				return page, nil
			})
			r := Request{Tool: "tg_history", Peer: "me", Limit: 6}
			if mode != "history" {
				r.Tool, r.Query = "tg_search", "body"
			}
			if mode == "global search" {
				r.Peer = ""
			}
			text, err := o.messages(context.Background(), r)
			if err != nil || calls != 3 {
				t.Fatalf("short page ended iteration: calls=%d err=%v", calls, err)
			}
			for id := 1; id <= 6; id++ {
				if !strings.Contains(text, fmt.Sprintf("body %d", id)) {
					t.Fatalf("message %d omitted", id)
				}
			}
		})
	}
}

func TestHistoryContinuesAfterEmptyMessages(t *testing.T) {
	calls := 0
	o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
		q := in.(*tg.MessagesGetHistoryRequest)
		calls++
		switch calls {
		case 1:
			return &tg.MessagesMessagesSlice{Count: 2, Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 5}}}, nil
		case 2:
			if q.OffsetID != 5 {
				t.Fatalf("empty-message cursor lost: offset=%d", q.OffsetID)
			}
			return &tg.MessagesMessagesSlice{Count: 2, Messages: []tg.MessageClass{msg(4)}}, nil
		default:
			t.Fatal("unexpected extra page")
			return nil, nil
		}
	})
	text, err := o.messages(context.Background(), Request{Tool: "tg_history", Peer: "me", Limit: 1})
	if err != nil || calls != 2 || !strings.Contains(text, "body 4") {
		t.Fatalf("empty message ended iteration: calls=%d err=%v", calls, err)
	}
}

func TestMessagePaginationRemainsBounded(t *testing.T) {
	for _, mode := range []string{"scan cap", "repeated cursor"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
				q := in.(*tg.MessagesGetHistoryRequest)
				calls++
				if calls > ScanCap || q.Limit > 100 {
					t.Fatal("unbounded message scan")
				}
				id := 1000 - calls
				if mode == "repeated cursor" {
					id = 999
				}
				return &tg.MessagesMessagesSlice{Count: 1000, Messages: []tg.MessageClass{msg(id)}}, nil
			})
			text, err := o.messages(context.Background(), Request{Tool: "tg_history", Peer: "me", Limit: 1, MinID: 1000})
			wantCalls := ScanCap
			if mode == "repeated cursor" {
				wantCalls = 2
			}
			if err != nil || calls != wantCalls || strings.Contains(text, "body") {
				t.Fatalf("pagination bounds failed: calls=%d err=%v", calls, err)
			}
			if mode == "scan cap" && !strings.Contains(text, "scan capped") {
				t.Fatal("scan cap was not reported")
			}
		})
	}
}

func TestHistoryFractionalTimeBounds(t *testing.T) {
	const base = 1700000000
	for _, tt := range []struct {
		name, since, until string
		wantOffset         int
		wantSeconds        []int
	}{
		{"since", time.Unix(base, 500000000).UTC().Format(time.RFC3339Nano), "", 0, []int{2, 1}},
		{"until", "", time.Unix(base+1, 500000000).UTC().Format(time.RFC3339Nano), base + 2, []int{1, 0}},
		{"until at int32 boundary", "", time.Unix(2147483647, 500000000).UTC().Format(time.RFC3339Nano), 0, []int{2, 1, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
				q := in.(*tg.MessagesGetHistoryRequest)
				if q.OffsetDate != tt.wantOffset {
					t.Errorf("wrong exclusive offset_date: got=%d want=%d", q.OffsetDate, tt.wantOffset)
				}
				return &tg.MessagesMessages{Messages: []tg.MessageClass{
					&tg.Message{ID: 3, Date: base + 2, PeerID: &tg.PeerUser{UserID: 7}, Message: "second 2"},
					&tg.Message{ID: 2, Date: base + 1, PeerID: &tg.PeerUser{UserID: 7}, Message: "second 1"},
					&tg.Message{ID: 1, Date: base, PeerID: &tg.PeerUser{UserID: 7}, Message: "second 0"},
				}}, nil
			})
			text, err := o.messages(context.Background(), Request{Tool: "tg_history", Peer: "me", Limit: 3, Since: tt.since, Until: tt.until})
			if err != nil {
				t.Fatal(err)
			}
			for second := 0; second <= 2; second++ {
				want := false
				for _, value := range tt.wantSeconds {
					want = want || second == value
				}
				if strings.Contains(text, fmt.Sprintf("second %d", second)) != want {
					t.Fatalf("wrong membership for second %d in fractional window", second)
				}
			}
		})
	}
}
