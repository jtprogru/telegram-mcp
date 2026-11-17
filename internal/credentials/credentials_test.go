package credentials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type memoryStore struct {
	values map[string][]byte
	getErr error
	putErr error
	writes int
}

func (m *memoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.getErr != nil {
		return nil, m.getErr
	}
	value, ok := m.values[key]
	if !ok {
		return nil, ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (m *memoryStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.putErr != nil {
		return m.putErr
	}
	m.writes++
	m.values[key] = bytes.Clone(value)
	return nil
}

func resolver(store Store, env map[string]string) Resolver {
	return Resolver{Store: store, LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }}
}

func TestResolverPriority(t *testing.T) {
	for _, tt := range []struct {
		name, keychain, env string
		found, envFound     bool
		getErr              error
		want, source        string
		wantErr             error
		failure             bool
	}{
		{name: "Keychain wins", found: true, keychain: "from-keychain", envFound: true, env: "from-env", want: "from-keychain", source: "Keychain"},
		{name: "empty Keychain is not replaced", found: true, envFound: true, env: "from-env", source: "Keychain"},
		{name: "missing", wantErr: ErrNotFound},
		{name: "env fallback", envFound: true, env: "from-env", want: "from-env", source: "env"},
		{name: "empty env is diagnosed later", envFound: true, source: "env"},
		{name: "locked fallback", getErr: ErrUnavailable, envFound: true, env: "from-env", want: "from-env", source: "env", failure: true},
		{name: "locked without fallback", getErr: ErrUnavailable, wantErr: ErrUnavailable, failure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{values: map[string][]byte{}, getErr: tt.getErr}
			if tt.found {
				store.values[APIIDService] = []byte(tt.keychain)
			}
			env := map[string]string{}
			if tt.envFound {
				env[APIIDEnv] = tt.env
			}
			got, err := resolver(store, env).Read(context.Background(), APIIDService, APIIDEnv)
			if !errors.Is(err, tt.wantErr) || string(got.Value) != tt.want || got.Source != tt.source || got.KeychainFailure != tt.failure {
				t.Fatalf("unexpected resolution: source=%s, err=%v, failed=%v", got.Source, err, got.KeychainFailure)
			}
		})
	}
}

func TestAPIValidation(t *testing.T) {
	for _, tt := range []struct {
		name, id, hash string
		valid          bool
	}{
		{"valid", "12345", strings.Repeat("a", 32), true},
		{"trim", " 12345 ", " " + strings.Repeat("a", 32), true},
		{"zero", "0", strings.Repeat("a", 32), false},
		{"negative", "-1", strings.Repeat("a", 32), false},
		{"overflow", "2147483648", strings.Repeat("a", 32), false},
		{"garbage", "DO-NOT-PRINT", strings.Repeat("a", 32), false},
		{"hash length", "12345", "DO-NOT-PRINT", false},
		{"hash characters", "12345", strings.Repeat("z", 32), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{values: map[string][]byte{APIIDService: []byte(tt.id), APIHashService: []byte(tt.hash)}}
			_, err := resolver(store, map[string]string{APIIDEnv: "123", APIHashEnv: strings.Repeat("b", 32)}).LoadAPI(context.Background())
			if (err == nil) != tt.valid {
				t.Fatalf("unexpected validation: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "DO-NOT-PRINT") {
				t.Fatal("input leaked in error")
			}
		})
	}
}

func TestRedaction(t *testing.T) {
	for _, value := range []any{Secret{Value: []byte("DO-NOT-PRINT"), Source: "env"}, API{ID: 12345, Hash: "DO-NOT-PRINT"}} {
		if strings.Contains(fmt.Sprintf("%v", value), "DO-NOT-PRINT") {
			t.Fatal("secret String method leaked")
		}
	}
}

func TestCancellationDoesNotFallBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &memoryStore{values: map[string][]byte{}}
	_, err := resolver(store, map[string]string{APIIDEnv: "123"}).Read(ctx, APIIDService, APIIDEnv)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
