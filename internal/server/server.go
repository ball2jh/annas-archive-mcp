package server

import (
	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New creates a configured MCP server with all Anna's Archive tools registered.
func New(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "annas-archive",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search",
		Description: "Search Anna's Archive for books, articles, and other documents.",
	}, searchHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "download",
		Description: "Download a file from Anna's Archive by its MD5 hash. Requires ANNAS_SECRET_KEY and ANNAS_DOWNLOAD_PATH to be configured.",
	}, downloadHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "lookup_doi",
		Description: "Resolve a DOI to paper metadata and a downloadable MD5 hash via Anna's Archive.",
	}, doiHandler(cfg, client, logger))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_details",
		Description: "Get full metadata for an item on Anna's Archive by its MD5 hash.",
	}, detailsHandler(cfg, client, logger))

	return srv
}
