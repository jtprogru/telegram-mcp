"""Credentials and the send allowlist.

Secrets live in the macOS Keychain, not in files: a session string is full
access to the Telegram account, so it never lands in .env or .mcp.json.
Environment variables are a fallback for headless runs where the Keychain is
locked or unavailable.
"""

import os
import subprocess
from dataclasses import dataclass
from pathlib import Path

KEYCHAIN_ACCOUNT = os.environ.get("USER", "")

API_ID_SERVICE = "telegram-mcp-api-id"
API_HASH_SERVICE = "telegram-mcp-api-hash"
SESSION_SERVICE = "telegram-mcp-session"
BOT_TOKEN_SERVICE = "telegram-mcp-bot-token"
CONNECTION_SERVICE = "telegram-mcp-business-connection"

API_ID_ENV = "TELEGRAM_MCP_API_ID"
API_HASH_ENV = "TELEGRAM_MCP_API_HASH"
SESSION_ENV = "TELEGRAM_MCP_SESSION"
BOT_TOKEN_ENV = "TELEGRAM_MCP_BOT_TOKEN"
CONNECTION_ENV = "TELEGRAM_MCP_BUSINESS_CONNECTION"
ALLOWLIST_ENV = "TELEGRAM_MCP_SEND_ALLOWLIST"
AGENT_MARKER_ENV = "TELEGRAM_MCP_AGENT_MARKER"

DEFAULT_SEND_ALLOWLIST = "jtprogru_channel,me"
DEFAULT_AGENT_MARKER = "🤖"

_KEYCHAIN_TIMEOUT = 15


def keychain_get(service: str, account: str | None = None) -> str | None:
    """Read a generic password from the Keychain, or None if it is not there."""
    try:
        result = subprocess.run(
            ["security", "find-generic-password", "-a", account or KEYCHAIN_ACCOUNT, "-s", service, "-w"],
            capture_output=True,
            text=True,
            timeout=_KEYCHAIN_TIMEOUT,
        )
    except (OSError, subprocess.SubprocessError):
        return None  # non-macOS or the tool is missing: fall back to env
    if result.returncode != 0:
        return None
    return result.stdout.strip() or None


def keychain_set(service: str, value: str, account: str | None = None) -> None:
    """Store (or replace) a generic password in the Keychain.

    The value goes through argv, so it is briefly visible to `ps` for other
    processes of the same user. Acceptable here: the writer is the interactive
    login script, and anything able to read that argv can read the Keychain too.
    """
    subprocess.run(
        ["security", "add-generic-password", "-U", "-a", account or KEYCHAIN_ACCOUNT, "-s", service, "-w", value],
        check=True,
        capture_output=True,
        text=True,
        timeout=_KEYCHAIN_TIMEOUT,
    )


def _secret(service: str, env: str) -> str | None:
    return keychain_get(service) or os.environ.get(env) or None


def normalize_peer(peer: str | int) -> str:
    """Reduce a peer reference to a comparable key: `@Foo`, `t.me/foo/` and `foo` all match."""
    if isinstance(peer, int):
        return str(peer)
    value = peer.strip()
    lowered = value.lower()
    for prefix in ("https://t.me/", "http://t.me/", "t.me/"):
        if lowered.startswith(prefix):
            value = value[len(prefix) :]
            break
    return value.strip("/").lstrip("@").lower()


def parse_allowlist(raw: str | None) -> frozenset[str]:
    """Parse a comma-separated allowlist; an empty value means the default one."""
    source = raw if raw is not None and raw.strip() else DEFAULT_SEND_ALLOWLIST
    return frozenset(normalize_peer(item) for item in source.split(",") if item.strip())


def load_marker() -> str:
    """The badge stamped on every message sent to a person.

    It can be changed, but not switched off: an empty value falls back to the
    default rather than producing an unmarked message.
    """
    return os.environ.get(AGENT_MARKER_ENV, "").strip() or DEFAULT_AGENT_MARKER


def load_allowlist() -> frozenset[str]:
    """Peers tg_send may write to.

    Deliberately independent of the credentials: the gate must hold even when
    the session is missing or broken.
    """
    return parse_allowlist(os.environ.get(ALLOWLIST_ENV))


def is_send_allowed(peer: str | int, allowlist: frozenset[str]) -> bool:
    return normalize_peer(peer) in allowlist


@dataclass(frozen=True)
class Settings:
    api_id: int
    api_hash: str
    session: str


def load_api_credentials() -> tuple[int, str]:
    """api_id/api_hash from my.telegram.org, needed both by the server and by the login script."""
    api_id = _secret(API_ID_SERVICE, API_ID_ENV)
    api_hash = _secret(API_HASH_SERVICE, API_HASH_ENV)
    if not api_id or not api_hash:
        raise RuntimeError(
            "Telegram API credentials are missing. Get them at https://my.telegram.org/apps and store them:\n"
            f'  security add-generic-password -a "$USER" -s {API_ID_SERVICE}   -w "<api_id>"\n'
            f'  security add-generic-password -a "$USER" -s {API_HASH_SERVICE} -w "<api_hash>"'
        )
    try:
        return int(api_id), api_hash
    except ValueError as exc:
        raise RuntimeError(f"{API_ID_SERVICE} is not a number: {api_id!r}") from exc


@dataclass(frozen=True)
class BotDelivery:
    """A bot connected to the account through Telegram Business.

    Messages sent this way carry via_business_bot_id, which is provenance at the
    protocol level rather than a convention in the text.
    """

    token: str
    connection_id: str


def load_bot_token() -> str | None:
    return _secret(BOT_TOKEN_SERVICE, BOT_TOKEN_ENV)


def load_bot_delivery() -> BotDelivery | None:
    """The bot delivery path, or None when it was never set up.

    A token without a connection id is a half-finished setup, not a reason to
    quietly fall back: it would send as the account itself and lose the very
    attribution the bot was added for.
    """
    token = load_bot_token()
    if not token:
        return None
    connection_id = _secret(CONNECTION_SERVICE, CONNECTION_ENV)
    if not connection_id:
        raise RuntimeError(
            f"a bot token is stored in {BOT_TOKEN_SERVICE} but its business connection is not. "
            "Run `make bot-connect` to fetch it, or remove the token to send as the account instead."
        )
    return BotDelivery(token=token, connection_id=connection_id)


SECRETARY_ENV = "TELEGRAM_SECRETARY_"
DEFAULT_MODEL = "claude-opus-5"
DEFAULT_EFFORT = "medium"


def _flag(name: str, default: bool = False) -> bool:
    raw = os.environ.get(SECRETARY_ENV + name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _number(name: str, default: int) -> int:
    raw = os.environ.get(SECRETARY_ENV + name)
    try:
        return int(raw) if raw else default
    except ValueError as exc:
        raise RuntimeError(f"{SECRETARY_ENV}{name} must be a number, got {raw!r}") from exc


@dataclass(frozen=True)
class SecretaryConfig:
    """Everything the daemon needs that is not a credential."""

    owner: str
    model: str
    effort: str
    marker: str
    allow_irreversible: bool
    history_messages: int
    max_replies_per_hour: int
    state_dir: Path

    @classmethod
    def from_env(cls) -> "SecretaryConfig":
        return cls(
            owner=os.environ.get(SECRETARY_ENV + "OWNER", "the account holder"),
            model=os.environ.get(SECRETARY_ENV + "MODEL", DEFAULT_MODEL),
            effort=os.environ.get(SECRETARY_ENV + "EFFORT", DEFAULT_EFFORT),
            marker=load_marker(),
            # Telegram's grant is the outer bound on what the bot may do; this is the
            # inner one. Off by default: money and identity should not be reachable
            # from a stranger's first message.
            allow_irreversible=_flag("IRREVERSIBLE"),
            history_messages=_number("HISTORY", 24),
            max_replies_per_hour=_number("MAX_REPLIES_PER_HOUR", 20),
            state_dir=Path(os.environ.get(SECRETARY_ENV + "STATE_DIR", "~/.local/state/telegram-mcp")).expanduser(),
        )


def load_settings() -> Settings:
    api_id, api_hash = load_api_credentials()
    session = _secret(SESSION_SERVICE, SESSION_ENV)
    if not session:
        raise RuntimeError("no Telegram session stored. Run `make login` in a terminal to create one.")
    return Settings(api_id=api_id, api_hash=api_hash, session=session)
