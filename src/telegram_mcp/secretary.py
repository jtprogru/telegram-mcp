"""The always-on half: poll Telegram, hand each incoming message to the agent.

This process holds the bot token and nothing else. It has no MTProto session, so
it cannot read the channel, other chats, or Saved Messages — which is the point.
An injected instruction inside an incoming message has no private data to reach
and no channel to reach it through, beyond answering the person who sent it.

Updates are long-polled rather than delivered by webhook: no domain, no public
HTTPS, nothing listening. Note that this makes the daemon the sole consumer of
the bot's update stream, so `make bot-connect` will conflict while it runs.
"""

import asyncio
import contextlib
import json
import logging
import signal
import sys
import time
from collections import defaultdict, deque
from typing import Any

from anthropic import AsyncAnthropic

from telegram_mcp import agent, botapi, business
from telegram_mcp.config import SecretaryConfig, load_bot_delivery

log = logging.getLogger("telegram-secretary")

BUSINESS_UPDATES = ["business_connection", "business_message", "edited_business_message"]
LONG_POLL_SECONDS = 25
BACKOFF_SECONDS = 5
HOUR = 3600


def _block_type(block: Any) -> str | None:
    """Blocks arrive as dicts from us and as SDK models from the runner."""
    return block.get("type") if isinstance(block, dict) else getattr(block, "type", None)


def _is_tool_result(message: Any) -> bool:
    content = message.get("content") if isinstance(message, dict) else None
    return isinstance(content, list) and bool(content) and _block_type(content[0]) == "tool_result"


def trim_history(messages: list[Any], limit: int) -> list[Any]:
    """Drop old turns without splitting a tool_use from its tool_result.

    Cutting mid-pair makes the next request invalid, so the trim only ever lands
    on a real user turn.
    """
    if len(messages) <= limit:
        return messages
    for index in range(len(messages) - limit, len(messages)):
        candidate = messages[index]
        if isinstance(candidate, dict) and candidate.get("role") == "user" and not _is_tool_result(candidate):
            return messages[index:]
    return []


class RateLimit:
    """A per-chat ceiling on replies, so a loop or a flood cannot run away."""

    def __init__(self, per_hour: int) -> None:
        self._per_hour = per_hour
        self._sent: dict[int, deque[float]] = defaultdict(deque)

    def allows(self, chat_id: int) -> bool:
        now = time.monotonic()
        recent = self._sent[chat_id]
        while recent and now - recent[0] > HOUR:
            recent.popleft()
        return len(recent) < self._per_hour

    def record(self, chat_id: int) -> None:
        self._sent[chat_id].append(time.monotonic())


class Secretary:
    def __init__(
        self, config: SecretaryConfig, token: str, connection_id: str, claude: AsyncAnthropic | None = None
    ) -> None:
        self.config = config
        self.token = token
        self.connection_id = connection_id
        # Injected so tests can build a Secretary without credentials of any kind.
        self.claude = claude or AsyncAnthropic()
        self.histories: dict[int, list[Any]] = {}
        self.limit = RateLimit(config.max_replies_per_hour)
        self.account: business.BusinessAccount | None = None
        self.owner_id: int | None = None
        self._state = config.state_dir / "secretary-state.json"
        self._stop = asyncio.Event()

    # ---------- state ----------

    def _load_offset(self) -> int:
        try:
            return int(json.loads(self._state.read_text())["offset"])
        except (OSError, ValueError, KeyError, TypeError):
            return 0

    def _save_offset(self, offset: int) -> None:
        try:
            self._state.parent.mkdir(parents=True, exist_ok=True)
            self._state.write_text(json.dumps({"offset": offset}))
        except OSError as exc:
            log.warning("could not persist the update offset: %s", exc)

    # ---------- lifecycle ----------

    async def refresh_connection(self) -> None:
        connection = await botapi.call(
            self.token, "getBusinessConnection", {"business_connection_id": self.connection_id}
        )
        rights = business.Rights.from_connection(connection)
        self.owner_id = (connection.get("user") or {}).get("id")
        self.account = business.BusinessAccount(self.token, self.connection_id, rights)
        usable = rights.usable(allow_irreversible=self.config.allow_irreversible)
        log.info("connection granted: %s", rights.describe())
        log.info("agent may use: %s", ", ".join(sorted(usable)) or "nothing")
        withheld = rights.granted - usable
        if withheld:
            log.info(
                "withheld locally (set TELEGRAM_SECRETARY_IRREVERSIBLE=1 to allow): %s", ", ".join(sorted(withheld))
            )
        if not rights.enabled:
            log.warning("the business connection is currently disabled in Telegram")

    def stop(self) -> None:
        self._stop.set()

    # ---------- updates ----------

    async def run(self) -> None:
        await self.refresh_connection()
        offset = self._load_offset()
        log.info("listening for business updates")

        while not self._stop.is_set():
            try:
                updates = await botapi.call(
                    self.token,
                    "getUpdates",
                    {"offset": offset, "timeout": LONG_POLL_SECONDS, "limit": 50, "allowed_updates": BUSINESS_UPDATES},
                    read_timeout=LONG_POLL_SECONDS,
                )
            except botapi.BotAPIError as exc:
                log.error("polling failed, backing off: %s", exc)
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(self._stop.wait(), BACKOFF_SECONDS)
                continue

            for update in updates:
                offset = int(update["update_id"]) + 1
                try:
                    await self.dispatch(update)
                except Exception:  # one bad message must not take the daemon down
                    log.exception("failed to handle update %s", update.get("update_id"))
            if updates:
                self._save_offset(offset)

    async def dispatch(self, update: dict[str, Any]) -> None:
        if "business_connection" in update:
            log.info("business connection changed, reloading rights")
            await self.refresh_connection()
            return

        message = update.get("business_message")
        if not message:
            return  # edited messages are context, not a prompt to act on
        if message.get("business_connection_id") != self.connection_id:
            return

        sender = message.get("from") or {}
        if self.owner_id is not None and sender.get("id") == self.owner_id:
            return  # the account holder answering in their own chat; stay out of the way

        chat_id = (message.get("chat") or {}).get("id")
        text = message.get("text") or message.get("caption")
        if chat_id is None or not text:
            return  # media without a caption is not something to answer blind

        if not self.limit.allows(chat_id):
            log.warning("rate limit reached for chat %s, not replying", chat_id)
            return

        await self.answer(chat_id, sender, text, int(message["message_id"]))

    async def answer(self, chat_id: int, sender: dict[str, Any], text: str, message_id: int) -> None:
        assert self.account is not None
        label = (
            sender.get("username")
            or " ".join(part for part in (sender.get("first_name"), sender.get("last_name")) if part)
            or str(sender.get("id", chat_id))
        )

        log.info("answering %s in chat %s", label, chat_id)
        history = await agent.handle(
            self.claude,
            self.config,
            self.account,
            chat_id,
            label,
            self.histories.get(chat_id, []),
            text,
            message_id,
        )
        self.histories[chat_id] = trim_history(history, self.config.history_messages)
        self.limit.record(chat_id)


async def serve() -> None:
    config = SecretaryConfig.from_env()
    delivery = load_bot_delivery()
    if delivery is None:
        raise RuntimeError(
            "no bot token stored. See the README: create the bot with @BotFather, connect it in "
            "Telegram Business, then run `make bot-connect`."
        )

    secretary = Secretary(config, delivery.token, delivery.connection_id)
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        with contextlib.suppress(NotImplementedError):
            loop.add_signal_handler(sig, secretary.stop)
    await secretary.run()
    log.info("stopped")


def main() -> None:
    logging.basicConfig(
        stream=sys.stderr,
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    try:
        asyncio.run(serve())
    except KeyboardInterrupt:
        pass
    except Exception as exc:  # noqa: BLE001 - a startup failure should read as one line, not a traceback
        log.error("secretary failed to start: %s", exc)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
