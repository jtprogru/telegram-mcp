package credentials

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gotd/td/session"
)

// Every auth key in these tests is synthetic; none is a working credential.
func legacyFixture() string {
	buf := []byte{2, 149, 154, 167, 51, 1, 187} // DC2, IPv4, port 443
	for i := range 256 {
		buf = append(buf, byte(i))
	}
	return "1" + base64.URLEncoding.EncodeToString(buf)
}

func goFixture(t *testing.T) []byte {
	t.Helper()
	data, err := decodeLegacy([]byte(legacyFixture()))
	if err != nil {
		t.Fatal(err)
	}
	// Like gotd Client.saveSession on a fresh QR login: the primary DC
	// and key are persisted, but no explicit address is recorded.
	data.Addr = ""
	memory := &session.StorageMemory{}
	if err := (&session.Loader{Storage: memory}).Save(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	raw, err := memory.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPrepareSession(t *testing.T) {
	for _, tt := range []struct {
		name, source string
		values       map[string][]byte
		getErr       error
		wantErr      bool
	}{
		{"Go preferred", "Go Keychain", map[string][]byte{GoService: goFixture(t), LegacyService: []byte("invalid")}, nil, false},
		{"legacy import", "Telethon Keychain", map[string][]byte{LegacyService: []byte(legacyFixture())}, nil, false},
		{"bad Go is not replaced", "Go Keychain", map[string][]byte{GoService: []byte("DO-NOT-PRINT"), LegacyService: []byte(legacyFixture())}, nil, true},
		{"bad legacy", "Telethon Keychain", map[string][]byte{LegacyService: []byte("DO-NOT-PRINT")}, nil, true},
		{"missing", "", map[string][]byte{}, nil, true},
		{"locked is not absent", "", map[string][]byte{}, ErrUnavailable, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{values: tt.values, getErr: tt.getErr}
			staged, source, err := resolver(store, nil).PrepareSession(context.Background())
			if (err != nil) != tt.wantErr || source != tt.source {
				t.Fatalf("unexpected preparation: source=%s err=%v", source, err)
			}
			if err != nil && strings.Contains(err.Error(), "DO-NOT-PRINT") {
				t.Fatal("decoder leaked input")
			}
			if store.writes != 0 {
				t.Fatal("offline preparation wrote Keychain")
			}
			if err == nil {
				data, err := staged.LoadSession(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateSession(context.Background(), data); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCommitAndPreservation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		putErr  error
		cancel  bool
		wantErr bool
	}{
		{"success", nil, false, false},
		{"denied", ErrUnavailable, false, true},
		{"cancelled", nil, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{values: map[string][]byte{GoService: []byte("previous"), LegacyService: []byte(legacyFixture())}, putErr: tt.putErr}
			staged := NewSession(store)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data := goFixture(t)
			if err := staged.StoreSession(ctx, data); err != nil {
				t.Fatal(err)
			}
			if store.writes != 0 || string(store.values[GoService]) != "previous" {
				t.Fatal("library writes changed old session before authorization")
			}
			if tt.cancel {
				cancel()
			}
			if err := staged.Commit(ctx); (err != nil) != tt.wantErr {
				t.Fatalf("unexpected commit: %v", err)
			}
			if tt.wantErr && string(store.values[GoService]) != "previous" {
				t.Fatal("failed commit changed old session")
			}
			if !tt.wantErr && !bytes.Equal(data, store.values[GoService]) {
				t.Fatal("commit did not persist")
			}
			if string(store.values[LegacyService]) != legacyFixture() {
				t.Fatal("legacy session changed")
			}
		})
	}
}

func TestSessionCopiesAndConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{values: map[string][]byte{}}
	staged := NewSession(store)
	data := goFixture(t)
	if err := staged.StoreSession(ctx, data); err != nil {
		t.Fatal(err)
	}
	data[0] = 'x'
	if err := staged.Commit(ctx); err != nil {
		t.Fatal("StoreSession retained input")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			raw, err := staged.LoadSession(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if err := staged.StoreSession(ctx, raw); err != nil {
				t.Error(err)
			}
			raw[0] = 'x'
		})
	}
	wg.Wait()
	if err := ValidateSession(ctx, store.values[GoService]); err != nil {
		t.Fatal("LoadSession exposed internal buffer")
	}
	store.putErr = ErrUnavailable
	if err := staged.StoreSession(ctx, goFixture(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("write failure suppressed: %v", err)
	}
}
