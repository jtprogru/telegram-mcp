package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/credentials"
	"github.com/jtprogru/telegram-mcp/internal/render"
)

type scriptedInvoker struct {
	fn func(context.Context, bin.Encoder) (bin.Encoder, error)
}

func (s scriptedInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	result, err := s.fn(ctx, in)
	if err != nil {
		return err
	}
	var buf bin.Buffer
	if err := result.Encode(&buf); err != nil {
		return err
	}
	return out.Decode(&buf)
}
func operationFor(fn func(context.Context, bin.Encoder) (bin.Encoder, error)) *operation {
	self := &tg.User{ID: 7, Self: true, FirstName: "Owner"}
	cat := render.Catalog{}
	cat.Add([]tg.UserClass{self}, nil)
	return &operation{api: tg.NewClient(scriptedInvoker{fn}), self: self, cat: cat, settings: config.Load(func(string) string { return "" })}
}
func msg(id int) tg.MessageClass {
	return &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 7}, Message: fmt.Sprintf("body %d", id), Date: 1700000000 + id, Out: true}
}

type forbiddenStore struct{}

func (forbiddenStore) Get(context.Context, string) ([]byte, error) {
	panic("credentials must not be read")
}
func (forbiddenStore) Put(context.Context, string, []byte) error {
	panic("credentials must not be written")
}

func TestDeniedAndInvalidCallsBeforeSecrets(t *testing.T) {
	s := &Service{Resolver: credentials.Resolver{Store: forbiddenStore{}}, Settings: config.Load(func(string) string { return "" })}
	for _, r := range []Request{
		{Tool: "tg_send", Peer: "not-allowed", Text: "hello", ParseMode: "none"},
		{Tool: "tg_send", Peer: "me", Text: "hello", ParseMode: "invalid"},
		{Tool: "tg_send", Peer: "me", Text: "hello", ParseMode: "md", ReplyTo: new(0)},
		{Tool: "tg_history", Peer: "me", Limit: 201},
		{Tool: "tg_history", Peer: "me", Limit: 1, Since: "bad"},
		{Tool: "tg_download", Peer: "me", MessageID: 0, DestDir: "/tmp"},
	} {
		if _, err := s.Call(context.Background(), r); err == nil {
			t.Fatalf("invalid call accepted: %s", r.Tool)
		}
	}
}

func TestHistoryPaginationOrderAndBounds(t *testing.T) {
	calls := 0
	o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
		q, ok := in.(*tg.MessagesGetHistoryRequest)
		if !ok {
			t.Fatalf("unexpected RPC %T", in)
		}
		calls++
		if q.MinID != 10 || q.MaxID != 300 {
			t.Fatal("exclusive bounds lost")
		}
		ms := []tg.MessageClass{}
		if calls == 1 {
			if q.OffsetID != 0 || q.Limit != 100 {
				t.Fatal("wrong initial page")
			}
			for id := 210; id > 110; id-- {
				ms = append(ms, msg(id))
			}
		} else {
			if q.OffsetID != 111 || q.Limit != 1 {
				t.Fatal("wrong continuation")
			}
			ms = append(ms, msg(110))
		}
		return &tg.MessagesMessages{Messages: ms}, nil
	})
	text, err := o.messages(context.Background(), Request{Tool: "tg_history", Peer: "me", Limit: 101, MinID: 10, MaxID: 300})
	if err != nil || calls != 2 {
		t.Fatalf("pagination failed: %v", err)
	}
	if strings.Index(text, "[110]") >= strings.Index(text, "[210]") {
		t.Fatal("history is not oldest first")
	}
}

func TestMessagePeerIsolation(t *testing.T) {
	o := operationFor(func(context.Context, bin.Encoder) (bin.Encoder, error) {
		return &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 999}, Message: "wrong peer"}}}, nil
	})
	if _, err := o.message(context.Background(), o.cat[7], 12); err == nil {
		t.Fatal("returned a message from another peer")
	}
}

func TestSendEntitiesReplyAndSingleAttempt(t *testing.T) {
	for _, mode := range []string{"md", "html", "none"} {
		t.Run(mode, func(t *testing.T) {
			sends := 0
			reads := 0
			o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
				switch q := in.(type) {
				case *tg.MessagesGetMessagesRequest:
					reads++
					return &tg.MessagesMessages{Messages: []tg.MessageClass{msg(12)}}, nil
				case *tg.MessagesSendMessageRequest:
					sends++
					if q.RandomID == 0 || !q.NoWebpage {
						t.Fatal("send safety flags/random_id missing")
					}
					reply, ok := q.ReplyTo.(*tg.InputReplyToMessage)
					if !ok || reply.ReplyToMsgID != 12 {
						t.Fatal("reply lost")
					}
					if q.Message != "🤖 🙂 bold" {
						t.Fatalf("unexpected plain body %q", q.Message)
					}
					if mode != "none" {
						if len(q.Entities) != 1 {
							t.Fatal("entities lost")
						}
						bold, ok := q.Entities[0].(*tg.MessageEntityBold)
						if !ok || bold.Offset != 6 || bold.Length != 4 {
							t.Fatal("UTF-16 offsets wrong")
						}
					}
					return &tg.UpdateShortSentMessage{ID: 99}, nil
				default:
					t.Fatalf("unexpected RPC %T", in)
					return nil, nil
				}
			})
			source := "🙂 **bold**"
			if mode == "html" {
				source = "🙂 <b>bold</b>"
			}
			if mode == "none" {
				source = "🙂 bold"
			}
			text, err := o.send(context.Background(), Request{Tool: "tg_send", Peer: "me", Text: source, ParseMode: mode, ReplyTo: new(12)})
			if err != nil || sends != 1 || reads != 1 || !strings.Contains(text, "sent [99]") {
				t.Fatalf("send failed: %v", err)
			}
		})
	}
}

func TestUnknownSendIsNeverRetried(t *testing.T) {
	for _, failure := range []error{errors.New("DO-NOT-PRINT"), &tgerr.Error{Code: 500, Type: "INTERNAL", Message: "DO-NOT-PRINT"}} {
		calls := 0
		o := operationFor(func(context.Context, bin.Encoder) (bin.Encoder, error) {
			calls++
			return nil, failure
		})
		_, err := o.send(context.Background(), Request{Tool: "tg_send", Peer: "me", Text: "hello", ParseMode: "none"})
		if err == nil || !strings.Contains(err.Error(), "UNKNOWN") || strings.Contains(err.Error(), "DO-NOT-PRINT") || calls != 1 {
			t.Fatal("unsafe unknown-outcome handling")
		}
	}
}

func TestBroadcastAndMarkupLimits(t *testing.T) {
	o := operationFor(nil)
	for _, tt := range []struct {
		source, mode, want string
		broadcast, invalid bool
	}{
		{"*italic*", "md", "🤖 italic", false, false},
		{"__bold__", "md", "bold", true, false},
		{"<b>x</b>", "html", "🤖 x", false, false},
		{"```go\ncode\n```", "md", "🤖 code", false, false},
		{"<pre><code class=\"language-go\">code</code></pre>", "html", "🤖 code", false, false},
		{strings.Repeat("🙂", 2048), "none", "", false, true},
		{"** **", "md", "🤖 ** **", false, false},
	} {
		text, _, err := o.format(Request{Text: tt.source, ParseMode: tt.mode}, tt.broadcast)
		if (err != nil) != tt.invalid || (!tt.invalid && text != tt.want) {
			t.Fatalf("format mismatch: got %q err=%v", text, err)
		}
	}
}

func TestPrivateNumericPeerAfterRestart(t *testing.T) {
	for range 2 {
		calls := 0
		o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
			calls++
			if _, ok := in.(*tg.MessagesGetDialogsRequest); !ok {
				t.Fatalf("unexpected RPC %T", in)
			}
			return &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 123}, TopMessage: 10}}, Chats: []tg.ChatClass{&tg.Channel{ID: 123, Title: "Private", AccessHash: 456, Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}}}, nil
		})
		e, err := o.resolve(context.Background(), "-1000000000123")
		if err != nil || calls != 1 {
			t.Fatalf("could not rehydrate private peer: %v", err)
		}
		p, ok := e.Input.(*tg.InputPeerChannel)
		if !ok || p.AccessHash != 456 {
			t.Fatal("private peer hash missing")
		}
	}
}

func TestGlobalSearchPagination(t *testing.T) {
	calls := 0
	o := operationFor(func(_ context.Context, in bin.Encoder) (bin.Encoder, error) {
		q := in.(*tg.MessagesSearchGlobalRequest)
		calls++
		page := &tg.MessagesMessagesSlice{}
		if calls == 1 {
			for id := 200; id > 100; id-- {
				page.Messages = append(page.Messages, msg(id))
			}
			page.SetNextRate(42)
		} else {
			if q.OffsetRate != 42 || q.OffsetID != 101 {
				t.Fatal("global cursor lost")
			}
			if _, ok := q.OffsetPeer.(*tg.InputPeerSelf); !ok {
				t.Fatal("global offset peer lost")
			}
			page.Messages = []tg.MessageClass{msg(100)}
		}
		return page, nil
	})
	text, err := o.messages(context.Background(), Request{Tool: "tg_search", Query: "body", Limit: 101})
	if err != nil || calls != 2 || strings.Index(text, "[200]") >= strings.Index(text, "[100]") {
		t.Fatalf("global search failed: %v", err)
	}
}
