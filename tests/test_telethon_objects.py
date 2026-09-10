"""Render real Telethon objects, not just stubs.

Everything else duck-types its way through getattr, which stays green even if
Telethon renames a field. These build actual Message instances (no network) so a
rename shows up here instead of in production output.
"""

from datetime import UTC, datetime

from telethon.tl import types
from telethon.tl.custom.message import Message

from telegram_mcp.formatting import entity_link_prefix, render_message


def test_an_incoming_private_message_names_its_author():
    message = Message(
        id=512,
        peer_id=types.PeerUser(7654321),
        date=datetime(2026, 9, 10, 11, 2, tzinfo=UTC),
        message="привет",
        from_id=types.PeerUser(7654321),
    )
    message._sender = types.User(id=7654321, first_name="Иван", last_name="Петров", username="ivan")
    assert render_message(message) == "[512] 2026-09-10 11:02 · from Иван Петров (@ivan)\nпривет"


def test_an_own_message_reads_as_you():
    message = Message(
        id=513,
        peer_id=types.PeerUser(7654321),
        date=datetime(2026, 9, 10, 11, 3, tzinfo=UTC),
        message="🤖 сейчас посмотрю",
        out=True,
    )
    assert render_message(message) == "[513] 2026-09-10 11:03 · from you\n🤖 сейчас посмотрю"


def test_a_channel_post_carries_no_author():
    message = Message(
        id=4038,
        peer_id=types.PeerChannel(1234567890),
        date=datetime(2021, 9, 8, 12, 34, tzinfo=UTC),
        message="OOM killer съел процесс",
        views=1234,
    )
    channel = types.Channel(
        id=1234567890,
        title="jtprogru channel",
        photo=None,
        date=None,
        broadcast=True,
        megagroup=False,
        username="jtprogru_channel",
    )
    rendered = render_message(message, link_prefix=entity_link_prefix(channel))
    assert rendered.split("\n")[0] == ("[4038] 2021-09-08 12:34 · 1.2k views · https://t.me/jtprogru_channel/4038")
