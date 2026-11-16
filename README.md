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

If the login code does not arrive, use QR login from an already logged-in phone:

```bash
brew install qrencode  # if it is not already installed
make login-qr
```

On the phone, open Telegram → Settings → Devices → Link Desktop Device, scan the QR
code in the terminal and confirm the login. Enter the 2FA password in the terminal
if requested. Expired QR codes refresh automatically. The existing Keychain session
is replaced only after a successful login. Keep the QR code private: it authorizes
the new session.

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
| `tg_send(peer, text, parse_mode="md", link_preview=False, reply_to=None)` | Send or reply to a message in the same chat. Allowlisted peers only, marked, and routed through the connected bot when there is one |
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

### Replying to a message

Pass the positive message ID from `tg_history`, `tg_search` or `tg_message` as
`reply_to`, using the same `peer` that contains the original message:

```json
{"peer": "me", "text": "Follow-up to this note", "reply_to": 512}
```

This is a `tg_send` call. Omit `reply_to` (or pass `null`) for a standalone message.
Replies work over both MTProto and the connected business bot, with the same
allowlist and agent marker as ordinary messages. With the business bot, a missing
reply target is an error rather than a fallback to a standalone message.
`reply_to` only addresses a message within the selected chat; it does not open
comments in a channel's linked discussion group.

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

## Running on a VPS

Two processes, split by which secret each one needs.

The **secretary** is a daemon: it long-polls the Bot API, hands each incoming private message to Claude, and answers through the business connection. Its only credential is the bot token. It has no MTProto session, so it cannot read the channel, other chats or Saved Messages — which is the whole point. An instruction hidden inside an incoming message has no private data to reach and no channel to reach it through: the reply tool takes no chat id, so the worst a hostile message achieves is a foolish answer to its own author.

The **MCP server** is not a daemon at all. Leave it on stdio and start it over ssh, so it runs only while you are connected and nothing listens on a port:

```bash
claude mcp add -s user telegram-vps -- ssh -q -T vps '/opt/telegram-mcp/.venv/bin/telegram-mcp'
```

Anything your shell rc files print on the server lands in stdout and corrupts the JSON-RPC stream, so check that `ssh -q -T vps true` prints nothing at all before wiring this up.

### What the secretary is allowed to do

Telegram decides. The rights you grant in Settings → Telegram Business → Chatbots come back on the connection, and the agent is handed one tool per granted right — no grant, no tool, not a refusal at call time. The daemon logs the full set at startup and reloads it whenever the connection changes:

```
INFO connection granted: can_edit_bio, can_read_messages, can_reply, can_view_gifts_and_stars
INFO agent may use: can_edit_bio, can_read_messages, can_reply, can_view_gifts_and_stars
```

Rights that cannot be undone from the outside — transferring Stars or gifts, converting gifts, changing the `@username`, deleting other people's messages — are withheld locally even when Telegram granted them, until `TELEGRAM_SECRETARY_IRREVERSIBLE=1` says otherwise. Telegram's grant is the ceiling and this is the floor; the flag can only narrow, never widen. Note that `transfer_gift` takes a recipient, which makes it the one tool in the set that can send something to a stranger of the model's choosing. That is the reason for the default.

Stories are not covered. `can_manage_stories` grants them, but posting one needs uploaded media, and a half-working implementation of that is worse than none.

### Deploying it

```bash
git clone <repo> /opt/telegram-mcp && cd /opt/telegram-mcp
uv sync --no-dev
install -d -m 700 /etc/telegram-mcp
cp deploy/secretary.env.example /etc/telegram-mcp/secretary.env   # fill it in, chmod 600
cp deploy/telegram-secretary.service /etc/systemd/system/
systemctl enable --now telegram-secretary
```

The Keychain does not exist on Linux and the code knows it: every secret falls back to an environment variable, which is what the unit's `EnvironmentFile` supplies. Keep `TELEGRAM_MCP_SESSION` out of that file — the secretary has no use for it, and leaving it out is most of what makes this split worth doing.

`make secretary` runs the same daemon in the foreground for a look at the logs. It long-polls, so while it runs it owns the bot's update stream and `make bot-connect` will conflict with it.


## Development

```bash
make help     # all targets
make ci       # ruff + pytest, what CI runs
make fmt      # autofix and reformat
```

Tests never touch the network: formatting, link parsing and the allowlist gate are covered with Telethon stubbed out.
