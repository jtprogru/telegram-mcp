"""The Telegram Business surface a connected bot can act on.

Telegram decides what this bot may do: the rights are granted in Settings →
Telegram Business → Chatbots and come back on the connection. Nothing here
widens that. Every method states the right it needs and refuses without it, and
the agent is only ever handed the subset that was actually granted.

Right names and method signatures follow core.telegram.org/bots/api. Note that
the docs name a few rights inconsistently in prose (`can_change_name` for what
the object calls `can_edit_name`); the field names below are the object's.
"""

from dataclasses import dataclass
from typing import Any

from telegram_mcp import botapi
from telegram_mcp.client import ToolPermissionError

CAN_REPLY = "can_reply"
CAN_READ_MESSAGES = "can_read_messages"
CAN_DELETE_SENT_MESSAGES = "can_delete_sent_messages"
CAN_DELETE_ALL_MESSAGES = "can_delete_all_messages"
CAN_EDIT_NAME = "can_edit_name"
CAN_EDIT_BIO = "can_edit_bio"
CAN_EDIT_PROFILE_PHOTO = "can_edit_profile_photo"
CAN_EDIT_USERNAME = "can_edit_username"
CAN_CHANGE_GIFT_SETTINGS = "can_change_gift_settings"
CAN_VIEW_GIFTS_AND_STARS = "can_view_gifts_and_stars"
CAN_CONVERT_GIFTS_TO_STARS = "can_convert_gifts_to_stars"
CAN_TRANSFER_AND_UPGRADE_GIFTS = "can_transfer_and_upgrade_gifts"
CAN_TRANSFER_STARS = "can_transfer_stars"
CAN_MANAGE_STORIES = "can_manage_stories"

ALL_RIGHTS = (
    CAN_REPLY,
    CAN_READ_MESSAGES,
    CAN_DELETE_SENT_MESSAGES,
    CAN_DELETE_ALL_MESSAGES,
    CAN_EDIT_NAME,
    CAN_EDIT_BIO,
    CAN_EDIT_PROFILE_PHOTO,
    CAN_EDIT_USERNAME,
    CAN_CHANGE_GIFT_SETTINGS,
    CAN_VIEW_GIFTS_AND_STARS,
    CAN_CONVERT_GIFTS_TO_STARS,
    CAN_TRANSFER_AND_UPGRADE_GIFTS,
    CAN_TRANSFER_STARS,
    CAN_MANAGE_STORIES,
)

# Rights whose use cannot be undone from the outside: money leaves, a gift changes
# hands, an identity or someone else's message is gone. Telegram granting them is
# the outer bound; TELEGRAM_SECRETARY_IRREVERSIBLE is the inner one, and it is off
# by default so a stranger's message cannot reach them on day one.
IRREVERSIBLE_RIGHTS = frozenset(
    {
        CAN_TRANSFER_STARS,
        CAN_TRANSFER_AND_UPGRADE_GIFTS,
        CAN_CONVERT_GIFTS_TO_STARS,
        CAN_EDIT_USERNAME,
        CAN_DELETE_ALL_MESSAGES,
    }
)


@dataclass(frozen=True)
class Rights:
    """What Telegram actually granted this connection."""

    granted: frozenset[str]
    enabled: bool = True

    @classmethod
    def from_connection(cls, connection: dict[str, Any]) -> "Rights":
        raw = connection.get("rights")
        if isinstance(raw, dict):
            granted = {name for name in ALL_RIGHTS if raw.get(name)}
        else:  # older connections carried a bare can_reply flag
            granted = {CAN_REPLY} if connection.get("can_reply") else set()
        return cls(granted=frozenset(granted), enabled=bool(connection.get("is_enabled", True)))

    def has(self, right: str) -> bool:
        return right in self.granted

    def usable(self, *, allow_irreversible: bool) -> frozenset[str]:
        """The rights the agent is allowed to act on, after the local narrowing."""
        if allow_irreversible:
            return self.granted
        return frozenset(self.granted - IRREVERSIBLE_RIGHTS)

    def describe(self) -> str:
        return ", ".join(sorted(self.granted)) or "none"


class BusinessAccount:
    """Bot API calls made on behalf of the connected account.

    Every call checks its right first. The agent never sees a tool for a right it
    does not hold, so this is the second lock on the same door.
    """

    def __init__(self, token: str, connection_id: str, rights: Rights) -> None:
        self._token = token
        self._connection_id = connection_id
        self.rights = rights

    def _require(self, right: str) -> None:
        if not self.rights.has(right):
            raise ToolPermissionError(
                f"the business connection does not grant {right}. "
                "Grant it in Telegram → Settings → Telegram Business → Chatbots to enable this."
            )

    async def _call(self, method: str, payload: dict[str, Any]) -> Any:
        return await botapi.call(self._token, method, {"business_connection_id": self._connection_id, **payload})

    async def send_message(self, chat_id: int, text: str, parse_mode: str | None, link_preview: bool) -> int:
        self._require(CAN_REPLY)
        return await botapi.send_message(self._token, self._connection_id, chat_id, text, parse_mode, link_preview)

    async def read_message(self, chat_id: int, message_id: int) -> None:
        self._require(CAN_READ_MESSAGES)
        await self._call("readBusinessMessage", {"chat_id": chat_id, "message_id": message_id})

    async def delete_messages(self, message_ids: list[int], *, own_only: bool) -> None:
        self._require(CAN_DELETE_SENT_MESSAGES if own_only else CAN_DELETE_ALL_MESSAGES)
        await self._call("deleteBusinessMessages", {"message_ids": message_ids})

    async def set_name(self, first_name: str, last_name: str | None = None) -> None:
        self._require(CAN_EDIT_NAME)
        payload: dict[str, Any] = {"first_name": first_name}
        if last_name is not None:
            payload["last_name"] = last_name
        await self._call("setBusinessAccountName", payload)

    async def set_bio(self, bio: str) -> None:
        self._require(CAN_EDIT_BIO)
        await self._call("setBusinessAccountBio", {"bio": bio})

    async def set_username(self, username: str) -> None:
        self._require(CAN_EDIT_USERNAME)
        await self._call("setBusinessAccountUsername", {"username": username})

    async def remove_profile_photo(self, *, is_public: bool = False) -> None:
        self._require(CAN_EDIT_PROFILE_PHOTO)
        await self._call("removeBusinessAccountProfilePhoto", {"is_public": is_public})

    async def star_balance(self) -> dict[str, Any]:
        self._require(CAN_VIEW_GIFTS_AND_STARS)
        return await self._call("getBusinessAccountStarBalance", {})

    async def gifts(self, **filters: Any) -> dict[str, Any]:
        self._require(CAN_VIEW_GIFTS_AND_STARS)
        return await self._call("getBusinessAccountGifts", {k: v for k, v in filters.items() if v is not None})

    async def set_gift_settings(self, show_gift_button: bool, accepted_gift_types: dict[str, bool]) -> None:
        self._require(CAN_CHANGE_GIFT_SETTINGS)
        await self._call(
            "setBusinessAccountGiftSettings",
            {"show_gift_button": show_gift_button, "accepted_gift_types": accepted_gift_types},
        )

    async def convert_gift_to_stars(self, owned_gift_id: str) -> None:
        self._require(CAN_CONVERT_GIFTS_TO_STARS)
        await self._call("convertGiftToStars", {"owned_gift_id": owned_gift_id})

    async def upgrade_gift(self, owned_gift_id: str, keep_original_details: bool, star_count: int | None) -> None:
        self._require(CAN_TRANSFER_AND_UPGRADE_GIFTS)
        if star_count:
            self._require(CAN_TRANSFER_STARS)  # a paid upgrade spends the account's stars
        payload: dict[str, Any] = {"owned_gift_id": owned_gift_id, "keep_original_details": keep_original_details}
        if star_count:
            payload["star_count"] = star_count
        await self._call("upgradeGift", payload)

    async def transfer_gift(self, owned_gift_id: str, new_owner_chat_id: int, star_count: int | None) -> None:
        self._require(CAN_TRANSFER_AND_UPGRADE_GIFTS)
        if star_count:
            self._require(CAN_TRANSFER_STARS)
        payload: dict[str, Any] = {"owned_gift_id": owned_gift_id, "new_owner_chat_id": new_owner_chat_id}
        if star_count:
            payload["star_count"] = star_count
        await self._call("transferGift", payload)

    async def transfer_stars(self, star_count: int) -> None:
        self._require(CAN_TRANSFER_STARS)
        await self._call("transferBusinessAccountStars", {"star_count": star_count})


async def load_rights(token: str, connection_id: str) -> Rights:
    """Ask Telegram what this connection is allowed to do, rather than assuming."""
    connection = await botapi.call(token, "getBusinessConnection", {"business_connection_id": connection_id})
    return Rights.from_connection(connection)
