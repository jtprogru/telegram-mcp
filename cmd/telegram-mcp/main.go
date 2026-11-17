package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jtprogru/telegram-mcp/internal/cli"
	"github.com/jtprogru/telegram-mcp/internal/credentials"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := cli.New(credentials.Resolver{
		Store: credentials.Keychain{Account: os.Getenv("USER")}, LookupEnv: os.LookupEnv,
	})
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "telegram-mcp:", err)
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}
