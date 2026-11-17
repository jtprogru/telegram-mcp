// Package cli contains the stdio MCP, setup and diagnostics commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtprogru/telegram-mcp/internal/config"
	"github.com/jtprogru/telegram-mcp/internal/credentials"
	"github.com/jtprogru/telegram-mcp/internal/mcpserver"
	tg "github.com/jtprogru/telegram-mcp/internal/telegram"
	"github.com/jtprogru/telegram-mcp/internal/terminal"
)

// New returns an independent command tree with injectable dependencies for offline tests.
func New(resolver credentials.Resolver) *cobra.Command {
	root := &cobra.Command{
		Use: "telegram-mcp", Short: "Telegram user-account MCP",
		Long:         "Bounded Telegram tools over stdio, with macOS Keychain and QR/2FA setup.\nDelivery is directly from your account, not through Business/Bot API.",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(&cobra.Command{
		Use: "serve", Short: "Run MCP over stdio (no interactive login)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := config.Load(os.Getenv)
			backend := &tg.Service{Resolver: resolver, Settings: settings}
			return mcpserver.RunStdio(cmd.Context(), backend, settings, os.Stdin, os.Stdout)
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "init", Short: "Store missing API credentials in macOS Keychain (interactive)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			ui, err := terminal.Open()
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, ui.Close()) }()
			return initialize(cmd.Context(), resolver, ui)
		},
	})
	auth := &cobra.Command{Use: "auth", Short: "Manage the Go user session"}
	var qr bool
	login := &cobra.Command{
		Use: "login --qr", Short: "QR login, with a hidden cloud-password prompt when needed", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			if !qr {
				return errors.New("only QR login is supported; pass --qr")
			}
			ui, err := terminal.Open()
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, ui.Close()) }()
			lock, err := credentials.AcquireLock(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, lock.Close()) }()
			api, err := resolver.LoadAPI(cmd.Context())
			if err != nil {
				return err
			}
			if err := ui.Message("Stop the Python MCP first. This creates/replaces only the Go session; old secrets are not removed."); err != nil {
				return err
			}
			if err := tg.Login(cmd.Context(), api, credentials.NewSession(resolver.Store), ui); err != nil {
				return err
			}
			return ui.Message("Authorized and saved to macOS Keychain. Verify a fresh process with: telegram-mcp doctor --network")
		},
	}
	login.Flags().BoolVar(&qr, "qr", false, "use Telegram QR login (required)")
	auth.AddCommand(login)
	root.AddCommand(auth)

	var network bool
	var timeout time.Duration
	doctor := &cobra.Command{
		Use: "doctor", Short: "Report secret sources locally; opt in to online session verification", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			if timeout <= 0 || timeout > 5*time.Minute {
				return errors.New("--timeout must be greater than zero and at most 5m")
			}
			if err := diagnose(cmd.Context(), resolver, cmd.OutOrStdout()); err != nil {
				return err
			}
			if !network {
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			lock, err := credentials.AcquireLock(ctx)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, lock.Close()) }()
			api, err := resolver.LoadAPI(ctx)
			if err != nil {
				return err
			}
			staged, source, err := resolver.PrepareSession(ctx)
			if err != nil {
				return err
			}
			if err := tg.Check(ctx, api, staged); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Network: authorized (%s); Go session saved to Keychain. No messages sent.\n", source)
			return err
		},
	}
	doctor.Flags().BoolVar(&network, "network", false, "contact Telegram and save verified Go session; STOP Python MCP before importing")
	doctor.Flags().DurationVar(&timeout, "timeout", 45*time.Second, "online verification timeout")
	root.AddCommand(doctor)
	return root
}

type setupUI interface {
	Secret(context.Context, string) (string, error)
	Message(string) error
}

func initialize(ctx context.Context, resolver credentials.Resolver, ui setupUI) error {
	for _, item := range []struct {
		service, env, prompt string
		validate             func([]byte) error
	}{
		{credentials.APIIDService, credentials.APIIDEnv, "API ID (hidden): ", credentials.ValidateID},
		{credentials.APIHashService, credentials.APIHashEnv, "API hash (hidden): ", credentials.ValidateHash},
	} {
		secret, err := resolver.Read(ctx, item.service, item.env)
		if err == nil {
			if err := item.validate(secret.Value); err != nil {
				return fmt.Errorf("%s (%s): %w; existing value was not changed", item.service, secret.Source, err)
			}
			if err := ui.Message(item.service + ": already configured (" + secret.Source + ")"); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, credentials.ErrNotFound) {
			return err
		}
		value, err := ui.Secret(ctx, item.prompt)
		if err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if err := item.validate([]byte(value)); err != nil {
			return err
		}
		if err := resolver.Store.Put(ctx, item.service, []byte(value)); err != nil {
			return fmt.Errorf("could not save %s: %w", item.service, err)
		}
	}
	return ui.Message("API credentials ready. Next: telegram-mcp auth login --qr")
}

func diagnose(ctx context.Context, resolver credentials.Resolver, out io.Writer) error {
	if _, err := fmt.Fprintln(out, "Delivery: direct MTProto from your user account (no Business attribution). Old bot credentials are ignored, not deleted."); err != nil {
		return err
	}
	settings := config.Load(os.Getenv)
	peers := make([]string, 0, len(settings.Allowlist))
	for peer := range settings.Allowlist {
		peers = append(peers, peer)
	}
	sort.Strings(peers)
	if _, err := fmt.Fprintf(out, "Send allowlist: %s\nAgent marker: enabled (broadcast posts are exempt)\n", strings.Join(peers, ", ")); err != nil {
		return err
	}
	var problems []error
	for _, item := range []struct {
		service, env string
		validate     func([]byte) error
	}{
		{credentials.APIIDService, credentials.APIIDEnv, credentials.ValidateID},
		{credentials.APIHashService, credentials.APIHashEnv, credentials.ValidateHash},
	} {
		secret, err := resolver.Read(ctx, item.service, item.env)
		if err == nil {
			err = item.validate(secret.Value)
		}
		status := "valid (format only)"
		if err != nil {
			status = err.Error()
			problems = append(problems, err)
		}
		if secret.KeychainFailure {
			status += "; Keychain inaccessible"
		}
		if _, err := fmt.Fprintf(out, "%s: %s [%s]\n", item.service, status, secret.Source); err != nil {
			return err
		}
	}
	_, source, err := resolver.PrepareSession(ctx)
	if err != nil {
		problems = append(problems, err)
		if _, writeErr := fmt.Fprintf(out, "Session: %s\n", err); writeErr != nil {
			return writeErr
		}
	} else {
		if _, err := fmt.Fprintf(out, "Session: %s; decodable, NOT verified online\n", source); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out, "No Telegram requests made. Use --network to verify authorization and persist a Go session; stop Python MCP before import."); err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New("local diagnostics found missing/invalid credentials or session (see above)")
	}
	return nil
}
