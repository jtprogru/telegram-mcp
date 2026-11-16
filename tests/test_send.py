from types import SimpleNamespace

import pytest

from telegram_mcp import tools
from telegram_mcp.config import DEFAULT_SEND_ALLOWLIST, BotDelivery, is_send_allowed, normalize_peer, parse_allowlist


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


CHANNEL = SimpleNamespace(broadcast=True, megagroup=False, username="jtprogru_channel", id=1)
PERSON = SimpleNamespace(first_name="Иван", last_name="Петров", username="ivan", id=765, bot=False, is_self=False)
SELF = SimpleNamespace(first_name="Михаил", last_name=None, username="jtprogru", id=1, bot=False, is_self=True)
ROBOT = SimpleNamespace(first_name="Some", last_name=None, username="somebot", id=9, bot=True, is_self=False)
DELIVERY = BotDelivery(token="123:ABC", connection_id="conn-1")
WITH_PERSON = f"{DEFAULT_SEND_ALLOWLIST},ivan,somebot"


class FakeClient:
    def __init__(self, entity):
        self.entity = entity
        self.sent = []
        self.bot_sent = []
        self.replies = []
        self.bot_replies = []

    async def get_entity(self, target):
        return self.entity

    async def send_message(self, entity, text, parse_mode=None, link_preview=False, reply_to=None):
        self.sent.append((text, parse_mode, link_preview))
        self.replies.append(reply_to)
        return SimpleNamespace(id=99)


@pytest.fixture
def send_to(monkeypatch):
    """Point tg_send at a fake account and hand back the client that recorded the call."""

    def wire(entity, *, delivery=None, allow=DEFAULT_SEND_ALLOWLIST):
        client = FakeClient(entity)

        async def get_client():
            return client

        async def send_via_bot(token, connection_id, chat_id, text, parse_mode, link_preview, reply_to=None):
            client.bot_sent.append((token, connection_id, chat_id, text, parse_mode, link_preview))
            client.bot_replies.append(reply_to)
            return 4242

        monkeypatch.setattr(tools, "get_client", get_client)
        monkeypatch.setattr(tools, "load_allowlist", lambda: parse_allowlist(allow))
        monkeypatch.setattr(tools, "load_bot_delivery", lambda: delivery)
        monkeypatch.setattr(tools.botapi, "send_message", send_via_bot)
        return client

    return wire


async def test_tg_send_rejects_a_peer_outside_the_allowlist(send_to):
    client = send_to(CHANNEL)
    with pytest.raises(PermissionError, match="not allowed"):
        await tools.tg_send("nonexistent_chat", "test")
    assert client.sent == []


async def test_tg_send_posts_to_the_channel_unmarked(send_to):
    client = send_to(CHANNEL)
    result = await tools.tg_send("@jtprogru_channel", "hi")
    assert client.sent == [("hi", "md", False)]
    assert client.replies == [None]
    assert result == "sent [99] to @jtprogru_channel https://t.me/jtprogru_channel/99"


async def test_tg_send_marks_a_message_to_a_person(send_to):
    client = send_to(SELF)
    result = await tools.tg_send("me", "проверка связи")
    assert client.sent == [("🤖 проверка связи", "md", False)]
    assert result == "sent [99] to me"


async def test_tg_send_does_not_double_an_existing_marker(send_to):
    client = send_to(SELF)
    await tools.tg_send("me", "🤖 уже помечено")
    assert client.sent == [("🤖 уже помечено", "md", False)]


async def test_the_marker_can_be_changed_but_not_emptied(send_to, monkeypatch):
    client = send_to(SELF)
    monkeypatch.setenv("TELEGRAM_MCP_AGENT_MARKER", "[agent]")
    await tools.tg_send("me", "hi")
    monkeypatch.setenv("TELEGRAM_MCP_AGENT_MARKER", "   ")
    await tools.tg_send("me", "hi")
    assert client.sent == [("[agent] hi", "md", False), ("🤖 hi", "md", False)]


async def test_a_message_to_a_person_goes_through_the_connected_bot(send_to):
    client = send_to(PERSON, delivery=DELIVERY, allow=WITH_PERSON)
    result = await tools.tg_send("@ivan", "буду через час")
    assert client.bot_sent == [("123:ABC", "conn-1", 765, "🤖 буду через час", "md", False)]
    assert client.bot_replies == [None]
    assert client.sent == []
    assert "via_business_bot_id set" in result


async def test_the_marker_survives_the_bot_path(send_to):
    """Protocol-level attribution is not a reason to drop the one a human can see."""
    client = send_to(PERSON, delivery=DELIVERY, allow=WITH_PERSON)
    await tools.tg_send("@ivan", "текст", parse_mode="html", link_preview=True)
    assert client.bot_sent == [("123:ABC", "conn-1", 765, "🤖 текст", "html", True)]


async def test_without_a_connected_bot_a_person_gets_the_account_itself(send_to):
    client = send_to(PERSON, allow=WITH_PERSON)
    await tools.tg_send("@ivan", "привет")
    assert client.sent == [("🤖 привет", "md", False)]
    assert client.bot_sent == []


async def test_a_channel_post_never_takes_the_bot_path(send_to):
    """A connected bot works in one-to-one chats only; a channel post has to stay on the session."""
    client = send_to(CHANNEL, delivery=DELIVERY)
    await tools.tg_send("@jtprogru_channel", "пост")
    assert client.bot_sent == []
    assert client.sent == [("пост", "md", False)]


async def test_saved_messages_never_take_the_bot_path(send_to):
    client = send_to(SELF, delivery=DELIVERY)
    await tools.tg_send("me", "заметка")
    assert client.bot_sent == []
    assert client.sent == [("🤖 заметка", "md", False)]


async def test_another_bot_is_not_a_business_chat(send_to):
    client = send_to(ROBOT, delivery=DELIVERY, allow=WITH_PERSON)
    await tools.tg_send("@somebot", "/start")
    assert client.bot_sent == []
    assert client.sent == [("🤖 /start", "md", False)]


async def test_tg_send_rejects_an_unknown_parse_mode(send_to):
    client = send_to(SELF)
    with pytest.raises(ValueError, match="parse_mode"):
        await tools.tg_send("me", "hi", parse_mode="rst")
    assert client.sent == []


async def test_tg_send_rejects_empty_text(send_to):
    client = send_to(SELF)
    with pytest.raises(ValueError, match="text is required"):
        await tools.tg_send("me", "   ")
    assert client.sent == []


@pytest.mark.parametrize("entity, peer", [(SELF, "me"), (CHANNEL, "@jtprogru_channel"), (PERSON, "@ivan")])
async def test_tg_send_replies_through_the_account(send_to, entity, peer):
    client = send_to(entity, allow=WITH_PERSON)
    await tools.tg_send(peer, "ответ", parse_mode="html", link_preview=True, reply_to=512)
    expected = "ответ" if entity is CHANNEL else "🤖 ответ"
    assert client.sent == [(expected, "html", True)]
    assert client.replies == [512]
    assert client.bot_sent == []


async def test_tg_send_replies_through_the_connected_bot(send_to):
    client = send_to(PERSON, delivery=DELIVERY, allow=WITH_PERSON)
    await tools.tg_send("@ivan", "ответ", parse_mode="none", reply_to=512)
    assert client.bot_sent == [("123:ABC", "conn-1", 765, "🤖 ответ", None, False)]
    assert client.bot_replies == [512]
    assert client.sent == []


@pytest.mark.parametrize("reply_to", [0, -1])
async def test_tg_send_rejects_invalid_reply_ids_before_connecting(send_to, monkeypatch, reply_to):
    client = send_to(SELF)

    async def explode():
        raise AssertionError("invalid reply_to must be rejected before connecting")

    monkeypatch.setattr(tools, "get_client", explode)
    with pytest.raises(ValueError, match="reply_to must be a positive message ID"):
        await tools.tg_send("me", "ответ", reply_to=reply_to)
    assert client.sent == []
    assert client.bot_sent == []


async def test_reply_does_not_bypass_the_allowlist(send_to):
    client = send_to(PERSON, delivery=DELIVERY)
    with pytest.raises(PermissionError, match="not allowed"):
        await tools.tg_send("@ivan", "ответ", reply_to=512)
    assert client.sent == []
    assert client.bot_sent == []


async def test_a_failed_bot_reply_does_not_fall_back_to_the_account(send_to, monkeypatch):
    client = send_to(PERSON, delivery=DELIVERY, allow=WITH_PERSON)

    async def fail(*args, **kwargs):
        raise tools.BotAPIError("sendMessage: Bad Request: message to be replied not found")

    monkeypatch.setattr(tools.botapi, "send_message", fail)
    with pytest.raises(tools.BotAPIError, match="message to be replied not found"):
        await tools.tg_send("@ivan", "ответ", reply_to=512)
    assert client.sent == []


async def test_the_gate_holds_even_without_credentials(monkeypatch):
    """No session, no api_id — the refusal must still be the allowlist, not a config error."""

    async def explode():
        raise AssertionError("get_client must not be reached for a rejected peer")

    monkeypatch.delenv("TELEGRAM_MCP_SEND_ALLOWLIST", raising=False)
    monkeypatch.setattr(tools, "get_client", explode)
    with pytest.raises(PermissionError, match="not allowed"):
        await tools.tg_send("nonexistent_chat", "test")
