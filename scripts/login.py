"""Interactive Telegram login: phone/code or QR, with 2FA — straight into the Keychain.

Run it yourself in a terminal (`make login`). The session string is never
printed: it is full access to the account, and anything on screen ends up in
scrollback and in the agent's transcript.
"""

import argparse
import asyncio
import getpass
import shutil
import subprocess
import sys

from telethon import TelegramClient, errors, types, utils
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


def display_qr(url: str) -> None:
    """Render locally; keep the login token out of process arguments and files."""
    executable = shutil.which("qrencode")
    if not executable:
        raise RuntimeError("QR login requires qrencode. On macOS, install it with `brew install qrencode`.")
    subprocess.run([executable, "-t", "UTF8", "-m", "4"], input=url, text=True, check=True)


async def sign_in_qr(client: TelegramClient) -> types.User:
    print("On your phone: Telegram > Settings > Devices > Link Desktop Device.")
    print("Scan the QR code and confirm the login. Press Ctrl+C to abort.")
    qr = await client.qr_login()
    while True:
        # Register Telethon's login-update handler before the user can scan.
        waiter = asyncio.create_task(qr.wait())
        try:
            await asyncio.sleep(0)
            display_qr(qr.url)
            return await waiter
        except TimeoutError:
            print("QR code expired; generating a new one.")
            await qr.recreate()
        except errors.SessionPasswordNeededError:
            return await client.sign_in(password=getpass.getpass("Please enter your 2FA password: "))
        finally:
            if not waiter.done():
                waiter.cancel()
            await asyncio.gather(waiter, return_exceptions=True)


async def login(*, qr: bool = False) -> None:
    if not sys.stdin.isatty():
        raise RuntimeError(
            "no terminal attached: Telethon asks for the phone, the code and the 2FA password interactively. "
            "Run `make login` in a real terminal window, not through a pipe or an agent shell."
        )
    if qr:
        if not sys.stdout.isatty():
            raise RuntimeError("QR login must display the code in a terminal, not a redirected log.")
        if not shutil.which("qrencode"):
            raise RuntimeError("QR login requires qrencode. On macOS, install it with `brew install qrencode`.")
    api_id, api_hash = load_api_credentials()
    if keychain_get(SESSION_SERVICE):
        print(f"A session is already stored in {SESSION_SERVICE}; logging in again replaces it.")

    client = LoginClient(StringSession(), api_id, api_hash)
    if qr:
        # __aenter__ calls start(), which would ask for a phone number.
        try:
            await client.connect()
            me = await sign_in_qr(client)
            session = client.session.save()
        finally:
            await client.disconnect()
    else:
        async with client:
            me = await client.get_me()
            session = client.session.save()

    keychain_set(SESSION_SERVICE, session)
    handle = f" (@{me.username})" if getattr(me, "username", None) else ""
    print(f"OK: session for {utils.get_display_name(me)}{handle} saved to Keychain as {SESSION_SERVICE}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--qr", action="store_true", help="Log in by scanning a QR code in the Telegram mobile app")
    args = parser.parse_args()
    try:
        asyncio.run(login(qr=args.qr))
    except KeyboardInterrupt:
        print("aborted", file=sys.stderr)
        raise SystemExit(1) from None
    except Exception as exc:  # noqa: BLE001 - the message matters more than the traceback here
        print(f"login failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
