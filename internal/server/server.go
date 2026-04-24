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

// New creates a configured MCP server with all Anna's Archive tools registered.
func New(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "annas-archive",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search",
		Title:       "Search Anna's Archive",
		Description: "Search Anna's Archive for books, articles, and other documents. Returns concise text plus structured results with title, authors, format, size, language, MD5 hash, and community stats.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, searchHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "download",
		Title:       "Download Anna's Archive File",
		Description: "Download a file by MD5 hash into ANNAS_DOWNLOAD_PATH. Requires ANNAS_SECRET_KEY. Skips the network call when the target file already exists, and returns the saved file path.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(true),
		},
	}, downloadHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "lookup_doi",
		Title:       "Look Up DOI",
		Description: "Resolve a DOI to paper metadata and an Anna's Archive MD5 hash that can be passed to download or get_details.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, doiHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_details",
		Title:       "Get Anna's Archive Details",
		Description: "Get metadata for an Anna's Archive item by 32-character MD5 hash, including identifiers, file info, cleaned description, and community stats.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, detailsHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "download_by_doi",
		Title:       "Download Paper by DOI",
		Description: "Download a paper by DOI. Tries Anna's Archive fast_download first, then falls back to Sci-Net. Requires ANNAS_DOWNLOAD_PATH. ANNAS_SECRET_KEY enables the Anna's path; without it only Sci-Net is used. Skips the download when the target file already exists. Returns the saved path and which source succeeded.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(true),
		},
	}, downloadByDOIHandler(cfg, client, logger))

	return srv
}
