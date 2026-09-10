"""Compact rendering of Telegram objects plus the untrusted-content fence.

The channel holds thousands of posts, so raw Telethon objects would eat a whole
context window in one call. Everything here collapses a message to one header
line and a truncated body.
"""

import re
from datetime import datetime
from typing import Any
from urllib.parse import urlsplit

SNIPPET_LIMIT = 500
MAX_MESSAGES = 200
FENCE_TAG = "untrusted-telegram-content"

_TME_HOSTS = {"t.me", "www.t.me", "telegram.me", "www.telegram.me", "telegram.dog"}
_FENCE_RE = re.compile(rf"</?{FENCE_TAG}", re.IGNORECASE)


def format_views(views: int | None) -> str | None:
    """1234 -> '1.2k', 3_400_000 -> '3.4M'."""
    if not views:
        return None
    if views >= 1_000_000:
        return f"{views / 1_000_000:.1f}M".replace(".0M", "M")
    if views >= 1_000:
        return f"{views / 1_000:.1f}k".replace(".0k", "k")
    return str(views)


def truncate(text: str, limit: int = SNIPPET_LIMIT) -> str:
    text = text.strip()
    if len(text) <= limit:
        return text
    return text[:limit].rstrip() + "…"


def format_datetime(value: datetime | None) -> str:
    return value.strftime("%Y-%m-%d %H:%M") if value else "?"


def media_label(media: Any) -> str | None:
    """'MessageMediaPhoto' -> '[photo]'. Link previews are noise and are dropped."""
    if media is None:
        return None
    name = type(media).__name__
    kind = name.removeprefix("MessageMedia").lower() or name.lower()
    if kind == "webpage":
        return None
    return f"[{kind}]"


def entity_link_prefix(entity: Any) -> str | None:
    """Base for t.me message links, or None when the peer has no linkable form.

    Only channels and supergroups have per-message links; users (Saved Messages
    included) and basic groups do not, and inventing one would produce a link
    that resolves to something else entirely.
    """
    if entity is None or not (hasattr(entity, "broadcast") or hasattr(entity, "megagroup")):
        return None
    username = getattr(entity, "username", None)
    return f"https://t.me/{username}" if username else f"https://t.me/c/{entity.id}"


def is_broadcast_channel(entity: Any) -> bool:
    """True for a channel post, false for anything addressed to people directly."""
    return bool(getattr(entity, "broadcast", False))


def is_private_person(entity: Any) -> bool:
    """A one-to-one chat with somebody else — not a channel, not a bot, not yourself."""
    if not hasattr(entity, "first_name"):
        return False
    return not (getattr(entity, "is_self", False) or getattr(entity, "bot", False))


def mark_as_agent(text: str, marker: str) -> str:
    """Stamp an outgoing message so the person reading it knows an agent wrote it.

    The marker goes in front rather than at the end: a trailing signature is
    invisible in a notification preview, and the point is that nobody has to
    read to the bottom to find out who they are talking to.
    """
    body = text.lstrip()
    if body.startswith(marker):
        return body
    return f"{marker} {body}"


def display_name(entity: Any) -> str:
    """Human name of a user, group or channel; empty when there is nothing to show."""
    if entity is None:
        return ""
    title = getattr(entity, "title", None)
    if title:
        return str(title)
    names = (getattr(entity, "first_name", None), getattr(entity, "last_name", None))
    return " ".join(name for name in names if name).strip()


def sender_label(message: Any) -> str | None:
    """Who wrote it, in a form the agent can act on.

    Without this a group log or Saved Messages is a wall of anonymous text and
    the account's own messages are indistinguishable from everyone else's. Own
    messages read as 'you'; unsigned channel posts have no author at all and
    stay bare.
    """
    if getattr(message, "out", False):
        return "you"
    author = getattr(message, "post_author", None)
    if author:
        return str(author)

    sender = getattr(message, "sender", None)
    name = display_name(sender)
    username = getattr(sender, "username", None)
    if name and username:
        return f"{name} (@{username})"
    if username:
        return f"@{username}"
    sender_id = getattr(sender, "id", None) or getattr(message, "sender_id", None)
    if name:
        return f"{name} ({sender_id})" if sender_id else name
    return str(sender_id) if sender_id else None


def forward_label(message: Any) -> str | None:
    """Where a forward came from — the only real authorship signal in Saved Messages."""
    forward = getattr(message, "forward", None)
    if forward is None:
        return None
    origin = getattr(forward, "sender", None) or getattr(forward, "chat", None)
    name = display_name(origin) or getattr(forward, "from_name", None)
    return f"fwd from {name}" if name else "fwd"


def render_message(message: Any, *, link_prefix: str | None = None, full: bool = False, chat: str | None = None) -> str:
    """One message as a header line plus its (by default truncated) text."""
    parts = [f"[{message.id}] {format_datetime(getattr(message, 'date', None))}"]
    if chat:
        parts.append(chat)
    sender = sender_label(message)
    if sender:
        parts.append(f"from {sender}")
    forwarded = forward_label(message)
    if forwarded:
        parts.append(forwarded)
    views = format_views(getattr(message, "views", None))
    if views:
        parts.append(f"{views} views")
    media = media_label(getattr(message, "media", None))
    if media:
        parts.append(media)
    reply_to = getattr(message, "reply_to_msg_id", None)
    if reply_to:
        parts.append(f"reply→{reply_to}")
    if link_prefix:
        parts.append(f"{link_prefix}/{message.id}")

    header = " · ".join(parts)
    text = (getattr(message, "message", None) or getattr(message, "text", None) or "").strip()
    if not text:
        action = getattr(message, "action", None)
        if action is not None:
            text = f"[service: {type(action).__name__.removeprefix('MessageAction')}]"
    if not text:
        return header
    body = text if full else truncate(text)
    return f"{header}\n{body}"


def render_messages(messages: list[Any], *, link_prefix: str | None = None, full: bool = False) -> str:
    if not messages:
        return "(no messages)"
    return "\n\n".join(render_message(message, link_prefix=link_prefix, full=full) for message in messages)


def render_dialog(dialog_id: int, title: str, username: str | None, kind: str, unread: int) -> str:
    handle = f" (@{username})" if username else ""
    return f"[{dialog_id}] {title or '(no title)'}{handle} · {kind} · unread={unread}"


def _attr(value: object) -> str:
    return str(value).replace('"', "'").replace("\n", " ").replace("<", "").replace(">", "")


def fence(body: str, *, peer: object, msg_id: object = None) -> str:
    """Wrap third-party content so it reads as data.

    Anything that did not come from the user can carry instructions aimed at the
    agent, so the payload is fenced and any nested fence tag is defanged.
    """
    attrs = f'peer="{_attr(peer)}"'
    if msg_id is not None:
        attrs += f' msg_id="{_attr(msg_id)}"'
    safe = _FENCE_RE.sub(lambda m: m.group(0).replace("<", "&lt;"), body)
    return f"<{FENCE_TAG} {attrs}>\n{safe}\n</{FENCE_TAG}>"


def parse_message_link(link: str) -> tuple[str | int, int]:
    """'https://t.me/jtprogru_channel/4038' -> ('jtprogru_channel', 4038).

    Private channels come as t.me/c/<internal_id>/<msg_id> and resolve to the
    -100-prefixed peer id. Comment and topic links keep their last segment as id.
    """
    raw = link.strip()
    if not raw:
        raise ValueError("empty message link")
    if "://" not in raw:
        raw = "https://" + raw.lstrip("/")
    parts = urlsplit(raw)
    if parts.netloc.lower() not in _TME_HOSTS:
        raise ValueError(f"not a t.me message link: {link!r}")
    segments = [segment for segment in parts.path.split("/") if segment]
    if segments and segments[0] == "s":  # t.me/s/<channel>/<id> is the web preview
        segments = segments[1:]
    if len(segments) < 2:
        raise ValueError(f"link carries no message id: {link!r}")
    try:
        message_id = int(segments[-1])
    except ValueError:
        raise ValueError(f"link carries no numeric message id: {link!r}") from None
    if segments[0] == "c":
        if len(segments) < 3:
            raise ValueError(f"private channel link is incomplete: {link!r}")
        return int(f"-100{segments[1]}"), message_id
    return segments[0], message_id
