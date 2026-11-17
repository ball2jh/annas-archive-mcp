# annas-archive-mcp

MCP server for searching, downloading, and looking up content on Anna's Archive. Built in Go, designed for use with Claude Code and other MCP-compatible clients.

## Tools

### `search`
Search across all Anna's Archive content types. Returns human-readable text and structured results.

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `query` | Yes | — | Search keywords |
| `content_type` | No | `book_any` | One of: `book_any`, `book_fiction`, `book_nonfiction`, `book_comic`, `journal`, `magazine`, `standards_document` |
| `limit` | No | 5 | Max results (1–20) |

Returns title, authors, format, size, language, MD5 hash, and community stats (downloads, lists, reports).

### `download`
Download a file by MD5 hash. Requires `ANNAS_DOWNLOAD_PATH`. Uses Anna's fast_download API when `ANNAS_SECRET_KEY` is configured, then falls back to Libgen when enabled, and finally to Anna's Archive's slow-download servers when `ANNAS_SLOW_DOWNLOAD=on`. Saves only inside the configured download directory.

The saved file is named `<title>.<format>`. If a non-empty file with that name already exists, the call returns it without making any request (and without using quota) — so pick a different title to download a different edition of the same book.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `hash` | Yes | MD5 hash from search results |
| `title` | Yes | Used for the filename (falls back to the hash when empty) |
| `format` | Yes | File extension (`pdf`, `epub`, etc.) |

Returns the saved file path, whether an existing file was reused, and the source that delivered the file (`fast_download`, `libgen.li`, or `cache`).

### `lookup_doi`
Resolve a DOI to paper metadata and a downloadable hash.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `doi` | Yes | e.g. `10.1038/nature12373` |

Returns title, authors, journal, year, DOI, and MD5 hash.

### `download_by_doi`
Download a paper by DOI. Requires `ANNAS_DOWNLOAD_PATH`. Resolves the DOI to an MD5 on Anna's Archive and downloads it the same way as `download` (fast_download, then Libgen, then slow download when enabled); if that fails, falls back to Sci-Net. `ANNAS_SECRET_KEY` is not required for the Libgen and Sci-Net paths.

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `doi` | Yes | — | DOI identifier, e.g. `10.1038/nature12373` (`doi:` and `https://doi.org/` prefixes are accepted) |
| `title` | No | DOI | Used for the filename |
| `format` | No | `pdf` | File extension |

Returns the saved file path, source (`fast_download`, `libgen.li`, `sci-net`, or `cache`), and whether an existing file was reused.

### `get_details`
Get full metadata for an item by hash.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `hash` | Yes | MD5 hash |

Returns title, authors, publisher, year, ISBN, DOI, ISSN, language, format, size, cleaned description, and community stats.

## Example prompts

- "Search Anna's Archive for `python programming`, limit to 3 EPUB/book results."
- "Look up DOI `10.1038/nature12373` and give me the MD5 hash."
- "Get details for hash `f87448722f0072549206b63999ec39e1`."
- "Download hash `51d2b22ca12a8b470b51f543298b34c9` as `Pride and Prejudice.epub`."
- "Download DOI `10.1038/nature12373` as a PDF."

## Error behavior

Tool execution errors are returned as MCP tool errors with stable codes, so agents can self-correct:

- Validation: `[INVALID_QUERY]`, `[INVALID_CONTENT_TYPE]`, `[INVALID_HASH]`, `[INVALID_DOI]`
- Config / filesystem: `[CONFIG]`, `[AUTH_REQUIRED]`, `[AUTH_INVALID]`, `[PATH_REQUIRED]`, `[PATH_UNAVAILABLE]`, `[IO_ERROR]`
- Rate / quota: `[RATE_LIMITED]`, `[LOCAL_RATE_LIMITED]`, `[QUOTA_EXHAUSTED]`
- Transport: `[REQUEST_TIMEOUT]`, `[REQUEST_CANCELLED]`
- Upstream: `[UPSTREAM_BLOCKED]`, `[UPSTREAM_REJECTED]`, `[UPSTREAM_UNAVAILABLE]`, `[UPSTREAM_API_ERROR]`, `[NOT_FOUND]`, `[NOT_FOUND_ON_LIBGEN]`, `[NOT_FOUND_ON_SCINET]`, `[NOT_FAST_DOWNLOADABLE]`
- Unclassified (one per tool): `[SEARCH_FAILED]`, `[DOWNLOAD_FAILED]`, `[DOI_DOWNLOAD_FAILED]`, `[DOI_LOOKUP_FAILED]`, `[DETAILS_FAILED]`

A few codes worth knowing:

- `[AUTH_INVALID]` means Anna's Archive rejected `ANNAS_SECRET_KEY`, or the key's account has no active membership. It is reported even when the Libgen fallback was tried and failed, because the key is what you can fix.
- `[RATE_LIMITED]` and `[UPSTREAM_UNAVAILABLE]` name the source that was responsible (Anna's Archive, Libgen or Sci-Net). When the server asks for a wait longer than 30 seconds, the tool returns straight away with the wait in the message instead of blocking.
- `[UPSTREAM_BLOCKED]` covers both the DDoS-Guard browser check and a mirror that is blocked on your network (HTTP 451); the message says which.

At most four requests to Anna's Archive are in flight at once, so a burst of parallel tool calls is queued rather than rate-limited.

Internal URLs and raw upstream error details are logged to stderr for debugging but never returned to the caller. `ANNAS_SECRET_KEY` is redacted from logs as well.

## Install

Requires Go 1.26 or newer.

```bash
go install github.com/ball2jh/annas-archive-mcp/cmd/annas-archive-mcp@latest
```

Or build from source:

```bash
git clone https://github.com/ball2jh/annas-archive-mcp.git
cd annas-archive-mcp
go build -o annas-archive-mcp ./cmd/annas-archive-mcp/
```

`annas-archive-mcp --version` prints the installed version.

## Configure

### Claude Code

```bash
claude mcp add annas-archive /path/to/annas-archive-mcp \
  -e ANNAS_SECRET_KEY=your_key \
  -e ANNAS_DOWNLOAD_PATH=/your/download/dir
```

### Manual (`mcp.json`)

```json
{
  "mcpServers": {
    "annas-archive": {
      "command": "/path/to/annas-archive-mcp",
      "env": {
        "ANNAS_SECRET_KEY": "your_key",
        "ANNAS_DOWNLOAD_PATH": "/your/download/dir"
      }
    }
  }
}
```

The server starts without `ANNAS_SECRET_KEY` — search and details work fine. Downloads return a clear error if the key is missing.

## Configuration

All via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ANNAS_SECRET_KEY` | For downloads | — | Anna's Archive membership key |
| `ANNAS_DOWNLOAD_PATH` | For downloads | — | Directory for saved files. A leading `~` is expanded; use an absolute path otherwise. |
| `ANNAS_BASE_URL` | No | `annas-archive.gl` | Primary mirror hostname (no path) |
| `SCINET_BASE_URL` | No | `sci-net.xyz` | Sci-Net fallback hostname for `download_by_doi` |
| `LIBGEN_BASE_URL` | No | `libgen.li` | Libgen fallback hostname for `download` |
| `LIBGEN_ENABLED` | No | `true` | Enables Libgen fallback for `download` |
| `ANNAS_HTTP_TIMEOUT` | No | `30s` | How long a request may stall (no response, or no new data) before it is abandoned. Not a limit on total download time. |
| `ANNAS_STATS_TIMEOUT` | No | `5s` | Time allowed for community stats; results are returned without stats when it elapses |
| `ANNAS_MAX_RETRIES` | No | `3` | Retry attempts on network errors, 429 and 5xx. A request that hits `ANNAS_HTTP_TIMEOUT` is retried once at most. |
| `ANNAS_MAX_CONCURRENCY` | No | `10` | Parallel community-stats request cap per tool call (requests to Anna's Archive are further limited to four in flight overall) |
| `ANNAS_TOOL_RATE_LIMIT_PER_MINUTE` | No | `60` | Per-tool local call cap. Set `0` to disable. |
| `ANNAS_LOG_LEVEL` | No | `warn` | Log level (`debug`, `info`, `warn`, `error`) |
| `ANNAS_BROWSER` | No | `auto` | `auto` opens Chrome when needed to pass the browser check; `off` never does |
| `ANNAS_SLOW_DOWNLOAD` | No | `off` | `on` adds Anna's Archive's free slow-download servers as the last download source. Needs the browser; see below. |
| `ANNAS_CHROME_PATH` | No | found on `PATH` | Chrome or Chromium binary to use for the browser check |
| `ANNAS_SESSION_FILE` | No | `<user config dir>/annas-archive-mcp/session.json` | Where the browser session is saved. The Chrome profile lives beside it. |

## Browser check (DDoS-Guard)

Anna's Archive puts its search, detail and DOI pages behind a DDoS-Guard "Checking your browser" page. That check runs JavaScript and cannot be answered by an HTTP client, so the server borrows a session from a real browser:

1. When a request is met with the check, the server opens a Chrome window on a guarded page. The window uses its own profile directory, never your everyday one.
2. Chrome passes the check by itself, usually within seconds and at most a minute — there is nothing to click. The server reads the resulting cookies and Chrome's User-Agent, then closes the window.
3. The session is saved to `session.json` in your user config directory (`~/.config/annas-archive-mcp/` on Linux, mode 600) and sent with every later request, straight from the server. Chrome is only opened again when the site starts challenging again.

So the first search after installing takes noticeably longer and briefly shows a window; after that, calls go direct. Several server processes share the same session file.

Requirements and limits:

- Google Chrome or Chromium must be installed, and the server must run in a desktop session: headless Chrome does not pass the check.
- Linux and macOS only for now. On Windows, and on machines without a display, the affected tools return `[UPSTREAM_BLOCKED]`.
- The session file holds live cookies for the site. Treat it like a credential.
- Set `ANNAS_BROWSER=off` to never open a window. The JSON endpoints (community stats, fast_download) are not behind the check, so `download` by hash keeps working either way.

## Slow downloads

With `ANNAS_SLOW_DOWNLOAD=on`, a file that neither fast_download nor Libgen can deliver is fetched from one of Anna's Archive's free slow-download servers. It is off by default because it is slower and needs more from the machine:

- Every slow download opens the Chrome window. A slow-download page runs the browser check afresh on each visit, so the saved session cannot stand in for the browser. The server reads the partner-server link from the page, closes Chrome, and downloads the file itself.
- Anna's Archive may ask for a manual check (a captcha) in that window. The server does not solve it: the tool returns `[UPSTREAM_BLOCKED]` asking for a person to complete it. Retry and complete the check in the window; the Chrome profile is then let through unattended for a while.
- The partner servers allow one download at a time per IP address without a membership, so slow downloads run one after another. If something else on your network is already downloading from one, the tool returns `[RATE_LIMITED]`.
- A server without a waitlist is preferred. If only waitlisted ones are listed, the page counts down before showing its link; a lookup is given up to three minutes.
- It needs `ANNAS_BROWSER` left on and has the same requirements as the browser check: Chrome or Chromium, a desktop session, Linux or macOS.

Downloads from this source report `"source": "slow_download"`.

## Operational notes

- Use stdio for local clients so the server is only reachable by the MCP client process.
- Keep `ANNAS_SECRET_KEY` in MCP environment configuration, not source files.
- Put downloads in a dedicated directory and add it to `.gitignore`.
- Search, DOI lookup, and details work without download configuration.
- Download tools require `ANNAS_DOWNLOAD_PATH`; `ANNAS_SECRET_KEY` enables Anna's fast_download tier but fallbacks may work without it.
- Downloads consume Anna's Archive quota only when the target file is not already present and the fast_download tier is used.
- Upstream rate limits and `Retry-After` headers are honored by the HTTP client.

## Architecture

```
cmd/annas-archive-mcp/main.go    Entry point: config, logger, HTTP client, MCP server
internal/
  config/       Env var loading, validation, defaults
  httpclient/   HTTP client with retry, backoff, DDoS-Guard detection and recovery
  browser/      Passes the DDoS-Guard browser check with a real Chrome window
  session/      Saved browser session (cookies, User-Agent) and its renewal
  scraper/      HTML parsing with CSS selector chains + fallbacks
  api/          JSON API wrappers (inline_info, fast_download)
  libgen/       Libgen fallback downloads by MD5
  scinet/       Sci-Net fallback downloads by DOI
  model/        Shared data types
  usererror/    User-facing error type with stable codes
  search/       Search orchestration + parallel stats enrichment
  download/     Download orchestration + atomic file writes
  doi/          DOI resolution via /scidb
  details/      Detail page scraping + stats enrichment
  server/       MCP tool registration + thin handlers
testdata/       Saved HTML snapshots for scraper tests
```

## Testing

```bash
go vet ./...
go test -race ./...
```

Tests run against saved HTML snapshots and `httptest` servers — no network access required.

For manual MCP verification, exercise all tools after restarting the client:

1. `search` a low-impact public-domain query, for example "Pride and Prejudice Project Gutenberg EPUB".
2. `get_details` with one returned MD5 hash.
3. `lookup_doi` with `10.1038/nature12373`.
4. `download` a small EPUB into `ANNAS_DOWNLOAD_PATH`, then call it again to confirm the skip-existing path.
5. `download_by_doi` a small paper DOI as PDF, then call it again to confirm the skip-existing path.

## License

MIT
