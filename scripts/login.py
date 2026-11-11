"""Interactive Telegram login: phone, code, 2FA — straight into the Keychain.

Run it yourself in a terminal (`make login`). The session string is never
printed: it is full access to the account, and anything on screen ends up in
scrollback and in the agent's transcript.
"""

import asyncio
import sys

from telethon import TelegramClient, types, utils
from telethon.sessions import StringSession

from telegram_mcp.config import SESSION_SERVICE, keychain_get, keychain_set, load_api_credentials


class LoginClient(TelegramClient):
    """Show the delivery metadata that Telethon's automatic login discards."""

    async def send_code_request(self, phone: str, **kwargs) -> types.auth.SentCode:
        sent = await super().send_code_request(phone, **kwargs)
        print(f"Telegram selected code delivery: {type(sent.type).__name__}")
        if isinstance(sent.type, types.auth.SentCodeTypeApp):
            print('Check the service chat "Telegram" on an already logged-in device.')
        elif isinstance(sent.type, types.auth.SentCodeTypeEmailCode):
            print("Check the login email configured in Telegram.")
        if sent.next_type is not None:
            print(f"Next delivery offered by Telegram: {type(sent.next_type).__name__}")
            if sent.timeout is not None:
                print(f"Telegram retry timeout: {sent.timeout} seconds")
        print("Waiting at the code prompt does not resend the code. Press Ctrl+C to abort.")
        return sent


async def login() -> None:
    if not sys.stdin.isatty():
        raise RuntimeError(
            "no terminal attached: Telethon asks for the phone, the code and the 2FA password interactively. "
            "Run `make login` in a real terminal window, not through a pipe or an agent shell."
        )
    api_id, api_hash = load_api_credentials()
    if keychain_get(SESSION_SERVICE):
        print(f"A session is already stored in {SESSION_SERVICE}; logging in again replaces it.")

    async with LoginClient(StringSession(), api_id, api_hash) as client:
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
