# annas-archive-mcp

MCP server for searching, downloading, and looking up content on [Anna's Archive](https://annas-archive.gl). Built in Go, designed for use with Claude Code and other MCP-compatible clients.

## Tools

### `search`
Search across all Anna's Archive content types.

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `query` | Yes | — | Search keywords |
| `content_type` | No | `book_any` | One of: `book_any`, `book_fiction`, `book_nonfiction`, `book_comic`, `journal`, `magazine`, `standards_document` |
| `limit` | No | 5 | Max results (1–20) |

Returns a numbered list with title, authors, format, size, language, MD5 hash, and community stats (downloads, lists, reports).

### `download`
Download a file by MD5 hash. Requires `ANNAS_SECRET_KEY` and `ANNAS_DOWNLOAD_PATH`.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `hash` | Yes | MD5 hash from search results |
| `title` | Yes | Used for the filename |
| `format` | Yes | File extension (`pdf`, `epub`, etc.) |

### `lookup_doi`
Resolve a DOI to paper metadata and a downloadable hash.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `doi` | Yes | e.g. `10.1038/nature12373` |

Returns title, authors, journal, year, DOI, and MD5 hash.

### `get_details`
Get full metadata for an item by hash.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `hash` | Yes | MD5 hash |

Returns title, authors, publisher, year, ISBN, DOI, ISSN, language, format, size, description, and community stats.

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
| `ANNAS_HTTP_TIMEOUT` | No | `30s` | HTTP request timeout |
| `ANNAS_STATS_TIMEOUT` | No | `5s` | Community stats fetch timeout |
| `ANNAS_MAX_RETRIES` | No | `3` | Retry attempts on failure |
| `ANNAS_MAX_CONCURRENCY` | No | `10` | Parallel request cap |
| `ANNAS_LOG_LEVEL` | No | `warn` | Log level (`debug`, `info`, `warn`, `error`) |

## Architecture

```
cmd/annas-archive-mcp/main.go    Entry point: config, logger, HTTP client, MCP server
internal/
  config/       Env var loading, validation, defaults
  httpclient/   HTTP client with retry, backoff, DDoS-Guard detection
  scraper/      HTML parsing with CSS selector chains + fallbacks
  api/          JSON API wrappers (inline_info, fast_download)
  model/        Shared data types
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

## License

MIT
