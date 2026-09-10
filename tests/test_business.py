import pytest

from telegram_mcp import agent, business
from telegram_mcp.client import ToolPermissionError

FULL_RIGHTS = {name: True for name in business.ALL_RIGHTS}


def test_rights_read_what_telegram_granted():
    rights = business.Rights.from_connection({"rights": {"can_reply": True, "can_edit_bio": True}, "is_enabled": True})
    assert rights.granted == frozenset({"can_reply", "can_edit_bio"})
    assert rights.enabled


def test_rights_ignore_names_telegram_does_not_use():
    rights = business.Rights.from_connection({"rights": {"can_reply": True, "can_do_anything": True}})
    assert rights.granted == frozenset({"can_reply"})


def test_rights_understand_the_older_flag():
    assert business.Rights.from_connection({"can_reply": True}).granted == frozenset({"can_reply"})
    assert business.Rights.from_connection({}).granted == frozenset()


def test_irreversible_rights_are_withheld_by_default():
    rights = business.Rights.from_connection({"rights": FULL_RIGHTS})
    assert rights.granted == frozenset(business.ALL_RIGHTS)

    default = rights.usable(allow_irreversible=False)
    assert business.CAN_TRANSFER_STARS not in default
    assert business.CAN_EDIT_USERNAME not in default
    assert business.CAN_DELETE_ALL_MESSAGES not in default
    assert business.CAN_REPLY in default

    assert rights.usable(allow_irreversible=True) == rights.granted


def test_a_local_opt_in_cannot_invent_a_right_telegram_withheld():
    """The env flag only narrows. Telegram's grant is the ceiling."""
    rights = business.Rights.from_connection({"rights": {"can_reply": True}})
    assert rights.usable(allow_irreversible=True) == frozenset({"can_reply"})


async def test_the_account_refuses_a_right_it_does_not_hold():
    account = business.BusinessAccount("token", "conn", business.Rights(granted=frozenset({"can_reply"})))
    with pytest.raises(ToolPermissionError, match="can_transfer_stars"):
        await account.transfer_stars(100)
    with pytest.raises(ToolPermissionError, match="can_edit_username"):
        await account.set_username("someone_else")


def tool_names(granted, *, allow_irreversible=False):
    rights = business.Rights(granted=frozenset(granted))
    account = business.BusinessAccount("token", "conn", rights)
    usable = rights.usable(allow_irreversible=allow_irreversible)
    return [tool.name for tool in agent.build_tools(account, 42, usable, "🤖")]


def test_a_reply_only_connection_gets_exactly_one_tool():
    assert tool_names({"can_reply"}) == ["reply"]


def test_every_granted_right_becomes_a_tool():
    names = tool_names(business.ALL_RIGHTS, allow_irreversible=True)
    for expected in ("reply", "mark_read", "set_bio", "set_username", "transfer_stars", "transfer_gift", "list_gifts"):
        assert expected in names


def test_withheld_rights_produce_no_tool_at_all():
    """Not a refusal at call time: the model is never shown the capability."""
    names = tool_names(business.ALL_RIGHTS)
    assert "transfer_stars" not in names
    assert "set_username" not in names
    assert "delete_any_messages" not in names
    assert "delete_own_messages" in names


def test_the_reply_tool_cannot_choose_a_recipient():
    """The chat is bound in the closure, so an injected instruction has nowhere to send anything."""
    reply = next(
        tool
        for tool in agent.build_tools(
            business.BusinessAccount("token", "conn", business.Rights(granted=frozenset({"can_reply"}))),
            42,
            frozenset({"can_reply"}),
            "🤖",
        )
        if tool.name == "reply"
    )
    assert set(reply.input_schema["properties"]) == {"text"}
