// Package credentials reads legacy secrets without changing their names or priority.
package credentials

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	APIIDService   = "telegram-mcp-api-id"
	APIHashService = "telegram-mcp-api-hash"
	LegacyService  = "telegram-mcp-session"
	GoService      = "telegram-mcp-go-session"
	APIIDEnv       = "TELEGRAM_MCP_API_ID"
	APIHashEnv     = "TELEGRAM_MCP_API_HASH"
	LegacyEnv      = "TELEGRAM_MCP_SESSION"
)

var (
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("Keychain unavailable or access denied")
)

// Store is a secret store. Put must replace an existing value atomically.
// Implementations must not include secret values in errors.
type Store interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
}

// Secret includes provenance and whether reading Keychain failed before env fallback.
// String deliberately redacts its value.
type Secret struct {
	Value           []byte
	Source          string
	KeychainFailure bool
}

func (s Secret) String() string { return "[redacted secret from " + s.Source + "]" }

// Resolver preserves the Python implementation's Keychain -> environment priority.
type Resolver struct {
	Store     Store
	LookupEnv func(string) (string, bool)
}

// Read never substitutes an environment value for a found (even empty) Keychain item.
// An empty env name disables env fallback, as required for the Go session.
func (r Resolver) Read(ctx context.Context, service, env string) (Secret, error) {
	value, err := r.Store.Get(ctx, service)
	if err == nil {
		return Secret{Value: value, Source: "Keychain"}, nil
	}
	if ctx.Err() != nil {
		return Secret{}, ctx.Err()
	}
	failed := !errors.Is(err, ErrNotFound)
	if env != "" && r.LookupEnv != nil {
		if value, ok := r.LookupEnv(env); ok {
			return Secret{Value: []byte(value), Source: "env", KeychainFailure: failed}, nil
		}
	}
	if failed {
		return Secret{KeychainFailure: true}, fmt.Errorf("%s: %w (no environment fallback available)", service, ErrUnavailable)
	}
	return Secret{}, fmt.Errorf("%s: %w", service, ErrNotFound)
}

// API contains validated Telegram application credentials.
type API struct {
	ID   int
	Hash string
}

func (API) String() string { return "[redacted API credentials]" }

// ValidateID reports invalid data without quoting it.
func ValidateID(value []byte) error {
	id, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 32)
	if err != nil || id <= 0 {
		return errors.New("API ID must be a positive 32-bit integer")
	}
	return nil
}

// ValidateHash checks the format provided by my.telegram.org.
func ValidateHash(value []byte) error {
	v := strings.TrimSpace(string(value))
	if len(v) != 32 {
		return errors.New("API hash must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(v); err != nil {
		return errors.New("API hash must contain 32 hexadecimal characters")
	}
	return nil
}

// LoadAPI validates the selected source; invalid Keychain data is not replaced by env.
func (r Resolver) LoadAPI(ctx context.Context) (API, error) {
	id, err := r.Read(ctx, APIIDService, APIIDEnv)
	if err != nil {
		return API{}, fmt.Errorf("API ID unavailable; run telegram-mcp init: %w", err)
	}
	if err := ValidateID(id.Value); err != nil {
		return API{}, fmt.Errorf("%s (%s): %w", APIIDService, id.Source, err)
	}
	hash, err := r.Read(ctx, APIHashService, APIHashEnv)
	if err != nil {
		return API{}, fmt.Errorf("API hash unavailable; run telegram-mcp init: %w", err)
	}
	if err := ValidateHash(hash.Value); err != nil {
		return API{}, fmt.Errorf("%s (%s): %w", APIHashService, hash.Source, err)
	}
	// Validation above guarantees conversion succeeds.
	n, err := strconv.Atoi(strings.TrimSpace(string(id.Value)))
	if err != nil {
		return API{}, errors.New("invalid API ID")
	}
	return API{ID: n, Hash: strings.TrimSpace(string(hash.Value))}, nil
}
