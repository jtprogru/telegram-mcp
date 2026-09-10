import pytest

from telegram_mcp import business, secretary
from telegram_mcp.config import SecretaryConfig
from telegram_mcp.secretary import RateLimit, Secretary, trim_history

CONNECTION = "conn-1"
OWNER_ID = 1
STRANGER = {"id": 765, "first_name": "Иван", "username": "ivan"}


def message(**kwargs):
    base = {
        "business_connection_id": CONNECTION,
        "message_id": 10,
        "chat": {"id": 765},
        "from": STRANGER,
        "text": "привет",
    }
    return {"update_id": 1, "business_message": base | kwargs}


@pytest.fixture
def daemon(monkeypatch, tmp_path):
    config = SecretaryConfig.from_env()
    object.__setattr__(config, "state_dir", tmp_path)
    bot = Secretary(config, "token", CONNECTION, claude=object())
    bot.owner_id = OWNER_ID
    bot.account = business.BusinessAccount("token", CONNECTION, business.Rights(granted=frozenset({"can_reply"})))
    bot.answered = []

    async def answer(chat_id, sender, text, message_id):
        bot.answered.append((chat_id, text, message_id))
        bot.limit.record(chat_id)

    monkeypatch.setattr(bot, "answer", answer)
    return bot


async def test_a_stranger_gets_answered(daemon):
    await daemon.dispatch(message())
    assert daemon.answered == [(765, "привет", 10)]


async def test_the_account_holder_answering_themselves_is_left_alone(daemon):
    """The owner replying in their own chat must not summon the bot on top of them."""
    await daemon.dispatch(message(**{"from": {"id": OWNER_ID}}))
    assert daemon.answered == []


async def test_updates_from_another_connection_are_ignored(daemon):
    await daemon.dispatch(message(business_connection_id="someone-elses"))
    assert daemon.answered == []


async def test_media_without_a_caption_is_not_answered_blind(daemon):
    await daemon.dispatch(message(text=None))
    assert daemon.answered == []


async def test_a_caption_counts_as_text(daemon):
    await daemon.dispatch(message(text=None, caption="посмотри"))
    assert daemon.answered == [(765, "посмотри", 10)]


async def test_the_rate_limit_stops_a_flood(daemon):
    for _ in range(daemon.config.max_replies_per_hour + 5):
        await daemon.dispatch(message())
    assert len(daemon.answered) == daemon.config.max_replies_per_hour


async def test_edited_messages_do_not_trigger_a_reply(daemon):
    await daemon.dispatch({"update_id": 2, "edited_business_message": message()["business_message"]})
    assert daemon.answered == []


async def test_a_connection_change_reloads_the_rights(daemon, monkeypatch):
    async def call(token, method, payload=None, read_timeout=None):
        assert method == "getBusinessConnection"
        return {"user": {"id": OWNER_ID}, "rights": {"can_reply": True, "can_edit_bio": True}, "is_enabled": True}

    monkeypatch.setattr(secretary.botapi, "call", call)
    await daemon.dispatch({"update_id": 3, "business_connection": {"id": CONNECTION}})
    assert daemon.account.rights.granted == frozenset({"can_reply", "can_edit_bio"})


def test_rate_limit_counts_per_chat():
    limit = RateLimit(per_hour=2)
    for _ in range(2):
        assert limit.allows(1)
        limit.record(1)
    assert not limit.allows(1)
    assert limit.allows(2)


def test_trim_history_never_splits_a_tool_call_from_its_result():
    history = [
        {"role": "user", "content": "первое"},
        {"role": "assistant", "content": [{"type": "tool_use", "id": "t1", "name": "reply", "input": {}}]},
        {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}]},
        {"role": "user", "content": "второе"},
        {"role": "assistant", "content": [{"type": "tool_use", "id": "t2", "name": "reply", "input": {}}]},
        {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t2", "content": "ok"}]},
    ]
    trimmed = trim_history(history, limit=3)
    assert trimmed[0] == {"role": "user", "content": "второе"}


def test_trim_history_keeps_short_conversations_whole():
    history = [{"role": "user", "content": "привет"}]
    assert trim_history(history, limit=10) is history
