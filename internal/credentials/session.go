package credentials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
)

const maxSessionBytes = 1 << 20

// Session is a staged gotd storage. Until Commit, all library writes stay in RAM.
// After Commit, session updates are persisted to the dedicated Go Keychain item.
// Failed login/import/cancellation before Commit cannot change the old item.
type Session struct {
	mu        sync.Mutex
	store     Store
	data      []byte
	committed bool
}

// NewSession starts an empty login without loading or modifying any existing session.
func NewSession(store Store) *Session { return &Session{store: store} }

// LoadSession implements gotd session.Storage, returning a defensive copy.
func (s *Session) LoadSession(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		return nil, session.ErrNotFound
	}
	return bytes.Clone(s.data), nil
}

// StoreSession stages library updates, or persists them once authorization is confirmed.
func (s *Session) StoreSession(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxSessionBytes {
		return errors.New("invalid session size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed {
		if err := s.store.Put(ctx, GoService, data); err != nil {
			return fmt.Errorf("could not update Go session in Keychain: %w", err)
		}
	}
	s.data = bytes.Clone(data)
	return nil
}

// Commit must be called only after successful network authorization (Self).
// A failed Keychain write is an error, never a successful login or a file fallback.
func (s *Session) Commit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateSession(ctx, s.data); err != nil {
		return err
	}
	if err := s.store.Put(ctx, GoService, s.data); err != nil {
		return fmt.Errorf("could not save Go session in Keychain; authorization was not saved: %w", err)
	}
	s.committed = true
	return nil
}

// ValidateSession checks gotd's envelope and essential auth-key fields offline.
// Decoder errors are intentionally discarded because they may contain input data.
func ValidateSession(ctx context.Context, raw []byte) error {
	if len(raw) == 0 || len(raw) > maxSessionBytes {
		return errors.New("invalid Go session size; run telegram-mcp auth login --qr")
	}
	memory := &session.StorageMemory{}
	if err := memory.StoreSession(ctx, raw); err != nil {
		return errors.New("could not stage Go session")
	}
	data, err := (&session.Loader{Storage: memory}).Load(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || data == nil {
		return errors.New("invalid or unsupported Go session format; run telegram-mcp auth login --qr")
	}
	return validateData(data)
}

func validateData(data *session.Data) error {
	invalid := errors.New("invalid Go session data; run telegram-mcp auth login --qr")
	if data.DC < 1 || data.DC > 5 || len(data.AuthKey) != 256 || len(data.AuthKeyID) != 8 {
		return invalid
	}
	if _, _, err := net.SplitHostPort(data.Addr); err != nil {
		return invalid
	}
	var key crypto.Key
	copy(key[:], data.AuthKey)
	id := key.WithID().ID
	if bytes.Equal(data.AuthKey, make([]byte, 256)) || !bytes.Equal(id[:], data.AuthKeyID) {
		return invalid
	}
	return nil
}

// ValidateLegacy checks whether gotd can decode a Telethon StringSession offline.
// Config and server salt are not present in that format; only a real connection
// can demonstrate whether gotd recovers them correctly.
func ValidateLegacy(value []byte) error {
	_, err := decodeLegacy(value)
	return err
}

func decodeLegacy(value []byte) (*session.Data, error) {
	if len(value) == 0 || len(value) > maxSessionBytes {
		return nil, errors.New("invalid Telethon session; run telegram-mcp auth login --qr")
	}
	data, err := session.TelethonSession(strings.TrimSpace(string(value)))
	if err != nil || data == nil {
		return nil, errors.New("could not decode Telethon session; run telegram-mcp auth login --qr")
	}
	if err := validateData(data); err != nil {
		return nil, errors.New("invalid Telethon session data; run telegram-mcp auth login --qr")
	}
	return data, nil
}

// PrepareSession prefers a valid Go item. Only an absent item permits legacy import.
// An unreadable or corrupt Go item is never silently replaced by a legacy key.
// The returned storage remains staged until its authorization is checked online.
func (r Resolver) PrepareSession(ctx context.Context) (*Session, string, error) {
	goSecret, err := r.Read(ctx, GoService, "")
	if err == nil {
		if err := ValidateSession(ctx, goSecret.Value); err != nil {
			return nil, "Go Keychain", err
		}
		s := NewSession(r.Store)
		if err := s.StoreSession(ctx, goSecret.Value); err != nil {
			return nil, "Go Keychain", err
		}
		return s, "Go Keychain", nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, "", err
	}
	legacy, err := r.Read(ctx, LegacyService, LegacyEnv)
	if err != nil {
		return nil, "", fmt.Errorf("no usable session; run telegram-mcp auth login --qr: %w", err)
	}
	data, err := decodeLegacy(legacy.Value)
	if err != nil {
		return nil, "Telethon " + legacy.Source, err
	}
	s := NewSession(r.Store)
	if err := (&session.Loader{Storage: s}).Save(ctx, data); err != nil {
		return nil, "Telethon " + legacy.Source, errors.New("could not stage imported session")
	}
	return s, "Telethon " + legacy.Source, nil
}
