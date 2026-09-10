"""A single lazily-connected Telethon client for the lifetime of the server."""

import asyncio
import functools
from collections.abc import Awaitable, Callable
from typing import Any

from mcp.server.mcpserver.exceptions import ToolError
from telethon import TelegramClient
from telethon.errors import FloodWaitError
from telethon.sessions import StringSession

from telegram_mcp.config import load_settings

_client: TelegramClient | None = None
_lock = asyncio.Lock()


# Only ToolError reaches the model with its text; anything else is reported as a
# bare "Error executing tool <name>". Deliberate failures therefore mix ToolError
# with the builtin that describes them, so callers and tests keep the usual types.
class ToolValueError(ToolError, ValueError):
    """Bad arguments."""


class ToolPermissionError(ToolError, PermissionError):
    """Refused by the allowlist."""


class ToolRuntimeError(ToolError, RuntimeError):
    """Missing credentials, dead session, and other state problems."""


class FloodWait(ToolRuntimeError):
    """Telegram asked us to back off. Reported, never slept through."""

    def __init__(self, seconds: int) -> None:
        super().__init__(f"Telegram rate limit hit: wait {seconds}s before retrying this call")
        self.seconds = seconds


async def get_client() -> TelegramClient:
    """Connect on first use and reuse the connection afterwards."""
    global _client
    async with _lock:
        if _client is not None and _client.is_connected():
            return _client
        settings = load_settings()
        client = TelegramClient(StringSession(settings.session), settings.api_id, settings.api_hash)
        await client.connect()
        if not await client.is_user_authorized():
            await client.disconnect()
            raise RuntimeError("the stored Telegram session is not authorized any more. Run `make login` again.")
        _client = client
        return client


async def close_client() -> None:
    global _client
    if _client is not None:
        await _client.disconnect()
        _client = None


def guard[T](func: Callable[..., Awaitable[T]]) -> Callable[..., Awaitable[T]]:
    """Make failures legible: rate limiting becomes an error with a number of seconds
    instead of a silent stall, and every deliberate failure keeps its message on the way out."""

    @functools.wraps(func)
    async def wrapper(*args: Any, **kwargs: Any) -> T:
        try:
            return await func(*args, **kwargs)
        except FloodWaitError as exc:
            raise FloodWait(exc.seconds) from exc
        except ToolError:
            raise
        except PermissionError as exc:
            raise ToolPermissionError(str(exc)) from exc
        except ValueError as exc:
            raise ToolValueError(str(exc)) from exc
        except RuntimeError as exc:
            raise ToolRuntimeError(str(exc)) from exc

    return wrapper
