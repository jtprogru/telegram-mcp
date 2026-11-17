package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadFileLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name                          string
		size                          int64
		cancel, oversize, early, fail bool
	}{
		{name: "success", size: 3},
		{name: "announced oversize", size: MaxDownloadBytes + 1},
		{name: "oversize chunk", oversize: true},
		{name: "truncated", size: 6, early: true},
		{name: "network failure", fail: true},
		{name: "cancelled", cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			existing := filepath.Join(dir, "existing.bin")
			if err := os.WriteFile(existing, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result, err := saveMedia(ctx, dir, 12, tt.size, func(_ context.Context, offset int64) ([]byte, error) {
				if offset != 0 {
					t.Fatal("unexpected download offset")
				}
				if tt.fail {
					return nil, errors.New("network")
				}
				if tt.cancel {
					cancel()
				}
				if tt.oversize {
					return make([]byte, downloadPart+1), nil
				}
				return []byte("abc"), nil
			})
			success := tt.name == "success"
			if (err == nil) != success {
				t.Fatalf("unexpected result: %s err=%v", result, err)
			}
			files, e := os.ReadDir(dir)
			if e != nil {
				t.Fatal(e)
			}
			wanted := 1
			if success {
				wanted = 2
			}
			if len(files) != wanted {
				t.Fatal("partial file not cleaned or successful file missing")
			}
			data, e := os.ReadFile(existing)
			if e != nil || string(data) != "keep" {
				t.Fatal("overwrote existing file")
			}
			if success {
				for _, f := range files {
					if strings.HasPrefix(f.Name(), "telegram-") {
						info, e := f.Info()
						if e != nil || info.Size() != 3 || info.Mode().Perm() != 0600 {
							t.Fatal("unsafe file permissions/size")
						}
					}
				}
			}
		})
	}
}
func TestDestinationWriteError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := saveMedia(context.Background(), file, 1, 0, func(context.Context, int64) ([]byte, error) {
		t.Fatal("fetch began with invalid destination")
		return nil, nil
	})
	if err == nil {
		t.Fatal("file destination accepted as directory")
	}
}
