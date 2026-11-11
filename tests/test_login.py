"""Login diagnostics must expose delivery metadata without exposing credentials."""

import asyncio
import importlib.util
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from telethon import TelegramClient, errors, types
from telethon.sessions import StringSession

spec = importlib.util.spec_from_file_location("login_script", Path(__file__).parents[1] / "scripts" / "login.py")
login_script = importlib.util.module_from_spec(spec)
spec.loader.exec_module(login_script)


class StopAtCodePrompt(Exception):
    pass


@pytest.mark.parametrize(
    "delivery",
    [
        types.auth.SentCodeTypeApp(length=5),
        types.auth.SentCodeTypeEmailCode(email_pattern="private@example.org", length=6),
    ],
)
async def test_login_reports_delivery_before_code_prompt(delivery, capsys):
    sent = types.auth.SentCode(
        type=delivery,
        phone_code_hash="private-code-hash",
        next_type=types.auth.CodeTypeSms(),
        timeout=60,
    )
    prompts = []

    def terminal_input(prompt):
        prompts.append(prompt)
        if len(prompts) == 1:
            return "+19995550123"
        output = capsys.readouterr().out
        assert type(delivery).__name__ in output
        assert "CodeTypeSms" in output
        assert "60" in output
        for secret in ("private-code-hash", "private@example.org", "+19995550123", "private-api-hash"):
            assert secret not in output
        raise StopAtCodePrompt

    with (
        patch.object(login_script.sys.stdin, "isatty", return_value=True),
        patch.object(login_script, "load_api_credentials", return_value=(12345, "private-api-hash")),
        patch.object(login_script, "keychain_get", return_value="old-session"),
        patch.object(login_script, "keychain_set") as save,
        patch.object(TelegramClient, "connect", new=AsyncMock()),
        patch.object(TelegramClient, "get_me", new=AsyncMock(return_value=None)),
        patch.object(TelegramClient, "__call__", new=AsyncMock(return_value=sent)) as rpc,
        patch("builtins.input", side_effect=terminal_input),
        pytest.raises(StopAtCodePrompt),
    ):
        await login_script.login()

    assert type(rpc.await_args.args[0]).__name__ == "SendCodeRequest"
    save.assert_not_called()


async def test_delivery_diagnostics_preserve_send_code_result(capsys):
    sent = types.auth.SentCode(type=types.auth.SentCodeTypeApp(length=5), phone_code_hash="private-code-hash")
    client = login_script.LoginClient(StringSession(), 12345, "private-api-hash")
    with patch.object(TelegramClient, "send_code_request", new=AsyncMock(return_value=sent)) as request:
        assert await client.send_code_request("+19995550123", force_sms=False) is sent
    request.assert_awaited_once_with("+19995550123", force_sms=False)
    assert "SentCodeTypeApp" in capsys.readouterr().out


@pytest.mark.parametrize("outcome", ["success", "expired", "2fa", "error", "render-error"])
async def test_qr_login_saves_only_after_authorization(outcome, capsys):
    me = types.User(id=12345, first_name="Test")
    results = {
        "success": [me],
        "expired": [TimeoutError(), me],
        "2fa": [errors.SessionPasswordNeededError(request=None)],
        "error": [RuntimeError("connection failed")],
        "render-error": [me],
    }
    qr = SimpleNamespace(
        url="tg://login?token=private-token", wait=AsyncMock(side_effect=results[outcome]), recreate=AsyncMock()
    )
    cancelled = asyncio.Event()
    if outcome == "render-error":

        async def wait_for_scan():
            try:
                await asyncio.Event().wait()
            finally:
                cancelled.set()

        qr.wait.side_effect = wait_for_scan
    client = MagicMock()
    client.connect = AsyncMock()
    client.disconnect = AsyncMock()
    client.qr_login = AsyncMock(return_value=qr)
    client.sign_in = AsyncMock(return_value=me)
    client.session.save.return_value = "private-session"
    with (
        patch.object(login_script.sys.stdin, "isatty", return_value=True),
        patch.object(login_script.sys.stdout, "isatty", return_value=True),
        patch.object(login_script, "load_api_credentials", return_value=(12345, "private-api-hash")),
        patch.object(login_script, "keychain_get", return_value="old-session"),
        patch.object(login_script, "keychain_set") as save,
        patch.object(login_script, "LoginClient", return_value=client),
        patch.object(login_script.shutil, "which", return_value="/usr/bin/qrencode"),
        patch.object(
            login_script,
            "display_qr",
            side_effect=RuntimeError("renderer failed") if outcome == "render-error" else None,
        ) as display,
        patch.object(login_script.getpass, "getpass", return_value="private-password") as password,
    ):
        if outcome in ("error", "render-error"):
            with pytest.raises(RuntimeError):
                await login_script.login(qr=True)
            save.assert_not_called()
        else:
            await login_script.login(qr=True)
            save.assert_called_once_with(login_script.SESSION_SERVICE, "private-session")
        client.connect.assert_awaited_once()
        client.disconnect.assert_awaited_once()
        client.start.assert_not_called()
        client.__aenter__.assert_not_called()
        client.send_code_request.assert_not_called()
        if outcome == "expired":
            qr.recreate.assert_awaited_once()
            assert display.call_count == 2
        if outcome == "render-error":
            assert cancelled.is_set()
        if outcome == "2fa":
            password.assert_called_once()
            client.sign_in.assert_awaited_once_with(password="private-password")
        else:
            password.assert_not_called()
    output = capsys.readouterr().out
    for secret in (qr.url, "private-session", "private-password", "private-api-hash"):
        assert secret not in output


def test_qr_renderer_passes_token_on_stdin():
    with (
        patch.object(login_script.shutil, "which", return_value="/usr/bin/qrencode"),
        patch.object(login_script.subprocess, "run") as run,
    ):
        login_script.display_qr("tg://login?token=private-token")
    assert "private-token" not in str(run.call_args.args)
    assert run.call_args.kwargs["input"] == "tg://login?token=private-token"
    assert run.call_args.kwargs["check"] is True


@pytest.mark.parametrize("stdout_tty,renderer", [(False, "/usr/bin/qrencode"), (True, None)])
async def test_qr_preflight_fails_before_accessing_credentials(stdout_tty, renderer):
    with (
        patch.object(login_script.sys.stdin, "isatty", return_value=True),
        patch.object(login_script.sys.stdout, "isatty", return_value=stdout_tty),
        patch.object(login_script.shutil, "which", return_value=renderer),
        patch.object(login_script, "load_api_credentials") as credentials,
        patch.object(login_script, "keychain_set") as save,
        patch.object(login_script, "LoginClient") as client,
        pytest.raises(RuntimeError),
    ):
        await login_script.login(qr=True)
    credentials.assert_not_called()
    client.assert_not_called()
    save.assert_not_called()
