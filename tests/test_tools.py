from datetime import UTC, datetime

import pytest

from telegram_mcp import tools
from telegram_mcp.formatting import MAX_MESSAGES

EXPECTED_TOOLS = {"tg_me", "tg_dialogs", "tg_history", "tg_search", "tg_message", "tg_send", "tg_download"}


async def test_the_server_exposes_exactly_the_seven_tools():
    registered = {tool.name for tool in await tools.mcp.list_tools()}
    assert registered == EXPECTED_TOOLS


async def test_no_destructive_tool_sneaks_in():
    names = " ".join(tool.name for tool in await tools.mcp.list_tools())
    for forbidden in ("delete", "ban", "kick", "leave", "profile", "edit"):
        assert forbidden not in names


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("me", "me"),
        ("@jtprogru_channel", "jtprogru_channel"),
        ("https://t.me/jtprogru_channel", "jtprogru_channel"),
        ("-1001234567890", -1001234567890),
        ("777000", 777000),
    ],
)
def test_peer_accepts_every_common_spelling(raw, expected):
    assert tools._peer(raw) == expected


def test_peer_requires_a_value():
    with pytest.raises(ValueError, match="peer is required"):
        tools._peer("   ")


def test_clamp_keeps_calls_within_the_context_budget():
    assert tools._clamp(0) == 1
    assert tools._clamp(50) == 50
    assert tools._clamp(10_000) == MAX_MESSAGES


def test_when_parses_iso_and_assumes_utc():
    assert tools._when(None, "since") is None
    assert tools._when("2021-09-08", "since") == datetime(2021, 9, 8, tzinfo=UTC)
    assert tools._when("2021-09-08T12:34+02:00", "since").utcoffset().total_seconds() == 7200


def test_when_rejects_free_form_dates():
    with pytest.raises(ValueError, match="ISO-8601"):
        tools._when("8 сентября 2021", "since")
