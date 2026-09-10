# telegram-mcp

An MCP server that gives an agent read-mostly access to a **personal** Telegram account over MTProto ([Telethon](https://docs.telethon.dev/)). Written in Python, dependencies are managed with [uv](https://docs.astral.sh/uv/).

Bot API cannot do this: a bot sees neither the history of a channel it did not post to, nor private chats, nor Saved Messages. MTProto with a user session can — which is exactly why this server is deliberately small. Seven tools, one of which writes, and that one only to peers named in an allowlist. There is no tool to delete a message, ban a user, leave a chat or edit the profile: they cannot be called because they do not exist here.

## Setup

**1. Get `api_id` and `api_hash`** at [my.telegram.org/apps](https://my.telegram.org/apps) (log in by phone number, "API development tools", create an application — any name will do). They are shown once.

**2. Store them in the Keychain.** Secrets never go into `.env` or `.mcp.json`: a session string is full access to the account.

```bash
security add-generic-password -a "$USER" -s telegram-mcp-api-id   -w "<api_id>"
security add-generic-password -a "$USER" -s telegram-mcp-api-hash -w "<api_hash>"
```

**3. Log in.** Run this yourself in a terminal — it asks for the phone number, the code from Telegram and the 2FA password. The session string goes straight into the Keychain and is never printed.

```bash
make install
make login
```

**4. Register the server with Claude Code:**

```bash
claude mcp add -s user telegram -- uv --directory ~/Work/github/jtprogru/telegram-mcp run telegram-mcp
```

Use `-s project` instead if the server should only be available inside one project; that writes to `.mcp.json`, which is gitignored here. The first run may show a system dialog asking for Keychain access — that is `security` being called by the server, "Always Allow" is the answer.

If the Keychain is unavailable (a headless launchd run, for example), the same three values are read from `TELEGRAM_MCP_API_ID`, `TELEGRAM_MCP_API_HASH` and `TELEGRAM_MCP_SESSION`.

## Tools

| Tool | What it does |
|---|---|
| `tg_me()` | Who is logged in. A liveness check for the session |
| `tg_dialogs(limit=50, kind, query)` | Chat list: `[id] title (@username) · channel · unread=N` |
| `tg_history(peer, limit=50, min_id, max_id, since, until, full=False)` | Messages from any peer, `"me"` included |
| `tg_search(query, peer=None, limit=30)` | Search inside one chat, or globally when `peer` is omitted |
| `tg_message(link)` | One message by its `t.me/<channel>/<id>` link, untruncated |
| `tg_send(peer, text, parse_mode="md", link_preview=False)` | Send. Allowlisted peers only |
| `tg_download(peer, message_id, dest_dir)` | Save a message's media to disk |

Output is compact on purpose — a channel with thousands of posts would otherwise fill a context window in a single call. One message renders as a header line plus its text, truncated to 500 characters:

```
[4038] 2021-09-08 12:34 · 1.2k views · https://t.me/jtprogru_channel/4038
Дежурство, три часа ночи, OOM killer съел процесс…
```

`full=True` drops the truncation, and no call ever returns more than 200 messages. To follow a channel incrementally, pass the highest id you already have as `min_id` instead of refetching from the top. All timestamps are UTC, and history comes back oldest first.

## Security

**Writes are fenced off in code.** `tg_send` compares the target against `TELEGRAM_MCP_SEND_ALLOWLIST` (default `jtprogru_channel,me`) and refuses anything else with a `PermissionError`, no matter who asks. The check runs before the client is even connected, so it holds when the session is broken too. Widening the list is a config change plus a restart.

**Everything that came from someone else is fenced as data:**

```
<untrusted-telegram-content peer="somechannel" msg_id="17">
…text…
</untrusted-telegram-content>
```

A post in someone else's channel can contain instructions aimed at the agent, so read tools say in their own descriptions that the payload is data, and any nested fence tag in the content is defanged before wrapping.

**Rate limits are reported, not slept through.** `FloodWaitError` comes back as an error carrying the number of seconds, rather than a silently hung session.

**What code cannot fix:** the session string in the Keychain is a key to the whole account, and any process running as you that gets Keychain access gets Telegram with it. Revoke it in Telegram → Settings → Devices → Terminate session. Automation also formally falls under Telegram's ToS — bulk scraping and mass sending can get an account limited, so the limits in these tools are not decoration.

## Development

```bash
make help     # all targets
make ci       # ruff + pytest, what CI runs
make fmt      # autofix and reformat
```

Tests never touch the network: formatting, link parsing and the allowlist gate are covered with Telethon stubbed out.
