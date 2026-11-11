"""Login diagnostics must expose delivery metadata without exposing credentials."""

import importlib.util
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest
from telethon import TelegramClient, types
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
