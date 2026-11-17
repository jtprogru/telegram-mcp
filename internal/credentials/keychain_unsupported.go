//go:build !darwin || !cgo

package credentials

import "context"

// Keychain is unavailable outside the supported macOS/cgo build.
type Keychain struct{ Account string }

func (Keychain) Get(context.Context, string) ([]byte, error) { return nil, ErrUnavailable }
func (Keychain) Put(context.Context, string, []byte) error   { return ErrUnavailable }
