package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/render"
)

func (o *operation) resolve(ctx context.Context, raw string) (render.Entity, error) {
	key := config.NormalizePeer(raw)
	if key == "me" {
		return o.cat[o.self.ID], nil
	}
	if id, err := strconv.ParseInt(key, 10, 64); err == nil {
		if e, ok := o.cat[id]; ok && e.Input != nil {
			return e, nil
		}
		// Numeric IDs do not carry access_hash. Rehydrate from bounded dialogs on
		// every new process, rather than guessing access_hash=0 or persisting peers.
		_, _, err := o.walkDialogs(ctx, func(d *tg.Dialog) bool { return render.PeerID(d.Peer) == id }, ScanCap)
		if err != nil {
			return render.Entity{}, err
		}
		if e, ok := o.cat[id]; ok && e.Input != nil {
			return e, nil
		}
		return render.Entity{}, userError("numeric peer could not be resolved within 500 dialogs; use a public username or bring the private chat into recent dialogs")
	}
	resolved, err := o.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: key})
	if err != nil {
		return render.Entity{}, err
	}
	o.cat.Add(resolved.Users, resolved.Chats)
	if e, ok := o.cat[render.PeerID(resolved.Peer)]; ok && e.Input != nil {
		return e, nil
	}
	return render.Entity{}, userError("Telegram did not return a usable peer/access hash")
}

// walkDialogs visits main and archived folders with a single combined hard cap.
// Dialog offsets use the last dialog's peer AND its top message date/ID.
func (o *operation) walkDialogs(ctx context.Context, visit func(*tg.Dialog) bool, cap int) (int, bool, error) {
	scanned := 0
	for _, folder := range []int{0, 1} {
		folderStart := scanned
		req := &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100}
		req.SetFolderID(folder)
		previous := ""
		for scanned < cap {
			req.Limit = min(100, cap-scanned)
			res, err := o.api.MessagesGetDialogs(ctx, req)
			if err != nil {
				return scanned, false, err
			}
			page, ok := res.AsModified()
			if !ok {
				return scanned, false, userError("unexpected dialog response")
			}
			o.cat.Add(page.GetUsers(), page.GetChats())
			dialogs := page.GetDialogs()
			if len(dialogs) == 0 {
				break
			}
			var last *tg.Dialog
			for _, raw := range dialogs {
				d, ok := raw.(*tg.Dialog)
				if !ok {
					continue
				}
				last = d
				scanned++
				if visit(d) {
					return scanned, false, nil
				}
				if scanned >= cap {
					return scanned, true, nil
				}
			}
			// A short slice is not proof of exhaustion. Only a complete response,
			// its exact count, or an empty page ends this folder's pagination.
			_, complete := res.(*tg.MessagesDialogs)
			if p, ok := res.(*tg.MessagesDialogsSlice); ok {
				complete = p.Count > 0 && scanned-folderStart >= p.Count
			}
			if last == nil || complete {
				break
			}
			offset := fmt.Sprintf("%d/%d", render.PeerID(last.Peer), last.TopMessage)
			if offset == previous {
				return scanned, true, nil
			}
			previous = offset
			e := o.cat[render.PeerID(last.Peer)]
			if e.Input == nil {
				return scanned, true, nil
			}
			date := 0
			for _, m := range page.GetMessages() {
				if x, ok := m.AsNotEmpty(); ok && x.GetID() == last.TopMessage && render.PeerID(x.GetPeerID()) == e.ID {
					date = x.GetDate()
					break
				}
			}
			if date == 0 {
				return scanned, true, nil
			}
			req.OffsetPeer = e.Input
			req.OffsetID = last.TopMessage
			req.OffsetDate = date
			req.ExcludePinned = true
		}
	}
	return scanned, false, nil
}

func (o *operation) dialogs(ctx context.Context, r Request) (string, error) {
	lines := []string{}
	needle := strings.ToLower(r.Query)
	_, capped, err := o.walkDialogs(ctx, func(d *tg.Dialog) bool {
		e, ok := o.cat[render.PeerID(d.Peer)]
		if !ok {
			return false
		}
		if r.Kind != "" && r.Kind != e.Kind {
			return false
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Title), needle) && !strings.Contains(strings.ToLower(e.Username), needle) {
			return false
		}
		lines = append(lines, render.Dialog(e, d.UnreadCount))
		return len(lines) >= r.Limit
	}, ScanCap)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		lines = append(lines, "(no dialogs matched)")
	}
	if capped {
		lines = append(lines, "[scan capped at 500 dialogs or a non-advancing page]")
	}
	return render.Fence(strings.Join(lines, "\n"), "dialogs"), nil
}
