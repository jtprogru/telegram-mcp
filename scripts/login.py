"""Interactive Telegram login: phone, code, 2FA — straight into the Keychain.

Run it yourself in a terminal (`make login`). The session string is never
printed: it is full access to the account, and anything on screen ends up in
scrollback and in the agent's transcript.
"""

import asyncio
import sys

from telethon import TelegramClient, utils
from telethon.sessions import StringSession

from telegram_mcp.config import SESSION_SERVICE, keychain_get, keychain_set, load_api_credentials


async def login() -> None:
    api_id, api_hash = load_api_credentials()
    if keychain_get(SESSION_SERVICE):
        print(f"A session is already stored in {SESSION_SERVICE}; logging in again replaces it.")

    async with TelegramClient(StringSession(), api_id, api_hash) as client:
        me = await client.get_me()
        session = client.session.save()

    keychain_set(SESSION_SERVICE, session)
    handle = f" (@{me.username})" if getattr(me, "username", None) else ""
    print(f"OK: session for {utils.get_display_name(me)}{handle} saved to Keychain as {SESSION_SERVICE}")


def main() -> None:
    try:
        asyncio.run(login())
    except KeyboardInterrupt:
        print("aborted", file=sys.stderr)
        raise SystemExit(1) from None
    except Exception as exc:  # noqa: BLE001 - the message matters more than the traceback here
        print(f"login failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
