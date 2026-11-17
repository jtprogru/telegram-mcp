package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/message/entity"
	htmlfmt "github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/markdown"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/jtprogru/telegram-mcp/internal/render"
)

func (o *operation) format(r Request, broadcast bool) (string, []tg.MessageEntityClass, error) {
	b := new(entity.Builder)
	// Parse the body separately from the marker so markup cannot hide or style it.
	if !broadcast {
		b.Plain(o.settings.Marker + " ")
	}
	resolve := func(id int64) (tg.InputUserClass, error) {
		if id == o.self.ID {
			return &tg.InputUserSelf{}, nil
		}
		if p, ok := o.cat[id].Input.(*tg.InputPeerUser); ok {
			return &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}, nil
		}
		return nil, userError("tg://user mentions require an already resolved user; use a normal @username instead")
	}
	var err error
	switch r.ParseMode {
	case "none":
		b.Plain(r.Text)
	case "md":
		err = markdown.Markdown(strings.NewReader(r.Text), b, markdown.Options{UserResolver: resolve})
	case "html":
		err = htmlfmt.HTML(strings.NewReader(r.Text), b, htmlfmt.Options{UserResolver: resolve})
	}
	if err != nil {
		return "", nil, userError("could not parse message markup or resolve a user mention")
	}
	text, entities := b.Complete()
	if strings.TrimSpace(text) == "" || (!broadcast && strings.TrimSpace(text) == o.settings.Marker) {
		return "", nil, userError("formatted text is empty")
	}
	if utf16Len(text) > 4096 {
		return "", nil, userError("formatted text including the marker exceeds 4096 UTF-16 code units")
	}
	return text, entities, nil
}

func (o *operation) send(ctx context.Context, r Request) (string, error) {
	e, err := o.resolve(ctx, r.Peer)
	if err != nil {
		return "", err
	}
	text, entities, err := o.format(r, e.Broadcast)
	if err != nil {
		return "", err
	}
	if r.ReplyTo != nil {
		if _, err := o.message(ctx, e, *r.ReplyTo); err != nil {
			return "", err
		}
	}
	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", userError("could not generate MTProto random_id")
	}
	randomID := int64(binary.LittleEndian.Uint64(idBytes[:]))
	if randomID == 0 {
		randomID = 1
	}
	req := &tg.MessagesSendMessageRequest{Peer: e.Input, Message: text, RandomID: randomID, NoWebpage: !r.LinkPreview}
	if len(entities) > 0 {
		req.SetEntities(entities)
	}
	if r.ReplyTo != nil {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: *r.ReplyTo})
	}
	// One logical RPC, one random_id. No application retry, flood middleware,
	// request-timeout reconnect/resend policy, Business fallback or paid flags.
	result, err := o.api.MessagesSendMessage(ctx, req)
	if err != nil {
		if rpc, ok := tgerr.As(err); ok && rpc.Code < 500 {
			return "", err
		}
		return "", userError("send outcome is UNKNOWN after a network/cancellation/server error; inspect peer history before any retry (a new call generates a new random_id)")
	}
	id := sentID(result, randomID, e.ID)
	if id == 0 {
		return "send accepted by Telegram but message ID was not returned; inspect history, do not resend blindly", nil
	}
	location := ""
	if prefix := e.LinkPrefix(); prefix != "" {
		location = fmt.Sprintf(" %s/%d", prefix, id)
	}
	return fmt.Sprintf("sent [%d] to %s%s directly from the user account", id, r.Peer, location), nil
}

func sentID(result tg.UpdatesClass, randomID, peerID int64) int {
	if r, ok := result.(*tg.UpdateShortSentMessage); ok {
		return r.ID
	}
	var updates []tg.UpdateClass
	switch r := result.(type) {
	case *tg.Updates:
		updates = r.Updates
	case *tg.UpdatesCombined:
		updates = r.Updates
	case *tg.UpdateShort:
		updates = []tg.UpdateClass{r.Update}
	}
	for _, u := range updates {
		if m, ok := u.(*tg.UpdateMessageID); ok && m.RandomID == randomID {
			return m.ID
		}
	}
	for _, u := range updates {
		var raw tg.MessageClass
		switch m := u.(type) {
		case *tg.UpdateNewMessage:
			raw = m.Message
		case *tg.UpdateNewChannelMessage:
			raw = m.Message
		}
		if m, ok := raw.(*tg.Message); ok && m.Out && render.PeerID(m.PeerID) == peerID {
			return m.ID
		}
	}
	return 0
}
