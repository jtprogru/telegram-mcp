import pytest
from telethon.tl import types

from telegram_mcp import botapi

TOKEN = "8123456:AAH-verysecret-token-value"


def test_parse_text_counts_offsets_in_utf16():
    """The marker is a surrogate pair, so a naive character count would shift every entity."""
    plain, entities = botapi.parse_text("🤖 **привет**", "md")
    assert plain == "🤖 привет"
    assert entities == [{"type": "bold", "offset": 3, "length": 6}]


def test_parse_text_reads_html_too():
    assert botapi.parse_text("🤖 <b>привет</b>", "html") == ("🤖 привет", [{"type": "bold", "offset": 3, "length": 6}])


def test_parse_text_leaves_plain_text_alone():
    assert botapi.parse_text("🤖 **нет**", None) == ("🤖 **нет**", [])


def test_to_bot_entities_carries_the_extra_fields():
    entities = [
        types.MessageEntityTextUrl(offset=0, length=3, url="https://example.com"),
        types.MessageEntityPre(offset=4, length=5, language="python"),
        types.MessageEntityMentionName(offset=10, length=2, user_id=765),
        types.MessageEntityCustomEmoji(offset=13, length=1, document_id=42),
    ]
    assert botapi.to_bot_entities(entities) == [
        {"type": "text_link", "offset": 0, "length": 3, "url": "https://example.com"},
        {"type": "pre", "offset": 4, "length": 5, "language": "python"},
        {"type": "text_mention", "offset": 10, "length": 2, "user": {"id": 765, "is_bot": False, "first_name": ""}},
        {"type": "custom_emoji", "offset": 13, "length": 1, "custom_emoji_id": "42"},
    ]


def test_to_bot_entities_splits_the_two_kinds_of_blockquote():
    plain = types.MessageEntityBlockquote(offset=0, length=4)
    collapsed = types.MessageEntityBlockquote(offset=5, length=4, collapsed=True)
    assert [item["type"] for item in botapi.to_bot_entities([plain, collapsed])] == [
        "blockquote",
        "expandable_blockquote",
    ]


def test_to_bot_entities_drops_what_the_bot_api_has_no_name_for():
    assert botapi.to_bot_entities([types.MessageEntityBankCard(offset=0, length=16)]) == []
    assert botapi.to_bot_entities(None) == []


class FakeResponse:
    def __init__(self, payload, status_code=200):
        self._payload = payload
        self.status_code = status_code

    def json(self):
        if self._payload is None:
            raise ValueError("not json")
        return self._payload


class FakeAsyncClient:
    posted: list = []
    payload: object = None

    def __init__(self, *args, **kwargs):
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def post(self, url, json=None):
        FakeAsyncClient.posted.append((url, json))
        return FakeResponse(FakeAsyncClient.payload)


@pytest.fixture
def bot_api(monkeypatch):
    FakeAsyncClient.posted = []

    def respond(payload):
        FakeAsyncClient.payload = payload
        monkeypatch.setattr(botapi.httpx, "AsyncClient", FakeAsyncClient)
        return FakeAsyncClient

    return respond


async def test_send_message_builds_the_business_payload(bot_api):
    http = bot_api({"ok": True, "result": {"message_id": 4242}})
    message_id = await botapi.send_message(TOKEN, "conn-1", 765, "🤖 **привет**", "md", False)

    assert message_id == 4242
    url, payload = http.posted[0]
    assert url.endswith("/sendMessage")
    assert payload == {
        "business_connection_id": "conn-1",
        "chat_id": 765,
        "text": "🤖 привет",
        "link_preview_options": {"is_disabled": True},
        "entities": [{"type": "bold", "offset": 3, "length": 6}],
    }


async def test_send_message_omits_entities_when_there_are_none(bot_api):
    http = bot_api({"ok": True, "result": {"message_id": 1}})
    await botapi.send_message(TOKEN, "conn-1", 765, "🤖 привет", None, True)
    assert "entities" not in http.posted[0][1]
    assert http.posted[0][1]["link_preview_options"] == {"is_disabled": False}


async def test_an_api_error_says_what_went_wrong_without_the_token(bot_api):
    bot_api({"ok": False, "description": "Bad Request: BUSINESS_CONNECTION_INVALID"})
    with pytest.raises(botapi.BotAPIError) as caught:
        await botapi.send_message(TOKEN, "conn-1", 765, "hi", None, False)
    assert "BUSINESS_CONNECTION_INVALID" in str(caught.value)
    assert TOKEN not in str(caught.value)


async def test_an_unreadable_body_is_still_a_clean_error(bot_api):
    bot_api(None)
    with pytest.raises(botapi.BotAPIError, match="unreadable body"):
        await botapi.whoami(TOKEN)


async def test_whoami_returns_the_handle(bot_api):
    bot_api({"ok": True, "result": {"id": 8123456, "username": "MishkaPersonalBot"}})
    assert await botapi.whoami(TOKEN) == "@MishkaPersonalBot"
