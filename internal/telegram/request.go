package telegram

import (
	"errors"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/render"
)

const (
	// ScanCap bounds dialog discovery and filtered reads per call.
	ScanCap = 500
	// MaxMessages bounds the number of returned messages.
	MaxMessages = 200
	// MaxDownloadBytes bounds both announced and actually written media size.
	MaxDownloadBytes int64 = 100 << 20
)

// Request is the validated internal contract shared by the MCP adapters and MTProto backend.
// Limits have defaults applied by MCP; no zero/unbounded limit reaches Telegram.
type Request struct {
	Tool        string
	Peer        string
	Query       string
	Kind        string
	Limit       int
	MinID       int
	MaxID       int
	Since       string
	Until       string
	Full        bool
	Link        string
	Text        string
	ParseMode   string
	LinkPreview bool
	ReplyTo     *int
	MessageID   int
	DestDir     string
}

// Validate performs all local checks before secret loading or network connection.
func (r Request) Validate(settings config.Settings) error {
	if r.Tool == "tg_send" && !settings.SendAllowed(r.Peer) {
		return errors.New("sending to this peer is not allowed; change TELEGRAM_MCP_SEND_ALLOWLIST and restart to widen access")
	}
	switch r.Tool {
	case "tg_me":
	case "tg_dialogs":
		if len(r.Query) > 1024 {
			return errors.New("query exceeds 1024 bytes")
		}
		if r.Limit < 1 || r.Limit > ScanCap {
			return errors.New("limit must be between 1 and 500")
		}
		if r.Kind != "" && r.Kind != "user" && r.Kind != "bot" && r.Kind != "group" && r.Kind != "channel" {
			return errors.New("kind must be user/bot/group/channel")
		}
	case "tg_history", "tg_search":
		if r.Limit < 1 || r.Limit > MaxMessages {
			return errors.New("limit must be between 1 and 200")
		}
		if r.Tool == "tg_history" || r.Peer != "" {
			if err := validatePeer(r.Peer); err != nil {
				return err
			}
		}
		if r.Tool == "tg_search" && strings.TrimSpace(r.Query) == "" {
			return errors.New("query is required")
		}
		if len(r.Query) > 1024 {
			return errors.New("query exceeds 1024 bytes")
		}
		if r.MinID < 0 || r.MaxID < 0 || r.MinID > 2147483647 || r.MaxID > 2147483647 || (r.MaxID != 0 && r.MinID >= r.MaxID) {
			return errors.New("invalid exclusive min_id/max_id bounds")
		}
		since, err := parseTime(r.Since)
		if err != nil {
			return err
		}
		until, err := parseTime(r.Until)
		if err != nil {
			return err
		}
		if !since.IsZero() && !until.IsZero() && !since.Before(until) {
			return errors.New("since must be before until")
		}
	case "tg_message":
		_, _, err := render.ParseLink(r.Link)
		return err
	case "tg_send":
		if err := validatePeer(r.Peer); err != nil {
			return err
		}
		if strings.TrimSpace(r.Text) == "" {
			return errors.New("text is required")
		}
		if len(r.Text) > 65536 {
			return errors.New("source text exceeds 65536 bytes")
		}
		if r.ReplyTo != nil && (*r.ReplyTo <= 0 || *r.ReplyTo > 2147483647) {
			return errors.New("reply_to must be a positive 32-bit message ID in the same peer")
		}
		switch r.ParseMode {
		case "md", "html", "none":
		default:
			return errors.New("parse_mode must be md/html/none")
		}
	case "tg_download":
		if err := validatePeer(r.Peer); err != nil {
			return err
		}
		if r.MessageID <= 0 || r.MessageID > 2147483647 {
			return errors.New("message_id must be a positive 32-bit integer")
		}
		if strings.TrimSpace(r.DestDir) == "" {
			return errors.New("dest_dir is required")
		}
	default:
		return errors.New("unknown tool")
	}
	return nil
}

func validatePeer(peer string) error {
	p := config.NormalizePeer(peer)
	if p == "" || len(p) > 128 || strings.ContainsAny(p, "/?# \\:\t\n\r") {
		return errors.New("peer must be me, a username, or a signed numeric Telegram peer ID")
	}
	return nil
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		t, err := time.Parse(layout, value)
		if err == nil && t.Unix() > 0 && t.Unix() <= 2147483647 {
			return t, nil
		}
	}
	return time.Time{}, errors.New("time must be an ISO-8601 date or timestamp in 1970–2038; timestamps without a zone use UTC")
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }
