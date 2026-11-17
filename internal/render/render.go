// Package render produces bounded compact text with explicitly untrusted Telegram payloads.
package render

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

const channelBase int64 = 1000000000000

var fencePattern = regexp.MustCompile(`(?i)</?untrusted-telegram-content`)

// Fence escapes nested delimiters and attributes. This is a trust signal, not an injection sandbox.
func Fence(body, peer string) string {
	safe := fencePattern.ReplaceAllStringFunc(body, func(s string) string { return strings.Replace(s, "<", "&lt;", 1) })
	return "<untrusted-telegram-content peer=\"" + html.EscapeString(peer) + "\">\n" + safe + "\n</untrusted-telegram-content>"
}

// ParseLink accepts public/private message links and topic links (last path ID).
func ParseLink(raw string) (string, int, error) {
	invalid := errors.New("expected a t.me/<username>/<message_id> or t.me/c/<channel_id>/<message_id> link")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", 0, invalid
	}
	switch strings.ToLower(u.Host) {
	case "t.me", "www.t.me", "telegram.me", "www.telegram.me", "telegram.dog":
	default:
		return "", 0, invalid
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) > 0 && p[0] == "s" {
		p = p[1:]
	}
	if len(p) < 2 {
		return "", 0, invalid
	}
	id, err := strconv.ParseInt(p[len(p)-1], 10, 32)
	if err != nil || id <= 0 {
		return "", 0, invalid
	}
	if p[0] == "c" {
		if len(p) < 3 {
			return "", 0, invalid
		}
		cid, err := strconv.ParseInt(p[1], 10, 64)
		if err != nil || cid <= 0 || cid >= channelBase {
			return "", 0, invalid
		}
		return strconv.FormatInt(-channelBase-cid, 10), int(id), nil
	}
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`).MatchString(p[0]) {
		return "", 0, invalid
	}
	return p[0], int(id), nil
}

// PeerID returns the standard Telegram signed ID, avoiding numeric user/channel collisions.
func PeerID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return -v.ChatID
	case *tg.PeerChannel:
		return -channelBase - v.ChannelID
	}
	return 0
}

// Entity contains only display/routing metadata, not MTProto session credentials.
type Entity struct {
	ID        int64
	Title     string
	Username  string
	Kind      string
	Broadcast bool
	Input     tg.InputPeerClass
}

// Catalog accumulates peers returned alongside messages, without extra per-message lookups.
type Catalog map[int64]Entity

// Add incorporates usable non-min users/channels and their access hashes.
func (c Catalog) Add(users []tg.UserClass, chats []tg.ChatClass) {
	for _, raw := range users {
		if u, ok := raw.(*tg.User); ok {
			kind := "user"
			if u.Bot {
				kind = "bot"
			}
			var input tg.InputPeerClass
			if u.Self {
				input = &tg.InputPeerSelf{}
			} else if !u.Min && u.AccessHash != 0 {
				input = &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
			}
			e := Entity{ID: u.ID, Title: strings.TrimSpace(u.FirstName + " " + u.LastName), Username: u.Username, Kind: kind, Input: input}
			c.merge(e)
		}
	}
	for _, raw := range chats {
		switch v := raw.(type) {
		case *tg.Chat:
			c.merge(Entity{ID: -v.ID, Title: v.Title, Kind: "group", Input: &tg.InputPeerChat{ChatID: v.ID}})
		case *tg.Channel:
			kind := "group"
			if v.Broadcast {
				kind = "channel"
			}
			var input tg.InputPeerClass
			if !v.Min && v.AccessHash != 0 {
				input = &tg.InputPeerChannel{ChannelID: v.ID, AccessHash: v.AccessHash}
			}
			c.merge(Entity{ID: -channelBase - v.ID, Title: v.Title, Username: v.Username, Kind: kind, Broadcast: v.Broadcast, Input: input})
		}
	}
}
func (c Catalog) merge(e Entity) {
	if old, ok := c[e.ID]; ok && e.Input == nil {
		e.Input = old.Input
	}
	c[e.ID] = e
}

// LinkPrefix returns message-link prefixes only for channels/supergroups, never users/basic groups.
func (e Entity) LinkPrefix() string {
	if e.ID >= -channelBase {
		return ""
	}
	if e.Username != "" {
		return "https://t.me/" + e.Username
	}
	return fmt.Sprintf("https://t.me/c/%d", -e.ID-channelBase)
}

// Dialog renders the supplied title as data; callers fence the entire output.
func Dialog(e Entity, unread int) string {
	title := e.Title
	if title == "" {
		title = "(no title)"
	}
	handle := ""
	if e.Username != "" {
		handle = " (@" + e.Username + ")"
	}
	return fmt.Sprintf("[%d] %s%s · %s · unread=%d", e.ID, title, handle, e.Kind, unread)
}

// Message renders own/signed/forwarded authorship, reply references, links and media labels.
func Message(raw tg.MessageClass, c Catalog, full, global bool) string {
	var id, date, reply int
	var peer, from tg.PeerClass
	var text, author, media, forward string
	var out bool
	switch m := raw.(type) {
	case *tg.Message:
		id, date, peer, from, text, author, out = m.ID, m.Date, m.PeerID, m.FromID, m.Message, m.PostAuthor, m.Out
		if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			reply = h.ReplyToMsgID
		}
		if m.Media != nil {
			switch m.Media.(type) {
			case *tg.MessageMediaWebPage:
			default:
				media = "[" + strings.TrimPrefix(m.Media.TypeName(), "messageMedia") + "]"
			}
		}
		if f, ok := m.GetFwdFrom(); ok {
			forward = "fwd"
			label := f.FromName
			if e, ok := c[PeerID(f.FromID)]; ok && e.Title != "" {
				label = e.Title
			}
			if label != "" {
				forward += " from " + label
			}
		}
	case *tg.MessageService:
		id, date, peer, from, out = m.ID, m.Date, m.PeerID, m.FromID, m.Out
		text = "[service]"
		if m.Action != nil {
			text = "[service: " + m.Action.TypeName() + "]"
		}
	default:
		return ""
	}
	parts := []string{fmt.Sprintf("[%d] %s", id, time.Unix(int64(date), 0).UTC().Format("2006-01-02 15:04"))}
	e := c[PeerID(peer)]
	if global {
		parts = append(parts, fmt.Sprintf("[%d] %s", PeerID(peer), e.Title))
	}
	if out {
		author = "you"
	} else if author == "" && from != nil {
		sender := c[PeerID(from)]
		author = sender.Title
		if sender.Username != "" {
			author += " (@" + sender.Username + ")"
		}
		if author == "" {
			author = strconv.FormatInt(PeerID(from), 10)
		}
	}
	if author != "" {
		parts = append(parts, "from "+author)
	}
	if forward != "" {
		parts = append(parts, forward)
	}
	if media != "" {
		parts = append(parts, media)
	}
	if reply > 0 {
		parts = append(parts, fmt.Sprintf("reply→%d", reply))
	}
	if prefix := e.LinkPrefix(); prefix != "" {
		parts = append(parts, fmt.Sprintf("%s/%d", prefix, id))
	}
	text = strings.TrimSpace(text)
	max := 500
	if full {
		max = 16384
	}
	runes := []rune(text)
	if len(runes) > max {
		text = string(runes[:max]) + "…"
	}
	if text == "" {
		return strings.Join(parts, " · ")
	}
	return strings.Join(parts, " · ") + "\n" + text
}

// Messages joins non-empty message lines in the order supplied by the caller.
func Messages(ms []tg.MessageClass, c Catalog, full, global bool) string {
	lines := make([]string, 0, len(ms))
	for _, m := range ms {
		if line := Message(m, c, full, global); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "(no messages)"
	}
	return strings.Join(lines, "\n\n")
}
