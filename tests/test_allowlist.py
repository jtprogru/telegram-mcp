from types import SimpleNamespace

import pytest

from telegram_mcp import tools
from telegram_mcp.config import DEFAULT_SEND_ALLOWLIST, is_send_allowed, normalize_peer, parse_allowlist


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("jtprogru_channel", "jtprogru_channel"),
        ("@jtprogru_channel", "jtprogru_channel"),
        ("  @JtProgru_Channel  ", "jtprogru_channel"),
        ("https://t.me/jtprogru_channel", "jtprogru_channel"),
        ("t.me/jtprogru_channel/", "jtprogru_channel"),
        (-1001234567890, "-1001234567890"),
    ],
)
def test_normalize_peer(raw, expected):
    assert normalize_peer(raw) == expected


def test_parse_allowlist_falls_back_to_the_default():
    assert parse_allowlist(None) == parse_allowlist(DEFAULT_SEND_ALLOWLIST)
    assert parse_allowlist("   ") == parse_allowlist(DEFAULT_SEND_ALLOWLIST)
    assert "me" in parse_allowlist(None)


def test_parse_allowlist_normalizes_entries():
    assert parse_allowlist("@Foo, https://t.me/Bar ,") == frozenset({"foo", "bar"})


def allowlist(raw: str = DEFAULT_SEND_ALLOWLIST) -> frozenset[str]:
    return parse_allowlist(raw)


def test_is_send_allowed_ignores_decoration():
    allowed = allowlist()
    assert is_send_allowed("@jtprogru_channel", allowed)
    assert is_send_allowed("https://t.me/jtprogru_channel", allowed)
    assert is_send_allowed("me", allowed)
    assert not is_send_allowed("nonexistent_chat", allowed)


class FakeClient:
    def __init__(self):
        self.sent = []

    async def send_message(self, target, text, parse_mode=None, link_preview=False):
        self.sent.append((target, text, parse_mode, link_preview))
        return SimpleNamespace(id=99)

    async def get_entity(self, target):
        return SimpleNamespace(broadcast=True, megagroup=False, username="jtprogru_channel", id=1)


@pytest.fixture
def fake_client(monkeypatch):
    client = FakeClient()

    async def get_client():
        return client

    monkeypatch.setattr(tools, "get_client", get_client)
    monkeypatch.setattr(tools, "load_allowlist", allowlist)
    return client


async def test_tg_send_rejects_a_peer_outside_the_allowlist(fake_client):
    with pytest.raises(PermissionError, match="not allowed"):
        await tools.tg_send("nonexistent_chat", "test")
    assert fake_client.sent == []


async def test_tg_send_delivers_to_an_allowed_peer(fake_client):
    result = await tools.tg_send("@jtprogru_channel", "hi")
    assert fake_client.sent == [("jtprogru_channel", "hi", "md", False)]
    assert result == "sent [99] to @jtprogru_channel https://t.me/jtprogru_channel/99"


async def test_tg_send_rejects_an_unknown_parse_mode(fake_client):
    with pytest.raises(ValueError, match="parse_mode"):
        await tools.tg_send("me", "hi", parse_mode="rst")
    assert fake_client.sent == []


async def test_tg_send_rejects_empty_text(fake_client):
    with pytest.raises(ValueError, match="text is required"):
        await tools.tg_send("me", "   ")
    assert fake_client.sent == []


async def test_the_gate_holds_even_without_credentials(monkeypatch):
    """No session, no api_id — the refusal must still be the allowlist, not a config error."""

    async def explode():
        raise AssertionError("get_client must not be reached for a rejected peer")

    monkeypatch.delenv("TELEGRAM_MCP_SEND_ALLOWLIST", raising=False)
    monkeypatch.setattr(tools, "get_client", explode)
    with pytest.raises(PermissionError, match="not allowed"):
        await tools.tg_send("nonexistent_chat", "test")
