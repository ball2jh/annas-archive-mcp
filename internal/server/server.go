package server

import (
	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func boolPtr(v bool) *bool {
	return &v
}

// New creates a configured MCP server with all Anna's Archive tools
// registered. version is reported to clients during the MCP handshake.
func New(cfg *config.Config, client *httpclient.Client, logger *zap.Logger, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "annas-archive",
		Title:   "Anna's Archive",
		Version: version,
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        toolSearch,
		Title:       "Search Anna's Archive",
		Description: "Search Anna's Archive for books, articles, and other documents. Returns concise text plus structured results with title, authors, format, size, language, MD5 hash, and community stats.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, searchHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        toolDownload,
		Title:       "Download Anna's Archive File",
		Description: "Download a file by MD5 hash into the configured download directory. Uses Anna's Archive fast_download when a membership key is configured, then falls back to Libgen and, when enabled, Anna's Archive's slow-download servers. Skips the download when a file with the same title and format already exists. Returns the saved file path and which source delivered it.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(true),
		},
	}, downloadHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        toolLookupDOI,
		Title:       "Look Up DOI",
		Description: "Resolve a DOI to paper metadata and an Anna's Archive MD5 hash that can be passed to download or get_details.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, doiHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        toolGetDetails,
		Title:       "Get Anna's Archive Details",
		Description: "Get metadata for an Anna's Archive item by 32-character MD5 hash, including identifiers, file info, cleaned description, and community stats.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, detailsHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        toolDownloadByDOI,
		Title:       "Download Paper by DOI",
		Description: "Download a paper by DOI into the configured download directory. Resolves the DOI on Anna's Archive and downloads it (fast_download, then Libgen, then slow download when enabled), falling back to Sci-Net when that fails. Skips the download when a file with the same title and format already exists. Returns the saved file path and which source delivered it.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(true),
		},
	}, downloadByDOIHandler(cfg, client, logger))

	return srv
}
