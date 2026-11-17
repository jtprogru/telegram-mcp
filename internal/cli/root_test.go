package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jtprogru/telegram-mcp/internal/credentials"
)

type fakeStore struct {
	reads  int
	writes int
	values map[string][]byte
}

func (s *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	s.reads++
	v, ok := s.values[key]
	if !ok {
		return nil, credentials.ErrNotFound
	}
	return v, nil
}
func (s *fakeStore) Put(_ context.Context, key string, v []byte) error {
	s.writes++
	s.values[key] = bytes.Clone(v)
	return nil
}

type fakeSetup struct {
	prompts int
}

func (s *fakeSetup) Secret(context.Context, string) (string, error) {
	s.prompts++
	return "invalid-test-input", nil
}
func (*fakeSetup) Message(string) error { return nil }

func TestDoctorRedaction(t *testing.T) {
	store := &fakeStore{values: map[string][]byte{
		credentials.APIIDService:  []byte("DO-NOT-PRINT"),
		credentials.LegacyService: []byte("DO-NOT-PRINT"),
	}}
	root := New(credentials.Resolver{Store: store, LookupEnv: func(string) (string, bool) { return "", false }})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"doctor"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("invalid/missing credentials not diagnosed")
	}
	if strings.Contains(out.String(), "DO-NOT-PRINT") {
		t.Fatal("diagnostics leaked input")
	}
	if store.writes != 0 || !strings.Contains(out.String(), "No Telegram requests made") || !strings.Contains(out.String(), "Old bot credentials are ignored") {
		t.Fatal("offline diagnostics contract violated")
	}
}

func TestHelpAndValidationNeverReadSecrets(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"auth", "login", "--help"}, {"doctor", "extra"}, {"auth", "login"}, {"doctor", "--timeout=0"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			store := &fakeStore{}
			root := New(credentials.Resolver{Store: store})
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)
			// Errors are expected for invalid arguments; none may cause secret access.
			_ = root.ExecuteContext(context.Background())
			if store.reads != 0 {
				t.Fatal("help/invalid invocation read secrets")
			}
		})
	}
}

func TestInitPreservesExistingCredentials(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid", false: "invalid"}[valid], func(t *testing.T) {
			id := "12345"
			if !valid {
				id = "DO-NOT-PRINT"
			}
			store := &fakeStore{values: map[string][]byte{credentials.APIIDService: []byte(id), credentials.APIHashService: []byte(strings.Repeat("a", 32))}}
			ui := &fakeSetup{}
			err := initialize(context.Background(), credentials.Resolver{Store: store}, ui)
			if (err == nil) != valid || ui.prompts != 0 || store.writes != 0 {
				t.Fatal("init changed or requested existing credentials")
			}
			if err != nil && strings.Contains(err.Error(), "DO-NOT-PRINT") {
				t.Fatal("init leaked input")
			}
		})
	}
}
