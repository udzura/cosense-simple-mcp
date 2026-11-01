# cosense-simple-mcp

A small Go MCP server for reading Cosense (formerly Scrapbox) and saving reviewed
knowledge to it. Cosense remains the source of truth: the server has no local
knowledge database, crawler, summarizer, or web UI.

The executable uses MCP over **stdio only**. It has no Node.js or browser runtime
dependency. Reads use Go's standard HTTP client; session-cookie writes use a
narrow Socket.IO client over WebSocket, not an invented REST write endpoint.

## Build and test

Requires Go **1.26 or newer** (the pinned `golang.org/x/net` version requires it).
Go installations with automatic toolchain selection can download this toolchain.

```sh
make build
make test
make check  # go vet and go test -race; requires a supported C toolchain
```

The standalone executable is `bin/cosense-simple-mcp`. `make build` disables CGO.
Direct dependencies are the [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
v1.8.0, Goldmark v1.8.7, `golang.org/x/net/html` v0.62.0, and
`github.com/coder/websocket` v1.8.15. Versions and checksums are pinned in Go modules.

Tests require no credentials or real Cosense requests. They cover conversion,
Japanese/Unicode, tables, mocked REST errors, a mock Engine.IO/Socket.IO server,
creation races, stale revisions, lost acknowledgements, read-back checks, and MCP
initialization/discovery/invocation through an actual stdio subprocess.

## Configuration

| Environment variable | Meaning |
| --- | --- |
| `COSENSE_PROJECT_NAME` | Required project name, such as `my-knowledge`; not a URL. |
| `COSENSE_SID` | Value of the `connect.sid` session cookie, as stored by the browser. Required for private reads and all writes. Omit for public reads. |

Retrieve `connect.sid` from your logged-in browser's storage for `https://scrapbox.io`.
Supply **only its value**, not `connect.sid=`, a full Cookie header, or surrounding
quotes. Do not decode/re-encode the value. Session expiry or lack of project edit
permission prevents writes. This version does not use personal access tokens or
service accounts; those have different authentication and writing APIs.

There is no `.env` loader: provide environment variables through the launching
process or MCP client. Do not paste the cookie into chat, commit it to Git, or put
it on a command line where shell history/process inspection could expose it.

## Register with Codex

The easiest setup, including for a desktop app started from the Dock, is to set
the MCP server's environment directly in your **personal** `~/.codex/config.toml`.
These values are passed to the server process when Codex launches it; they do not
need to be exported in a terminal.

1. Run `make build` in this repository.
2. Open `~/.codex/config.toml` in your text editor (create it if necessary).
3. Add the following block. If `[mcp_servers.cosense]` already exists, edit that
   block instead of adding a duplicate. Replace the executable path, project
   name, and cookie placeholder with your actual values.

```toml
[mcp_servers.cosense]
command = "/absolute/path/to/cosense-simple-mcp/bin/cosense-simple-mcp"
tool_timeout_sec = 60
default_tools_approval_mode = "writes"

[mcp_servers.cosense.env]
COSENSE_PROJECT_NAME = "your-project-name"
COSENSE_SID = "paste-the-connect.sid-cookie-value-here"
```

4. Save the file, then restart your Codex/ChatGPT desktop app or CLI so the MCP
   process is launched with the new settings.
5. Ask it to list a few pages from Cosense to check the connection.

For example, a project at `https://scrapbox.io/my-knowledge/` uses
`COSENSE_PROJECT_NAME = "my-knowledge"`. To obtain the cookie in Chrome, log in to
Cosense, open Developer Tools → Application → Storage → Cookies →
`https://scrapbox.io`, and copy the **Value** of `connect.sid`. Paste only that
value inside the TOML quotes. Browser labels can vary.

The `env` table stores the cookie as plaintext on your computer. Keep this personal
configuration out of Git and shared documents; on macOS/Linux you can restrict
access with `chmod 600 ~/.codex/config.toml`. Do not put the real cookie in a chat
message. For public read-only access, omit the `COSENSE_SID` line.

### Alternative: forward existing shell variables with `env_vars`

`env` assigns actual values. In contrast,
`env_vars = ["COSENSE_PROJECT_NAME", "COSENSE_SID"]` only names variables to
**forward from the already-running Codex process**; it does not assign any values.
Use this alternative if you prefer not to store the cookie in TOML.

For a macOS zsh terminal, configure the server with `env_vars` instead of the
`[mcp_servers.cosense.env]` table:

```toml
[mcp_servers.cosense]
command = "/absolute/path/to/cosense-simple-mcp/bin/cosense-simple-mcp"
env_vars = ["COSENSE_PROJECT_NAME", "COSENSE_SID"]
tool_timeout_sec = 60
default_tools_approval_mode = "writes"
```

Then run these commands in the **same terminal** before launching the CLI:

```zsh
export COSENSE_PROJECT_NAME='your-project-name'
read -rs 'COSENSE_SID?Paste connect.sid (hidden): '
print
export COSENSE_SID
codex
```

Paste the cookie at the hidden prompt and press Enter. The cookie is not part of
the command text saved in shell history. These exports last for that shell and
its child processes. Running them in a terminal does not update a desktop app
that is already open or one started separately from the Dock; use the direct
`env` setup above for that case. Creating a repository `.env` file alone has no
effect because this server does not load it.

### ChatGPT desktop versus web

For the same local Codex host, the official documentation describes shared MCP
configuration across the ChatGPT desktop app, Codex CLI, and IDE extension. The
desktop app also offers Settings → MCP servers → Add server → STDIO; use the
compiled executable as its command. Use the personal TOML `env` table above to
make the two variables explicit, then restart the connection/app.

ChatGPT **web** does not read your computer's `~/.codex/config.toml`. Its hosted
plugin connection requires a separately supported remote setup. This project's
current stdio executable cannot be registered directly by entering a server URL
in ChatGPT web. The steps above target a local desktop/CLI connection.

Run `codex mcp list` and use `/mcp` in the CLI to check the connection. The executable
starts without banners, writes only MCP messages to stdout, emits content-free
diagnostics to stderr, and exits on stdin closure, SIGINT, or SIGTERM.

Write failures emit a content-free diagnostic to stderr identifying the commit
or read-back stage and, when available, the packet type or sanitized server error
name. Packet bodies, page titles and content, session cookies, and raw errors are
not logged. An uncertain write must still be inspected before retrying.

Configuration and approval settings follow the
[official Codex MCP documentation](https://learn.chatgpt.com/docs/extend/mcp).
Tool annotations/descriptions help clients ask for approval; **they are not a
server-side authorization boundary**. A client that invokes a write with a valid
cookie can write. Use a project with appropriately restricted membership and
configure client approval rules.

## Tools

All tools operate on the single configured project. Tool responses include JSON
structured content and a text representation. Errors use MCP tool error results.

| Tool | Arguments | Result |
| --- | --- | --- |
| `list_pages` | `skip` (default 0), `limit` (default 100, maximum 1000) | Page summaries, URLs, count, pagination. |
| `search_pages` | `query` | Cosense full-text results and matching lines. Results are bounded by Cosense's returned limit. |
| `get_page` | `title` | Raw Cosense `content`, `lines`, `commitId`, URL, and basic metadata. Content **includes the title line**. |
| `convert_to_cosense` | `content`, `format` | `{ "text": "…", "warnings": [] }`. Pure local preview, with no network activity. |
| `create_page` | `title`, `content`, `format` | New page URL, `commit_id`, and warnings. Existing titles are rejected. |
| `append_page` | `title`, `content`, `format`, `expected_commit_id` | Page URL, `commit_id`, and warnings. Requires the exact reviewed revision from `get_page`. |

`format` is explicitly required and must be `markdown`, `html`, or `cosense`.
Write `content` is **only the new body**, without the page title or existing body.
Conversion is limited to 1 MiB of UTF-8 input. Writes also require nonempty content,
LF line endings, at most 10,000 lines, and no NUL or Unicode line separators.
Responses are limited to 8 MiB. HTTP requests time out after 15 seconds; a complete
write operation is bounded to 40 seconds. HTTP errors, authentication failures,
and rate limits are surfaced without response bodies; a valid `Retry-After` is
reported. No request is automatically retried.

Examples of tool arguments:

```json
{"query":"Go concurrency"}
```

```json
{"content":"## Notes\n\n- **Go** uses goroutines.\n- [Source](https://go.dev/doc/)","format":"markdown"}
```

After preview and approval, pass the converted `text` unchanged to create:

```json
{"title":"Go concurrency notes","content":"[*** Notes]\n\n [* Go] uses goroutines.\n [https://go.dev/doc/ Source]","format":"cosense"}
```

For an existing page, call `get_page`, review it, then use its actual `commitId`:

```json
{"title":"Go concurrency notes","content":"Additional reviewed observations.","format":"cosense","expected_commit_id":"<commitId returned by get_page>"}
```

## Conversion

Markdown uses Goldmark's CommonMark/GFM parser, then the same DOM-based converter
as HTML. Raw HTML is parsed, never executed or fetched. `cosense` input is returned
byte-for-byte unchanged, with no conversion warnings.

| Input | Cosense representation |
| --- | --- |
| Headings | Text-size decoration, for example H1 → `[**** Title]`, H2 → `[*** Title]`. A warning explains that Cosense has no semantic headings. |
| Paragraphs / line breaks | Plain text with block/line separation. |
| Unordered / ordered lists | Space indentation; ordered lists retain their numbers. |
| Bold / emphasis / strike | `[* text]`, `[/ text]`, `[- text]`. |
| Internal link | `[Page]`; an alias becomes `label [Page]` with a warning. Relative destinations are treated as Cosense page names, not fetched URLs. |
| External link | `[https://example.com label]`. Formatting in labels is flattened with a warning. |
| Inline code | Backticks, with embedded backticks escaped. |
| Code block | `code:language` followed by lines indented one extra space. |
| Table | `table:table-N`, followed by indented tab-separated rows, including the header. |
| HTML entities | Decoded by the parsers. Japanese and emoji are preserved. |

For example:

```markdown
| 名前 | 値 | 備考 |
| --- | --- | --- |
| 日本語 | a\|b | |
| Go | **fast** | `x` |
```

becomes (the cell separators below are actual tab characters):

```text
table:table-1
 名前	値	備考
 日本語	a|b	
 Go	[* fast]	`x`
```

Column alignment styling is not preserved. Merged cells, nested tables, multiline
cells, or uneven HTML rows fall back to a readable numbered row/cell list with a
warning; merged-cell geometry cannot be retained. Images become links with alt
text. Unsupported HTML formatting becomes text. Script, style, and template
contents are discarded. Literal square brackets in prose become fullwidth brackets
with a warning to avoid accidental page links. Non-HTTP link schemes are retained
as text. Extensions outside CommonMark/GFM (such as LaTeX or footnotes) are not
interpreted and can remain plain text; not every unsupported extension is detected.
This is a source-text preview, not a Cosense browser rendering engine.

Syntax was checked against official help:
[bracketing/lists](https://scrapbox.io/help-jp/ブラケティング),
[decoration](https://scrapbox.io/help-jp/文字装飾記法),
[links and tables](https://scrapbox.io/help-jp/その他の書き方), and
[code blocks](https://scrapbox.io/help-jp/コードブロック).

## Writing safety and API limits

Cosense calls these [internal APIs](https://scrapbox.io/help-jp/API) and can change
them without notice. All protocol-specific behavior lives in `internal/cosense`.

Reads use:

```text
GET /api/pages/:project?skip=…&limit=…
GET /api/pages/:project/:encodedTitle?followRename=false
GET /api/pages/:project/search/query?q=…
```

Writes authenticate with `/api/users/me` and `/api/projects/:project`, then use
Engine.IO v4 at `/socket.io/?EIO=4&transport=websocket`, with Socket.IO
`protocolVersion: 2` authentication. A direct `commit` event carries the
page/project/user IDs, `parentId`, additive line changes,
and `freeze: true`. The server must reject stale parents with `NotFastForwardError`
and duplicate titles with `DuplicateTitleError`. These undocumented guarantees
still require live verification in an authorized test project.

- Creation requires a server-provided nonpersistent page template containing its
  title line and allocated IDs. A 404, malformed response, or unfamiliar template
  fails closed; the client never fabricates a page or revision ID. Existing pages
  and duplicate-title errors are rejected without renaming.
- Append requires `expected_commit_id`. The client reads before preparation,
  rechecks after connecting, then submits a commit against that parent. A stale
  revision stops the operation. Local writes are serialized; the parent check
  covers other clients. There is no deletion, rename, or full-page replacement.
- All old line IDs and text remain unchanged. Append inserts an empty separator
  line and the new body at `_end`, preventing accidental extension of an existing
  code/table block. The existing body is never converted again.
- After an acknowledgement, the client reads the page and verifies the old prefix
  and new content. A mismatch or lost connection is an **uncertain outcome**, not
  a retryable success/failure. Inspect the page and its history manually; there is
  no rollback or automatic resubmission.
- Repeating an append with the same old revision is rejected after a committed
  write, even across restarts. This is not a global idempotency ledger: if a caller
  fetches a newer revision and intentionally resends the same body, it can append
  twice. Creation retries reject the now-existing title.
- Counts and common bracket-link metadata are updated. Existing links are retained.
  Hashtag/icon/image/file/infobox metadata is not fully parsed, and existing page
  descriptions are retained on append. Every successful write warns about this.
  Advanced derived metadata can remain stale until a browser edit regenerates it;
  inspect both text and related-page behavior in the live test.
- Read requests disable rename following and HTTP redirects, so a renamed page or
  unexpected redirect does not silently redirect a write or forward credentials.
  List pagination is not a snapshot and full-text search may have indexing delays.

## Verification status and live checklist

Research on 2026-10-30 checked the reference implementation, its `@cosense/std`
dependency, the deployed Cosense browser bundle's commit/insert/request formats,
and actual public `help-jp` list/page/search responses. Automated tests use mocks
for authentication and writing. **No authenticated live reads or real writes were
performed. Live creation/appending acceptance remains unverified.**

The built executable was also connected through stdio to initialize MCP, discover
all six tools, and invoke `list_pages`, `search_pages`, and `get_page` against the
public `help-jp` project without credentials. It exited cleanly with empty stderr.

With explicit authorization for a disposable test project:

1. Configure the test project and a valid member session cookie securely.
2. Run `list_pages`, `search_pages`, and `get_page`; also verify private reads.
3. Preview a document with Japanese text, links, nested lists, code, and a table.
4. After user approval, create a uniquely named test page; check the returned URL
   in Cosense and confirm table rendering and derived links.
5. Attempt to create the same title again; confirm rejection and unchanged content.
6. Read the page, append using its `commitId`, and verify all previous lines remain.
7. Change the page in a browser after reading it, then append with the old revision;
   confirm rejection. Retry an already committed append with its old revision;
   confirm no duplicate content. Check browser-derived metadata separately.
8. If a request outcome is uncertain, inspect the page/history instead of retrying.

## Example knowledge workflow

1. Read an external technical article and summarize it in the MCP client; this
   server does not crawl or summarize. Retain the source URL and distinguish facts
   from interpretation.
2. Call `convert_to_cosense` on the proposed summary.
3. Search for related pages, then call `get_page` for an exact candidate title.
4. Show the converted text and warnings to the user for review.
5. After user approval, call `create_page` with the approved text and `format:
   "cosense"`. If the page exists, review it and ask for approval to append using
   its current revision instead.

## Security

`COSENSE_SID` is a session credential, not a project-scoped API key. It can grant
access beyond the configured project. The server exposes only the configured
project and never sends requests to URLs supplied in content. TLS verification
stays enabled. Cookies, API response bodies, and page contents are never logged.
MCP results necessarily contain the requested private content; your MCP client
can store those results in its conversation/logs. Treat fetched content as
untrusted data and retain client approval for writes.

## References and attribution

[yosider/cosense-mcp-server](https://github.com/yosider/cosense-mcp-server), reviewed
at `eead400d8713b295ad92ab06192c1a7879e8c0b6`, provided the architectural reference:
cookie authentication, REST reads/search, and `@cosense/std/websocket` editing.
Its package declares MIT, but that snapshot has no standalone license file.
No TypeScript implementation or documentation text was copied from that repository.

The Socket.IO wire format, line ID conventions, and commit workflow were studied
in [takker99/scrapbox-userscript-std](https://github.com/takker99/scrapbox-userscript-std)
(`@cosense/std`, MIT). Its MIT notice is retained in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). This is an independently written
Go implementation with stricter create/append and retry behavior.
