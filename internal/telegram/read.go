package telegram

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"github.com/jtprogru/telegram-mcp/internal/render"
)

func (o *operation) message(ctx context.Context, e render.Entity, id int) (tg.MessageClass, error) {
	var res tg.MessagesMessagesClass
	var err error
	if p, ok := e.Input.(*tg.InputPeerChannel); ok {
		res, err = o.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: id}}})
	} else {
		res, err = o.api.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: id}})
	}
	if err != nil {
		return nil, err
	}
	page, ok := res.AsModified()
	if !ok {
		return nil, userError("unexpected message response")
	}
	o.cat.Add(page.GetUsers(), page.GetChats())
	for _, m := range page.GetMessages() {
		if x, ok := m.AsNotEmpty(); ok && x.GetID() == id && render.PeerID(x.GetPeerID()) == e.ID {
			return m, nil
		}
	}
	return nil, userError("message not found in this peer (deleted or not visible)")
}

func (o *operation) messages(ctx context.Context, r Request) (string, error) {
	var e render.Entity
	var err error
	global := r.Tool == "tg_search" && r.Peer == ""
	if !global {
		e, err = o.resolve(ctx, r.Peer)
		if err != nil {
			return "", err
		}
	}
	since, _ := parseTime(r.Since)
	until, _ := parseTime(r.Until) // Already validated before connection.
	untilOffset := until.Unix()
	if until.Nanosecond() != 0 {
		// Telegram dates are integer seconds. Round an exclusive fractional
		// upper bound up so its final whole second remains eligible.
		untilOffset++
	}
	offsetID, offsetRate := 0, 0
	previousCursor := ""
	var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	collected := make([]tg.MessageClass, 0, r.Limit)
	seen := map[string]bool{}
	scanned := 0
	for scanned < ScanCap && len(collected) < r.Limit {
		limit := min(100, r.Limit-len(collected), ScanCap-scanned)
		var res tg.MessagesMessagesClass
		if global {
			res, err = o.api.MessagesSearchGlobal(ctx, &tg.MessagesSearchGlobalRequest{Q: r.Query, Filter: &tg.InputMessagesFilterEmpty{}, OffsetRate: offsetRate, OffsetPeer: offsetPeer, OffsetID: offsetID, Limit: limit})
		} else if r.Tool == "tg_search" {
			res, err = o.api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: e.Input, Q: r.Query, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offsetID, Limit: limit, MinID: r.MinID, MaxID: r.MaxID})
		} else {
			req := &tg.MessagesGetHistoryRequest{Peer: e.Input, OffsetID: offsetID, Limit: limit, MinID: r.MinID, MaxID: r.MaxID}
			if !until.IsZero() && untilOffset <= 2147483647 {
				req.OffsetDate = int(untilOffset)
			}
			res, err = o.api.MessagesGetHistory(ctx, req)
		}
		if err != nil {
			return "", err
		}
		page, ok := res.AsModified()
		if !ok {
			return "", userError("unexpected history/search response")
		}
		o.cat.Add(page.GetUsers(), page.GetChats())
		raw := page.GetMessages()
		if len(raw) == 0 {
			break
		}
		var last tg.MessageClass
		for _, m := range raw {
			scanned++
			if scanned > ScanCap {
				break
			}
			x, ok := m.AsNotEmpty()
			// Empty messages still carry an ID usable for peer-local pagination.
			// Global search additionally needs a known peer/date cursor.
			if !global || ok {
				last = m
			}
			if !ok {
				continue
			}
			if !global && render.PeerID(x.GetPeerID()) != e.ID {
				continue
			}
			date := time.Unix(int64(x.GetDate()), 0)
			if !since.IsZero() && date.Before(since) {
				continue
			}
			if !until.IsZero() && !date.Before(until) {
				continue
			}
			if r.MinID != 0 && x.GetID() <= r.MinID {
				continue
			}
			if r.MaxID != 0 && x.GetID() >= r.MaxID {
				continue
			}
			key := fmt.Sprintf("%d/%d", render.PeerID(x.GetPeerID()), x.GetID())
			if seen[key] {
				continue
			}
			seen[key] = true
			collected = append(collected, m)
			if len(collected) >= r.Limit {
				break
			}
		}
		if last == nil {
			break
		}
		lastPeer := e.ID
		if x, ok := last.AsNotEmpty(); ok {
			if !since.IsZero() && time.Unix(int64(x.GetDate()), 0).Before(since) {
				break
			}
			if global {
				lastPeer = render.PeerID(x.GetPeerID())
				offsetRate = x.GetDate()
				if p, ok := res.(*tg.MessagesMessagesSlice); ok {
					if rate, ok := p.GetNextRate(); ok {
						offsetRate = rate
					}
				}
				offsetPeer = o.cat[lastPeer].Input
				if offsetPeer == nil {
					break
				}
			}
		}
		offsetID = last.GetID()
		cursor := fmt.Sprintf("%d/%d/%d", lastPeer, offsetID, offsetRate)
		if cursor == previousCursor {
			break
		}
		previousCursor = cursor
		// A short page is not necessarily the end (deleted/inaccessible messages).
		// Continue until an empty/non-advancing page, a bound or the scan cap.
	}
	if r.Tool == "tg_history" {
		slices.Reverse(collected)
	}
	body := render.Messages(collected, o.cat, r.Full, global)
	if scanned >= ScanCap {
		body += "\n[scan capped at 500 messages]"
	}
	peer := r.Peer
	if global {
		peer = "global-search"
	}
	return render.Fence(body, peer), nil
}
