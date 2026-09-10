"""Find the business connection of the connected bot and store it in the Keychain.

The connection id only ever exists on the bot's side: the account can see *that*
a bot is connected (account.getConnectedBots), but not under which id. So the id
has to be read out of the bot's own update stream.
"""

import asyncio
import sys

from telegram_mcp import botapi
from telegram_mcp.config import BOT_TOKEN_ENV, BOT_TOKEN_SERVICE, CONNECTION_SERVICE, keychain_set, load_bot_token

BUSINESS_UPDATES = ["business_connection", "business_message", "edited_business_message"]
LONG_POLL_SECONDS = 15
ROUNDS = 4


def _connection_from(update: dict) -> tuple[str | None, dict | None]:
    connection = update.get("business_connection")
    if connection and connection.get("id"):
        return connection["id"], connection
    for key in ("business_message", "edited_business_message"):
        message = update.get(key) or {}
        if message.get("business_connection_id"):
            return message["business_connection_id"], None
    return None, None


def _can_reply(connection: dict | None) -> bool | None:
    """None when the update did not say; Telegram moved this from a flag to a rights object."""
    if connection is None:
        return None
    rights = connection.get("rights")
    if isinstance(rights, dict):
        return bool(rights.get("can_reply"))
    if "can_reply" in connection:
        return bool(connection["can_reply"])
    return None


async def discover(token: str) -> tuple[str, dict | None]:
    for attempt in range(ROUNDS + 1):
        timeout = 0 if attempt == 0 else LONG_POLL_SECONDS
        updates = await botapi.call(
            token,
            "getUpdates",
            {"timeout": timeout, "limit": 100, "allowed_updates": BUSINESS_UPDATES},
            read_timeout=timeout,
        )
        for update in updates:
            connection_id, connection = _connection_from(update)
            if connection_id:
                return connection_id, connection
        if attempt == 0:
            print(
                "No business update is pending yet. In Telegram open Settings → Telegram Business → Chatbots\n"
                "and toggle the bot off and on; that makes Telegram resend the connection.\n"
                f"Listening for {LONG_POLL_SECONDS * ROUNDS}s…"
            )
    raise RuntimeError(
        "no business connection arrived. Check that the bot is actually connected to this account, "
        "that it has no webhook set (a webhook takes the updates instead), and try again."
    )


async def connect() -> None:
    token = load_bot_token()
    if not token:
        raise RuntimeError(
            "no bot token stored. Create the bot with @BotFather, connect it in Telegram Business, then:\n"
            f'  security add-generic-password -a "$USER" -s {BOT_TOKEN_SERVICE} -w "<token>"\n'
            f"  (or set {BOT_TOKEN_ENV})"
        )

    bot = await botapi.whoami(token)
    print(f"Bot: {bot}")
    connection_id, connection = await discover(token)
    keychain_set(CONNECTION_SERVICE, connection_id)

    reply = _can_reply(connection)
    if reply is False:
        print("Warning: this connection does not grant the bot the right to reply; messages will be refused.")
    print(f"OK: business connection for {bot} saved to Keychain as {CONNECTION_SERVICE}")


def main() -> None:
    try:
        asyncio.run(connect())
    except KeyboardInterrupt:
        print("aborted", file=sys.stderr)
        raise SystemExit(1) from None
    except Exception as exc:  # noqa: BLE001 - the message matters more than the traceback here
        print(f"bot-connect failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
