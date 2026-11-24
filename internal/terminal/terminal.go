// Package terminal provides interactive-only QR display and cancellable hidden input.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// UI owns a controlling terminal; QR codes and input never go to redirected streams.
type UI struct{ tty *os.File }

// Open refuses redirected stdin or stderr, even when /dev/tty is available.
// Call before reading credentials or exporting a login token.
func Open() (*UI, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil, errors.New("interactive stdin and stderr terminals required; run this command yourself in a terminal (do not redirect or record QR/credentials)")
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("could not open controlling terminal")
	}
	return &UI{tty: f}, nil
}

// Close releases the controlling terminal.
func (u *UI) Close() error { return u.tty.Close() }

// Message writes fixed, non-secret guidance to the terminal.
func (u *UI) Message(message string) error {
	_, err := fmt.Fprintln(u.tty, message)
	return err
}

// QR renders a QR bitmap in process, not its bearer URL. Each refresh clears the screen.
func (u *UI) QR(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	code, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return errors.New("could not render login QR")
	}
	_, err = fmt.Fprint(u.tty, "\x1b[2J\x1b[HScan in Telegram: Settings → Devices → Link Desktop Device. Ctrl+C cancels.\n", code.ToSmallString(false))
	return err
}

// ClearQR removes the displayed token from the active terminal screen.
func (u *UI) ClearQR() error {
	_, err := fmt.Fprint(u.tty, "\x1b[2J\x1b[H")
	return err
}

// Secret reads without echo, restoring terminal state even on cancellation.
// Timed readiness checks bound cancellation latency without a reader goroutine.
func (u *UI) Secret(ctx context.Context, label string) (value string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fd := int(u.tty.Fd())
	if fd < 0 || fd >= unix.FD_SETSIZE {
		return "", errors.New("terminal descriptor is outside the select range")
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", errors.New("could not disable terminal echo")
	}
	defer func() {
		if restoreErr := term.Restore(fd, state); restoreErr != nil {
			value, err = "", errors.New("could not restore terminal state")
		}
		if _, writeErr := fmt.Fprint(u.tty, "\n"); writeErr != nil && err == nil {
			value, err = "", writeErr
		}
	}()
	if _, err := fmt.Fprint(u.tty, label); err != nil {
		return "", err
	}
	buf := make([]byte, 0, 128)
	defer func() { clear(buf[:cap(buf)]) }()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Darwin poll reports POLLNVAL for the /dev/tty alias even when it
		// is readable. select supports it; reset both in/out arguments.
		var read unix.FdSet
		read.Set(fd)
		tv := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
		n, err := unix.Select(fd+1, &read, nil, nil, &tv)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return "", errors.New("could not read terminal")
		}
		if n == 0 {
			continue
		}
		var b [1]byte
		if n, err := u.tty.Read(b[:]); err != nil || n != 1 {
			return "", errors.New("could not read terminal")
		}
		switch b[0] {
		case 3, 4: // Ctrl+C / Ctrl+D in raw mode.
			return "", context.Canceled
		case '\r', '\n':
			return string(buf), nil
		case 8, 127:
			if len(buf) > 0 {
				_, size := utf8.DecodeLastRune(buf)
				buf = buf[:len(buf)-size]
			}
		default:
			if b[0] >= 32 {
				if len(buf) >= 4096 {
					return "", errors.New("terminal input too long")
				}
				buf = append(buf, b[0])
			}
		}
	}
}
