# telegram-mcp

An MCP server that gives an agent read-mostly access to a **personal** Telegram account over MTProto ([Telethon](https://docs.telethon.dev/)). Written in Python, dependencies are managed with [uv](https://docs.astral.sh/uv/).

Bot API cannot do this: a bot sees neither the history of a channel it did not post to, nor private chats, nor Saved Messages. MTProto with a user session can — which is exactly why this server is deliberately small. Seven tools, one of which writes, and that one only to peers named in an allowlist — and whatever it sends to a person carries a 🤖 so the recipient can see an agent was involved. There is no tool to delete a message, ban a user, leave a chat or edit the profile: they cannot be called because they do not exist here.

## Setup

**1. Get `api_id` and `api_hash`** at [my.telegram.org/apps](https://my.telegram.org/apps) (log in by phone number, "API development tools", create an application — any name will do). They are shown once.

**2. Store them in the Keychain.** Secrets never go into `.env` or `.mcp.json`: a session string is full access to the account.

```bash
security add-generic-password -a "$USER" -s telegram-mcp-api-id   -w "<api_id>"
security add-generic-password -a "$USER" -s telegram-mcp-api-hash -w "<api_hash>"
```

**3. Log in.** Run this yourself in a real terminal window — it asks for the phone number, the code from Telegram and the 2FA password, so a piped or agent-driven shell will not do. The session string goes straight into the Keychain and is never printed.

```bash
make install
make login
```

**4. Register the server with Claude Code:**

```bash
claude mcp add -s user telegram -- uv --directory ~/Work/github/jtprogru/telegram-mcp run telegram-mcp
```

Use `-s project` instead if the server should only be available inside one project; that writes to `.mcp.json`, which is gitignored here. The first run may show a system dialog asking for Keychain access — that is `security` being called by the server, "Always Allow" is the answer.

**5. Connect a bot (optional).** Telegram Business, which needs Premium, lets you attach a bot to the account in Settings → Telegram Business → Chatbots. Messages this server sends to another person then go out through that bot and carry `via_business_bot_id`: Telegram's own record of who sent them, rather than a convention in the text. Take the token from @BotFather and fetch the connection:

```bash
security add-generic-password -a "$USER" -s telegram-mcp-bot-token -w "<bot token>"
make bot-connect
```

`bot-connect` reads the connection id out of the bot's own update stream, because that is the only place it exists — the account can see *that* a bot is connected (`account.getConnectedBots`) but not under which id. If nothing arrives, toggle the bot off and on in Chatbots while the script is listening, and check that no webhook is set on the bot, since a webhook takes the updates instead.

If the Keychain is unavailable (a headless launchd run, for example), the same three values are read from `TELEGRAM_MCP_API_ID`, `TELEGRAM_MCP_API_HASH` and `TELEGRAM_MCP_SESSION`. The bot path reads `TELEGRAM_MCP_BOT_TOKEN` and `TELEGRAM_MCP_BUSINESS_CONNECTION` the same way. Two more variables tune behaviour rather than access: `TELEGRAM_MCP_SEND_ALLOWLIST` and `TELEGRAM_MCP_AGENT_MARKER`.

## Tools

| Tool | What it does |
|---|---|
| `tg_me()` | Who is logged in. A liveness check for the session |
| `tg_dialogs(limit=50, kind, query)` | Chat list: `[id] title (@username) · channel · unread=N` |
| `tg_history(peer, limit=50, min_id, max_id, since, until, full=False)` | Messages from any peer, `"me"` included |
| `tg_search(query, peer=None, limit=30)` | Search inside one chat, or globally when `peer` is omitted |
| `tg_message(link)` | One message by its `t.me/<channel>/<id>` link, untruncated |
| `tg_send(peer, text, parse_mode="md", link_preview=False)` | Send. Allowlisted peers only, marked, and routed through the connected bot when there is one |
| `tg_download(peer, message_id, dest_dir)` | Save a message's media to disk |

Output is compact on purpose — a channel with thousands of posts would otherwise fill a context window in a single call. One message renders as a header line plus its text, truncated to 500 characters:

```
[4038] 2021-09-08 12:34 · 1.2k views · https://t.me/jtprogru_channel/4038
Дежурство, три часа ночи, OOM killer съел процесс…
```

Where a message has an author, the header says who: `from you` for this account, `from Иван Петров (@ivan)` for anyone else, and `fwd from <origin>` for a forward — which in Saved Messages is the only authorship that means anything. Unsigned channel posts have no author and stay bare, so following a channel costs nothing extra.

```
[512] 2026-09-10 11:02 · from Иван Петров (@ivan)
привет, ты на созвоне?

[513] 2026-09-10 11:03 · from you
🤖 на созвоне до 12:00, напишу как освобожусь
```

`full=True` drops the truncation, and no call ever returns more than 200 messages. To follow a channel incrementally, pass the highest id you already have as `min_id` instead of refetching from the top. All timestamps are UTC, and history comes back oldest first.

## Security

**Writes are fenced off in code.** `tg_send` compares the target against `TELEGRAM_MCP_SEND_ALLOWLIST` (default `jtprogru_channel,me`) and refuses anything else with a `PermissionError`, no matter who asks. The check runs before the client is even connected, so it holds when the session is broken too. Widening the list is a config change plus a restart.

**Messages to people are marked as agent-sent.** Anything `tg_send` delivers to a private chat or a group is prefixed with 🤖 before it leaves, so the person reading it can tell how it was written without having to ask. The badge goes in front rather than at the end, where a notification preview would hide it, and it is applied in code — the agent cannot leave it out or talk its way past it. Posts to a broadcast channel are exempt: those are content authored by the channel, not a message from you to someone. Set `TELEGRAM_MCP_AGENT_MARKER` to use something other than 🤖; an empty value falls back to the default instead of switching the marking off.

**Two delivery paths, one marker.** A message to another person goes through the connected bot when one is configured, and out of the account directly otherwise. Channel posts and Saved Messages always go directly: a business connection only covers one-to-one chats. The marker is applied before the path is chosen, so it rides along either way. Attribution the protocol records is no reason to drop the attribution a person can actually see, and a token that is half configured raises rather than quietly falling back to an unattributed send.

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
