package credentials

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock prevents concurrent Go auth-key/session use. The lock file contains no secrets.
// It cannot detect an old Python process: that process still must be stopped manually.
type Lock struct{ file *os.File }

// AcquireLock takes a nonblocking process lock in the owner's private cache directory.
func AcquireLock(ctx context.Context) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, errors.New("cannot locate session lock directory")
	}
	return lockAt(filepath.Join(base, "telegram-mcp"))
}
func lockAt(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create session lock directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("cannot open session lock directory")
	}
	f, err := root.OpenFile("session.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	closeErr := root.Close()
	if err != nil {
		return nil, errors.New("cannot open session lock")
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, f.Close())
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(errors.New("another Go process is using the Telegram session; stop it and retry"), f.Close())
	}
	return &Lock{file: f}, nil
}

// Close releases the kernel lock, including on errors/cancellation; a stale file is harmless.
func (l *Lock) Close() error { return l.file.Close() }
