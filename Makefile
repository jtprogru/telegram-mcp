.DEFAULT_GOAL := help

.PHONY: help build fmt fmt-check lint vet test test-race check init login login-qr doctor run clean

# ANSI colours. They are only emitted when the recipe's own stdout is an
# interactive terminal, so piping into a file/CI log or NO_COLOR=1 stays plain.
ESC       := $(shell printf '\033')
C_RESET   := $(ESC)[0m
C_TARGET  := $(ESC)[36m
C_COMMENT := $(ESC)[35m
C_ERROR   := $(ESC)[31m
C_OK      := $(ESC)[32m

# Shell prologue for recipes: sets $t, $c, $e, $ok, $r to colours or to empty
# strings. Use as: @$(colors); printf ...
define colors
t=''; c=''; e=''; ok=''; r=''; \
if [ -t 1 ] && [ -z "$${NO_COLOR}" ]; then \
	t='$(C_TARGET)'; c='$(C_COMMENT)'; e='$(C_ERROR)'; ok='$(C_OK)'; r='$(C_RESET)'; \
fi
endef

help: ## Show local development targets
	@$(colors); \
	awk -v t="$$t" -v c="$$c" -v r="$$r" 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*?##/ {printf "  %s%-15s%s %s%s%s\n", t, $$1, r, c, $$2, r}' $(MAKEFILE_LIST)

build: ## Build the macOS binary in bin/
	go build -o bin/telegram-mcp ./cmd/telegram-mcp

fmt: ## Format Go source and tests
	gofmt -w cmd internal

fmt-check: ## Check Go formatting without changing files
	@$(colors); files="$$(gofmt -l cmd internal)" || exit $$?; \
	if [ -n "$$files" ]; then printf '%s\n%s\n' "$${e}unformatted Go files:$${r}" "$$files" >&2; exit 1; fi

lint: fmt-check ## Check formatting and run golangci-lint
	golangci-lint run ./...

vet: ## Run go vet
	go vet ./...

test: ## Run offline tests (no real Telegram or Keychain access)
	go test ./...

test-race: ## Run offline tests with the race detector
	go test -race ./...

check: vet test test-race lint ## Run all local checks; no CI or release automation
	@$(colors); printf '%s\n' "$${ok}all local checks passed$${r}"

init: build ## Interactively store missing API credentials in Keychain
	./bin/telegram-mcp init

login: login-qr ## Alias for interactive QR/2FA login

login-qr: build ## Log in via built-in QR and optional hidden cloud-password prompt
	./bin/telegram-mcp auth login --qr

doctor: build ## Diagnose locally without contacting Telegram
	./bin/telegram-mcp doctor

run: build ## Run MCP on stdio (normally the MCP client starts it)
	./bin/telegram-mcp serve

clean: ## Remove only the locally built binary; never secrets or user data
	rm -f -- bin/telegram-mcp
