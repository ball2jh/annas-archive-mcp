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
Download a file by MD5 hash. Requires `ANNAS_DOWNLOAD_PATH`. Uses Anna's fast_download API when `ANNAS_SECRET_KEY` is configured, then falls back to Libgen when enabled. Saves only inside the configured download directory and skips the network call when the target file already exists.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `hash` | Yes | MD5 hash from search results |
| `title` | Yes | Used for the filename |
| `format` | Yes | File extension (`pdf`, `epub`, etc.) |

Returns the saved file path, whether an existing file was reused, and the source that delivered the file (`fast_download`, `libgen.li`, or `cache`).

### `lookup_doi`
Resolve a DOI to paper metadata and a downloadable hash.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `doi` | Yes | e.g. `10.1038/nature12373` |

Returns title, authors, journal, year, DOI, and MD5 hash.

### `download_by_doi`
Download a paper by DOI. Requires `ANNAS_DOWNLOAD_PATH`. Tries Anna's Archive by resolving DOI to MD5 first, then falls back to Sci-Net when Anna's path is unavailable. `ANNAS_SECRET_KEY` improves the Anna's fast_download path but is not required for Sci-Net fallback.

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `doi` | Yes | — | DOI identifier, e.g. `10.1038/nature12373` |
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

Internal URLs, secret keys, and raw upstream error details are logged for debugging but not returned to the caller.

## Install

```bash
go install github.com/ball2jh/annas-archive-mcp/cmd/annas-archive-mcp@latest
```

Or build from source:

```bash
git clone https://github.com/ball2jh/annas-archive-mcp.git
cd annas-archive-mcp
go build -o annas-archive-mcp ./cmd/annas-archive-mcp/
```

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
| `ANNAS_DOWNLOAD_PATH` | For downloads | — | Absolute path for saved files |
| `ANNAS_BASE_URL` | No | `annas-archive.gl` | Primary mirror hostname |
| `SCINET_BASE_URL` | No | `sci-net.xyz` | Sci-Net fallback hostname for `download_by_doi` |
| `LIBGEN_BASE_URL` | No | `libgen.li` | Libgen fallback hostname for `download` |
| `LIBGEN_ENABLED` | No | `true` | Enables Libgen fallback for `download` |
| `ANNAS_HTTP_TIMEOUT` | No | `30s` | HTTP request timeout |
| `ANNAS_STATS_TIMEOUT` | No | `5s` | Community stats fetch timeout |
| `ANNAS_MAX_RETRIES` | No | `3` | Retry attempts on failure |
| `ANNAS_MAX_CONCURRENCY` | No | `10` | Parallel request cap |
| `ANNAS_TOOL_RATE_LIMIT_PER_MINUTE` | No | `60` | Per-tool local call cap. Set `0` to disable. |
| `ANNAS_LOG_LEVEL` | No | `warn` | Log level (`debug`, `info`, `warn`, `error`) |

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
  httpclient/   HTTP client with retry, backoff, DDoS-Guard detection
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
go test ./...
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
