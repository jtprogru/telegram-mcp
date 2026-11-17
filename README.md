# telegram-mcp

A local **Go MCP server** for a personal Telegram account over MTProto, using
[gotd](https://github.com/gotd/td) and the official
[Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk). Seven tools:
read chats/history, search, fetch message links, download media, and send to an
explicit allowlist.

The Go installation has been accepted by the owner. The Python implementation,
secretary, Business delivery and systemd configuration have been removed.
Messages are sent **directly from the user account**, not through a bot.
Existing Keychain secrets and user data are not removed or rewritten as part
of that cleanup.

## Build on macOS

Tested on macOS arm64 with **Go 1.27.4** and Xcode command-line tools. Keychain
uses native Security/CoreFoundation frameworks through cgo (`CGO_ENABLED=1`).
Linux/Windows and headless-server secret storage are not supported targets.

```bash
make build
# Equivalent:
# go build -o bin/telegram-mcp ./cmd/telegram-mcp
./bin/telegram-mcp --help
```

The resulting `bin/telegram-mcp` runs without Python, uv or external `qrencode`.
No CI/CD, published binaries or release automation are included.

## Credentials and login

Already used the Python version? Existing API credentials and Telethon
StringSession can be imported automatically when no Go session exists. Stop any
old Python MCP before sharing its imported auth key with Go. Check locally,
then explicitly verify/import online:

```bash
./bin/telegram-mcp doctor            # local diagnostics, no Telegram requests
./bin/telegram-mcp doctor --network  # verify authorization and save Go session
```

For a new setup, get API ID/hash at [my.telegram.org/apps](https://my.telegram.org/apps),
then run these commands yourself in an interactive terminal:

```bash
./bin/telegram-mcp init
./bin/telegram-mcp auth login --qr
./bin/telegram-mcp doctor --network
```

Scan the QR using Telegram → Settings → Devices → Link Desktop Device. If the
account has a cloud password, enter it at the hidden prompt. Expired QR codes
refresh; an incorrect password can be retried or cancelled with Ctrl+C. No phone
code delivery or external QR tool is required. Never share or record the QR,
login URL, password or session string. `serve` never starts interactive login.

Secrets are read **Keychain → environment**, independently for each legacy item.
The Keychain account is `$USER`. Found but invalid values cause an error, not a
silent fallback:

| Keychain service | Environment fallback |
|---|---|
| `telegram-mcp-api-id` | `TELEGRAM_MCP_API_ID` |
| `telegram-mcp-api-hash` | `TELEGRAM_MCP_API_HASH` |
| `telegram-mcp-session` (legacy Telethon) | `TELEGRAM_MCP_SESSION` |
| `telegram-mcp-go-session` | none |

New Go authorization is saved only after successful verification, in a separate
Keychain item; the old Telethon session is not changed. A corrupt/inaccessible
Go item is not silently replaced by a legacy key. Keychain write failure is an
error: there is **no session-file fallback**. See the
[full guide](docs/go-migration.md) for storage, import and recovery details.

## Connect an MCP client

Use the absolute path to the built binary with argument `serve`:

```json
{
  "mcpServers": {
    "telegram": {
      "command": "/absolute/path/to/telegram-mcp/bin/telegram-mcp",
      "args": ["serve"],
      "env": {
        "TELEGRAM_MCP_SEND_ALLOWLIST": "me"
      }
    }
  }
}
```

Do not put credentials or session strings in the client configuration. Restart
the client after changing the binary/configuration. stdout is exclusively MCP
JSON; initialize/list and forbidden sends do not access Keychain or Telegram.

## Tools

| Tool | Purpose |
|---|---|
| `tg_me` | Current user and delivery mode |
| `tg_dialogs` | Main/archived chats, optional kind/title/username filters |
| `tg_history` | Newest matching slice returned oldest first; ID/time bounds |
| `tg_search` | Search inside a peer or globally, newest first |
| `tg_message` | One full message by public/private/topic `t.me` link |
| `tg_send` | Direct allowlisted message or same-peer reply |
| `tg_download` | Photo/document to a new private local file, max 100 MiB |

Peers are strings: `me`, a username/username link, or a signed numeric Telegram
ID. Numeric peers rehydrate access hashes from at most 500 dialogs; older private
chats may need to be moved into recent dialogs. No call returns more than 200
messages; a read scans at most 500. History `min_id`/`max_id` are exclusive,
`since` inclusive and `until` exclusive. Snippets default to 500 characters.

The [tool contract](docs/go-migration.md#tool-contract-and-bounds) documents exact
schemas/defaults/null, pagination, output bounds, download limitations and timeouts.

## Sending and safety

- **Default allowlist: `me` only.** Add needed usernames or signed IDs explicitly
  in `TELEGRAM_MCP_SEND_ALLOWLIST` (comma-separated), then restart. Resolving an
  alias never widens the allowlist. The check precedes credentials/connection.
- **Mandatory marker:** literal `🤖 ` prefix outside markup on non-broadcast
  messages, including Saved Messages and groups. Broadcast posts are exempt.
  `TELEGRAM_MCP_AGENT_MARKER` can change it; a blank value cannot disable it.
  Do not add a marker of your own in source text.
- **Formatting:** `parse_mode=md` uses gotd **CommonMark**, not Telethon:
  `*italic*`, `**bold**`, `__bold__`, links and code blocks. `html` uses the gotd
  Telegram subset, and `none` sends literal text. Maximum final body is 4096
  UTF-16 code units, including the marker.
- **Replies:** optional positive `reply_to` must identify a visible message in
  the same peer. It does not select a channel's discussion group.
- **Unknown send outcome:** one logical RPC/`random_id`, no blind application
  retries. If a network/server failure reports **UNKNOWN**, inspect history
  before retrying; a new tool call creates a new random ID. This is not exactly-once.
- **Direct delivery only:** no Business attribution (`via_business_bot_id`),
  bot fallback, paid sending or secretary. Old bot credentials are ignored, not
  deleted. There are no tools to delete messages, change a profile or ban users.
- Read content is fenced as `untrusted-telegram-content`, with nested tags and
  attributes escaped. Third-party text is data, not instructions; the fence is
  **not a prompt-injection sandbox**.
- FloodWait returns its wait duration instead of sleeping silently. EOF/shutdown
  cancels active calls. A kernel lock excludes concurrent Go session operations,
  but cannot detect an old Python process.

The MTProto session grants broad account access. Protect Keychain access and
revoke unwanted sessions in Telegram → Settings → Devices. Avoid mass scraping
or sending: Telegram may restrict the account regardless of these local limits.

## Local development

```bash
make help
make fmt        # format Go source/tests
make check      # vet, offline tests, race detector, formatting and golangci-lint
make build
```

`golangci-lint` is a separate development tool, not a runtime dependency. All
project tests are offline: mocked RPC/Keychain, pseudoterminal input, schemas,
real CLI stdio and EOF/SIGTERM. `make clean` removes only the built binary, never
secrets, sessions, downloads or legacy local environments.

Historical migration scope and acceptance evidence are recorded in
[plans/2026-11-17-migrate-to-golang.md](plans/2026-11-17-migrate-to-golang.md) and
[the progress report](plans/2026-11-17-migrate-to-golang-progress.md).
