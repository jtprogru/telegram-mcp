//go:build darwin && cgo

package credentials

import (
	"context"
	"errors"

	keychain "github.com/keybase/go-keychain"
)

// Keychain uses Security.framework in process, never the security CLI.
// Account is the legacy $USER value (not an inferred login or home directory).
type Keychain struct{ Account string }

func (k Keychain) query(service string) keychain.Item {
	q := keychain.NewItem()
	q.SetSecClass(keychain.SecClassGenericPassword)
	q.SetService(service)
	q.SetAccount(k.Account)
	return q
}

// Get reads a single generic password without logging native errors or values.
func (k Keychain) Get(ctx context.Context, service string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q := k.query(service)
	q.SetMatchLimit(keychain.MatchLimitOne)
	q.SetReturnData(true)
	items, err := keychain.QueryItem(q)
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items[0].Data, nil
}

// Put uses SecItemUpdate, or SecItemAdd for a new item; never delete-then-add.
func (k Keychain) Put(ctx context.Context, service string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q := k.query(service)
	update := keychain.NewItem()
	update.SetData(data)
	if err := keychain.UpdateItem(q, update); err == nil {
		return nil
	} else if !errors.Is(err, keychain.ErrorItemNotFound) {
		return ErrUnavailable
	}
	item := k.query(service)
	item.SetData(data)
	item.SetAccessible(keychain.AccessibleWhenUnlockedThisDeviceOnly)
	if err := keychain.AddItem(item); err != nil {
		// Another process may have created it since the update. Do not remove it.
		if !errors.Is(err, keychain.ErrorDuplicateItem) {
			return ErrUnavailable
		}
		if err := keychain.UpdateItem(q, update); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
