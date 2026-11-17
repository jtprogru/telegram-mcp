# Go MCP — macOS guide

The owner confirmed auth/import/QR first, then the connected Go MCP and its tools.
The personal Go installation is accepted; the Python implementation and its
secretary/Business deployment files have been removed from the repository.
Real secrets, downloads, local environments and the MCP client configuration were
not changed by that cleanup. Live acceptance is based on the owner's reports,
not agent-run Telegram requests. See [the migration record](../plans/2026-11-17-migrate-to-golang-progress.md).

## Build on macOS

The tested host is macOS arm64, Go **1.27.4**, with Xcode command-line tools.
`go.mod` specifies Go 1.27.0 and toolchain go1.27.4. Main pinned dependencies:

- `github.com/gotd/td v0.166.1`
- `github.com/modelcontextprotocol/go-sdk v1.8.0`
- `github.com/keybase/go-keychain v0.0.1`
- `github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e`

```bash
go build -o bin/telegram-mcp ./cmd/telegram-mcp
./bin/telegram-mcp --help
```

The Keychain backend uses cgo and the system Security/CoreFoundation frameworks.
Leave `CGO_ENABLED=1`. No Python, uv, external `qrencode`, or subprocess carrying
secrets is needed to run this binary. Other platforms and cgo-disabled builds
cannot use Keychain and are not supported targets of this migration.

## Secret storage and selection

The Keychain account remains exactly `$USER`, matching Python. Selection is
**Keychain → environment** for each legacy secret, independently:

| Keychain service | Environment fallback |
|---|---|
| `telegram-mcp-api-id` | `TELEGRAM_MCP_API_ID` |
| `telegram-mcp-api-hash` | `TELEGRAM_MCP_API_HASH` |
| `telegram-mcp-session` | `TELEGRAM_MCP_SESSION` |
| `telegram-mcp-go-session` | none |

A found but invalid/empty item is an error, not an invitation to use env.
Missing or unreadable legacy items allow env fallback; local diagnostics indicate
inaccessible Keychain. No secret values are included in diagnostics.

Go session selection is deliberately conservative: only an **absent** Go item
permits automatic Telethon import. A corrupt or inaccessible Go item stops the
operation; it is not silently overwritten using a legacy key. `auth login --qr`
is the explicit replacement path. This means an entirely inaccessible Keychain
cannot be bypassed by a legacy env session alone: there is no Go-session env or
file fallback, and the Go application requires Keychain to save authorization.

Session writes from gotd stay in memory until the account is successfully
verified online. Only then does `SecItemUpdate` (or `SecItemAdd` for a new item)
save the separate Go record. Replacement never deletes the old item first.
Cancellation or errors before that point leave the old Go record untouched.
Failure to save is an error, not a successful login. Neither command writes a
session file, modifies the old `telegram-mcp-session`, or reads bot credentials.

## Setup and recovery: interactive steps are owner-only

**Stop the Python MCP before using the imported auth key in Go.** Go network
operations (tools, login, and `doctor --network`) use a nonblocking kernel session
lock in `~/Library/Caches/telegram-mcp/session.lock`. The file contains no secrets;
a leftover file is harmless, as the kernel releases the lock when a process exits.
Concurrent tool calls within one server are serialized. Another Go process gets a
clear busy error; the lock cannot detect the old Python process.

Local diagnostics read Keychain but make **no Telegram requests**:

```bash
./bin/telegram-mcp doctor
```

Missing API credentials can be entered interactively (both fields are hidden).
Existing valid credentials, including those selected from env, are not changed:

```bash
./bin/telegram-mcp init
```

An existing session can be verified/imported explicitly:

```bash
./bin/telegram-mcp doctor --network --timeout 45s
# A second fresh process checks the saved Go format, without QR/password:
./bin/telegram-mcp doctor --network
```

`--network` calls Telegram to check authorization and saves a verified Go session.
It sends no messages and does not mark history read. If import fails, **do not
assume it was migrated**: record only the sanitized error and discuss QR relogin
as the replacement path. StringSession lacks salt/config, so offline decoding is
not evidence of successful import.

For a new session or an explicitly chosen replacement:

```bash
./bin/telegram-mcp auth login --qr
./bin/telegram-mcp doctor --network
```

Run this yourself in an interactive terminal, with interactive stdin and stderr.
QR display uses `/dev/tty`; redirected/noninteractive login is refused before
credentials or tokens are read. Do not record, screenshot, copy, or send the QR,
login URL, password, or session to the agent. The QR is a bearer credential.
The active terminal screen is cleared on refresh and after the login attempt;
this is **not** a guarantee against terminal history capture or screen recording.

On the phone: Telegram → Settings → Devices → Link Desktop Device. Let one QR
expire before scanning to check refresh. Accounts with a cloud password receive
a hidden prompt after confirmation. A wrong password permits another attempt;
Ctrl+C/Ctrl+D or external cancellation stops input and restores terminal state.
Login does not use a phone-code delivery path. Only after authorization and
Keychain save does the CLI report success. A new login creates a new Telegram
authorization, even if later cancelled before local persistence; no automatic
remote authorization revocation is promised.

Before a replacement, verify the existing Go session in a fresh process. Cancel
a new attempt and verify again; the previous session should still work. Test
wrong-password handling and a correct password locally, without sharing them.
Do not revoke your only working session to test a failure.

## Offline development checks

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go test -race ./...
golangci-lint run ./...
# Dependency regression tests: mocked RPCs, not live Telegram:
go test github.com/gotd/td/telegram/auth/qrlogin
```

All project tests are offline. Native Keychain operations are injected with an
in-memory fake; hidden input/cancellation is checked with pseudoterminals. Tests also cover all seven MCP schemas and actual CLI stdio
`initialize` → `tools/list` → denied `tools/call`, clean stdout, EOF/SIGTERM, and
cancellation of active handlers. No CI or release automation is included.

## Connect the locally built MCP

Configure the MCP client's Telegram entry with the **absolute path** to the
binary and `serve`. If replacing an old Python entry, stop that process first;
do not keep both implementations running with the imported auth key:

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
the MCP client after a configuration change. The default send allowlist is **me
only**; add a username or signed numeric ID explicitly if needed. Username and
numeric-ID aliases are not expanded to bypass the allowlist: use the allowed
reference when sending. `@Name`, `name` and `https://t.me/name/` normalize alike.

`serve` is lazy: initialize/list/invalid or forbidden calls do not read secrets
or connect to Telegram. Each valid operation creates a bounded connection,
verifies the user session, runs the request, and disconnects. This trades a little
connection latency for simple cancellation and no unattended background login.
Missing/revoked credentials return a tool error with setup guidance, never QR
or terminal prompts. stdout is exclusively newline-delimited MCP JSON.

## Tool contract and bounds

| Tool | Inputs and behavior |
|---|---|
| `tg_me` | No arguments; current user and direct-delivery mode |
| `tg_dialogs` | `limit=50` (1–500), optional `kind=user/bot/group/channel`, `query`; main folder then archive, max 500 scanned |
| `tg_history` | Required `peer`; `limit=50` (1–200), `min_id=0`, `max_id=0`, `since`, `until`, `full=false`; newest matching slice returned **oldest first** |
| `tg_search` | Required nonblank `query` (max 1024 bytes), optional `peer`; `limit=30` (1–200); **newest first**, global hits identify chat ID/title |
| `tg_message` | Required `link`; public/private/topic t.me links; last path segment is the message ID |
| `tg_send` | Required `peer`, `text`; `parse_mode=md`, `link_preview=false`, optional positive `reply_to`; allowlist checked before credentials |
| `tg_download` | Required `peer`, positive `message_id`, `dest_dir`; photo/document, **100 MiB** maximum |

Use strings for peers: `me`, usernames/username links, positive user IDs, negative
basic-group IDs, or standard marked channel IDs (`-1000000000000 - channel_id`).
Private message links use the **unmarked** ID in `t.me/c/<channel_id>/<message_id>`.
Numeric resolution rehydrates `access_hash` from at most 500 dialogs on each fresh
connection; a peer outside that window may require a public username or moving
its chat into recent dialogs. No zero access-hash guesses or persistent peer
cache are used. Min entities do not replace a known usable access hash.

History IDs are **exclusive** bounds. `since` is inclusive, `until` exclusive;
ISO dates or RFC3339 timestamps are accepted (zone-less date/minute forms use
UTC, supported Unix timestamp range is 1970–2038). Use the oldest returned ID
as `max_id` for the next older history page. Reads scan at most 500 messages;
search pagination within one call uses Telegram's peer/ID/rate cursor. There is
no exposed global-search continuation token yet.

Omit optional arguments to use defaults. Explicit `null` is supported for the
pointer options `limit`, `parse_mode`, `reply_to` and means omitted. Other
fields follow their published SDK JSON schema; do not send `null` for strings,
integers or booleans. Invalid ranges are errors, not silently clamped. Application
errors return `isError=true` with compact text; malformed JSON/invalid schemas
can be rejected by the MCP SDK before tool execution.

Read tools fence titles, authors and content in `untrusted-telegram-content`,
escape nested fence delimiters and escape attributes. This is a trust signal,
**not a prompt-injection sandbox**. Text defaults to 500-character snippets,
`full` permits up to 16,384 characters per message; the total tool result is
bounded to 2 MiB. Reads never call `readHistory`. Timeouts include waiting for
the server's serialization gate: 60 seconds normally, 2 minutes for downloads.
EOF and shutdown cancel active calls and release session locks.

Downloads use a confined directory handle, random exclusive `.bin` filenames,
0600 files and 0700 newly created directories. Untrusted remote filenames are not
used; existing files are not overwritten. Both announced size and received bytes
are checked, and partial files are removed on errors/cancellation. External URLs,
link previews and arbitrary web fetches are unsupported. CDN redirects are not
requested; an unexpected required redirect returns an explicit limitation.

### Smoke checks after setup or update (owner controlled)

After connecting, check `tg_me`, dialogs, Saved Messages, private-channel history
by numeric ID after a fresh server restart, chat/global search, a message link,
and a small photo/document download. Then explicitly request a send to **me**,
verify emoji/formatting and a reply using an existing message ID. Do not use a
real person's chat or a broadcast channel as an implicit send test. No agent-run
live reads or sends have been performed by the agent during implementation.
The owner has accepted the Go installation. These checks remain useful after an
update; they are not an automatic test run or permission to send to other peers.

## Formatting and delivery

`tg_send.parse_mode` supports `md` (default), `html`, and `none`. Markdown uses
**gotd CommonMark**, not Telethon: `*italic*`, `**bold**`, `__bold__`, inline/fenced
code, links, block quotes, strikethrough and spoiler extensions. HTML uses the
gotd Telegram subset (`b`, `i`, `code`, `pre`, links, etc.), not arbitrary HTML.
`none` sends literal text. Unknown modes fail; no fallback silently sends markup.
`tg://user` mentions require an already resolved user/access hash; prefer ordinary
`@username` text when the user is not in the returned entity catalog.

The marker is a **literal prefix outside parsed markup** on every non-broadcast
message, including Saved Messages and groups. Do not add a marker in your source
text: this implementation always adds its own. Broadcast channel posts are exempt.
The default marker is `🤖`; a blank `TELEGRAM_MCP_AGENT_MARKER` cannot disable it.
The final body must fit 4096 UTF-16 code units, including the marker. Formatting
fixtures test actual parsed text/entities/emoji offsets. This is the chosen
dialect in the accepted Go version; it does not preserve Telethon Markdown semantics.

Delivery is **direct MTProto from the user account**. Old Business/bot credentials
are ignored, not deleted. There is no `via_business_bot_id` guarantee, fallback to
a bot, paid-message flag or send-as identity change. `reply_to` is checked against
an existing visible message in the same peer before sending; it does not select a
channel discussion group or quote a message from another chat.

Each send uses one cryptographic MTProto `random_id` and one logical RPC. Application
retries and RPC timeout resends are disabled; any protocol-required retransmission
uses the same request/random ID. FloodWait is returned with its wait duration, not
slept through. After a network/non-RPC failure or server 5xx once sending started, the result says
**UNKNOWN**: inspect history before retrying, since another MCP call gets a new
random ID. This is not a cross-call idempotency key or exactly-once promise.
