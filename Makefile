.DEFAULT_GOAL := help

.PHONY: help install lint fmt test ci login bot-connect run secretary clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Install dependencies into .venv
	uv sync

lint: ## Ruff: lint + formatting check
	uv run ruff check
	uv run ruff format --check

fmt: ## Ruff: autofix lint findings and reformat
	uv run ruff check --fix
	uv run ruff format

test: ## Run the test suite
	uv run pytest

ci: lint test ## Everything CI runs

login: ## Interactive Telegram login; stores the session string in the Keychain
	uv run python scripts/login.py

bot-connect: ## Fetch the connected bot's business connection id into the Keychain
	uv run python scripts/connect_bot.py

run: ## Run the MCP server on stdio (for manual poking; Claude Code starts it itself)
	uv run telegram-mcp

secretary: ## Run the business-account secretary daemon in the foreground
	uv run telegram-secretary

clean: ## Remove venv and tool caches
	rm -rf .venv .pytest_cache .ruff_cache
