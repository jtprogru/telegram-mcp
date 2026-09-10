"""Bot API delivery through a Telegram Business connection.

A message sent this way carries `via_business_bot_id`, so the attribution rides
in the protocol instead of resting on a convention in the text. The marker still
goes on: what a client renders for that field is up to the client, and the point
of the marker is that the person reading the message cannot miss it.

Formatting is parsed locally with Telethon and handed over as an explicit entity
list. Telethon's markdown dialect and the Bot API's are not the same language,
and its HTML serializer mangles code blocks, so translating offsets is the only
lossless route.
"""

import logging
from typing import Any

import httpx
from telethon.extensions import html, markdown

from telegram_mcp.client import ToolRuntimeError

API_BASE = "https://api.telegram.org"
_TIMEOUT = httpx.Timeout(20.0, connect=5.0)

# The Bot API carries the token in the URL path, and httpx logs every request URL
# at INFO. Any application that turns on INFO logging would then write the token
# to its log. Silencing that logger here covers every entry point, not just ours.
logging.getLogger("httpx").setLevel(logging.WARNING)
logging.getLogger("httpcore").setLevel(logging.WARNING)

# Telegram's own entity names on the left, the Bot API's on the right. Anything
# missing here is server-generated (bank cards, diffs) and never comes out of a parser.
_ENTITY_TYPES = {
    "MessageEntityBlockquote": "blockquote",
    "MessageEntityBold": "bold",
    "MessageEntityBotCommand": "bot_command",
    "MessageEntityCashtag": "cashtag",
    "MessageEntityCode": "code",
    "MessageEntityCustomEmoji": "custom_emoji",
    "MessageEntityEmail": "email",
    "MessageEntityHashtag": "hashtag",
    "MessageEntityItalic": "italic",
    "MessageEntityMention": "mention",
    "MessageEntityMentionName": "text_mention",
    "MessageEntityPhone": "phone_number",
    "MessageEntityPre": "pre",
    "MessageEntitySpoiler": "spoiler",
    "MessageEntityStrike": "strikethrough",
    "MessageEntityTextUrl": "text_link",
    "MessageEntityUnderline": "underline",
    "MessageEntityUrl": "url",
}


class BotAPIError(ToolRuntimeError):
    """Telegram refused a Bot API call. Never carries the token."""


def to_bot_entities(entities: list[Any] | None) -> list[dict[str, Any]]:
    """Translate Telethon entities into Bot API ones. Both count offsets in UTF-16."""
    converted: list[dict[str, Any]] = []
    for entity in entities or []:
        kind = _ENTITY_TYPES.get(type(entity).__name__)
        if kind is None:
            continue
        if kind == "blockquote" and getattr(entity, "collapsed", False):
            kind = "expandable_blockquote"
        item: dict[str, Any] = {"type": kind, "offset": entity.offset, "length": entity.length}
        if kind == "text_link":
            item["url"] = entity.url
        elif kind == "pre" and getattr(entity, "language", None):
            item["language"] = entity.language
        elif kind == "text_mention":
            item["user"] = {"id": entity.user_id, "is_bot": False, "first_name": ""}
        elif kind == "custom_emoji":
            item["custom_emoji_id"] = str(entity.document_id)
        converted.append(item)
    return converted


def parse_text(text: str, parse_mode: str | None) -> tuple[str, list[dict[str, Any]]]:
    """Split formatted text into plain text plus Bot API entities."""
    if parse_mode == "md":
        plain, entities = markdown.parse(text)
    elif parse_mode == "html":
        plain, entities = html.parse(text)
    else:
        return text, []
    return plain, to_bot_entities(entities)


async def call(
    token: str, method: str, payload: dict[str, Any] | None = None, read_timeout: float | None = None
) -> Any:
    """One Bot API call. Errors quote the method, never the URL, which holds the token."""
    timeout = _TIMEOUT if read_timeout is None else httpx.Timeout(read_timeout + 10, connect=5.0)
    try:
        async with httpx.AsyncClient(timeout=timeout) as http:
            response = await http.post(f"{API_BASE}/bot{token}/{method}", json=payload or {})
    except httpx.HTTPError as exc:
        raise BotAPIError(f"{method}: could not reach the Bot API ({type(exc).__name__})") from exc

    try:
        data = response.json()
    except ValueError as exc:
        raise BotAPIError(f"{method}: HTTP {response.status_code} with an unreadable body") from exc
    if not data.get("ok"):
        raise BotAPIError(f"{method}: {data.get('description') or f'HTTP {response.status_code}'}")
    return data["result"]


async def send_message(
    token: str,
    connection_id: str,
    chat_id: int,
    text: str,
    parse_mode: str | None,
    link_preview: bool,
) -> int:
    """Send on behalf of the connected account and return the new message id."""
    plain, entities = parse_text(text, parse_mode)
    payload: dict[str, Any] = {
        "business_connection_id": connection_id,
        "chat_id": chat_id,
        "text": plain,
        "link_preview_options": {"is_disabled": not link_preview},
    }
    if entities:
        payload["entities"] = entities
    result = await call(token, "sendMessage", payload)
    return int(result["message_id"])


async def whoami(token: str) -> str:
    """The bot's @username, used to show which bot messages are going out through."""
    result = await call(token, "getMe")
    username = result.get("username")
    return f"@{username}" if username else str(result.get("id", "?"))
