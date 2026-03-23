// Package server implements the MCP server that exposes Anna's Archive
// functionality as MCP tools over stdio.
package server

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/details"
	"github.com/ball2jh/annas-archive-mcp/internal/doi"
	"github.com/ball2jh/annas-archive-mcp/internal/download"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Input structs ---

// SearchInput defines the input schema for the search tool.
type SearchInput struct {
	Query       string `json:"query" jsonschema:"Search keywords"`
	ContentType string `json:"content_type,omitempty" jsonschema:"Content type filter: book_any (default), book_fiction, book_nonfiction, book_comic, journal, magazine, standards_document"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default 5, max 20)"`
}

// DownloadInput defines the input schema for the download tool.
type DownloadInput struct {
	Hash   string `json:"hash" jsonschema:"MD5 hash from search results"`
	Title  string `json:"title" jsonschema:"Title for the downloaded filename"`
	Format string `json:"format" jsonschema:"File extension (pdf, epub, etc.)"`
}

// DOIInput defines the input schema for the lookup_doi tool.
type DOIInput struct {
	DOI string `json:"doi" jsonschema:"DOI identifier (e.g. 10.1038/nature12373)"`
}

// DetailsInput defines the input schema for the get_details tool.
type DetailsInput struct {
	Hash string `json:"hash" jsonschema:"MD5 hash of the item"`
}

// --- Default application ---

const (
	defaultContentType = "book_any"
	defaultLimit       = 5
	maxLimit           = 20
)

// applySearchDefaults normalises the search input, filling in defaults and
// clamping out-of-range values.
func applySearchDefaults(in *SearchInput) {
	if in.ContentType == "" {
		in.ContentType = defaultContentType
	}
	if in.Limit <= 0 {
		in.Limit = defaultLimit
	}
	if in.Limit > maxLimit {
		in.Limit = maxLimit
	}
}

// --- Handler factories ---
// Each factory returns a closure that captures the shared dependencies.

func searchHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[SearchInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
		applySearchDefaults(&input)

		results, err := search.Search(ctx, client, logger, input.Query, model.ContentType(input.ContentType), input.Limit)
		if err != nil {
			logger.Error("search failed", zap.Error(err))
			return nil, nil, fmt.Errorf("[SEARCH_FAILED] Search failed — the server may be temporarily unavailable.")
		}

		text := formatSearchResults(results)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	}
}

func downloadHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DownloadInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input DownloadInput) (*mcp.CallToolResult, any, error) {
		result, err := download.Download(ctx, client, logger, cfg, input.Hash, input.Title, input.Format)
		if err != nil {
			logger.Error("download failed", zap.Error(err))
			// Surface config-related errors with structured codes since they are user-actionable.
			if strings.Contains(err.Error(), "ANNAS_SECRET_KEY") {
				return nil, nil, fmt.Errorf("[AUTH_REQUIRED] %s", err.Error())
			}
			if strings.Contains(err.Error(), "ANNAS_DOWNLOAD_PATH") {
				return nil, nil, fmt.Errorf("[PATH_REQUIRED] %s", err.Error())
			}
			return nil, nil, fmt.Errorf("[DOWNLOAD_FAILED] Download failed — please try again.")
		}

		text := fmt.Sprintf("%s\nFile saved to: %s", result.Message, result.FilePath)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	}
}

func doiHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DOIInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input DOIInput) (*mcp.CallToolResult, any, error) {
		result, err := doi.Resolve(ctx, client, logger, input.DOI)
		if err != nil {
			logger.Error("DOI lookup failed", zap.Error(err))
			// Surface validation errors with structured codes since they are user-actionable.
			if strings.Contains(err.Error(), "invalid DOI") {
				return nil, nil, fmt.Errorf("[INVALID_DOI] %s", err.Error())
			}
			return nil, nil, fmt.Errorf("[DOI_LOOKUP_FAILED] DOI lookup failed — please verify the DOI and try again.")
		}

		text := formatDOIResult(result)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	}
}

func detailsHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DetailsInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input DetailsInput) (*mcp.CallToolResult, any, error) {
		result, err := details.GetDetails(ctx, client, logger, input.Hash)
		if err != nil {
			logger.Error("get details failed", zap.Error(err))
			// Surface validation errors with structured codes since they are user-actionable.
			if strings.Contains(err.Error(), "invalid hash") {
				return nil, nil, fmt.Errorf("[INVALID_HASH] %s", err.Error())
			}
			return nil, nil, fmt.Errorf("[DETAILS_FAILED] Could not retrieve details — please verify the hash and try again.")
		}

		text := formatDetails(result)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	}
}

// --- Formatting ---

// formatSearchResults renders search results as a numbered human-readable list.
func formatSearchResults(results []model.SearchResult) string {
	if len(results) == 0 {
		return "No results found."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d result(s):\n", len(results))

	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, valueOr(r.Title, "—"))
		fmt.Fprintf(&b, "   Authors: %s\n", valueOr(strings.Join(r.Authors, ", "), "—"))
		fmt.Fprintf(&b, "   Format: %s\n", formatFileInfo(r.Format, r.Size))
		fmt.Fprintf(&b, "   Language: %s\n", valueOr(r.Language, "—"))
		fmt.Fprintf(&b, "   Hash: %s\n", valueOr(r.Hash, "—"))
		fmt.Fprintf(&b, "   Downloads: %s", formatStats(r.Stats))
		if i < len(results)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// formatDetails renders full metadata as key-value pairs.
func formatDetails(d *model.BookDetails) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n", valueOr(d.Title, "—"))
	fmt.Fprintf(&b, "Authors: %s\n", valueOr(strings.Join(d.Authors, ", "), "—"))
	fmt.Fprintf(&b, "Publisher: %s\n", valueOr(d.Publisher, "—"))
	fmt.Fprintf(&b, "Year: %s\n", valueOr(d.Year, "—"))
	fmt.Fprintf(&b, "Format: %s\n", formatFileInfo(d.Format, d.Size))
	fmt.Fprintf(&b, "Language: %s\n", valueOr(d.Language, "—"))
	fmt.Fprintf(&b, "Hash: %s\n", valueOr(d.Hash, "—"))
	fmt.Fprintf(&b, "ISBN: %s\n", valueOr(d.ISBN, "—"))
	fmt.Fprintf(&b, "DOI: %s\n", valueOr(d.DOI, "—"))
	fmt.Fprintf(&b, "ISSN: %s\n", valueOr(d.ISSN, "—"))
	fmt.Fprintf(&b, "Description: %s\n", valueOr(d.Description, "—"))
	fmt.Fprintf(&b, "Downloads: %s", formatStatsNoReports(d.Stats))
	return b.String()
}

// formatDOIResult renders DOI metadata as key-value pairs.
func formatDOIResult(r *model.DOIResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n", valueOr(r.Title, "—"))
	fmt.Fprintf(&b, "Authors: %s\n", valueOr(strings.Join(r.Authors, ", "), "—"))
	fmt.Fprintf(&b, "Journal: %s\n", valueOr(r.Journal, "—"))
	fmt.Fprintf(&b, "Year: %s\n", valueOr(r.Year, "—"))
	fmt.Fprintf(&b, "DOI: %s\n", valueOr(r.DOI, "—"))
	fmt.Fprintf(&b, "Hash: %s", valueOr(r.Hash, "—"))
	return b.String()
}

// formatFileInfo combines format and size into "FORMAT · SIZE" or just one if
// the other is missing.
func formatFileInfo(format, size string) string {
	parts := make([]string, 0, 2)
	if format != "" {
		parts = append(parts, format)
	}
	if size != "" {
		parts = append(parts, size)
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

// formatStats renders community stats as a compact inline string for search results.
func formatStats(s model.CommunityStats) string {
	return fmt.Sprintf("%s · Lists: %s · Reports: %s",
		formatCount(s.Downloads),
		formatCount(s.Lists),
		formatCount(s.Reports),
	)
}

// formatStatsNoReports renders community stats without the reports field (for details).
func formatStatsNoReports(s model.CommunityStats) string {
	return fmt.Sprintf("%s · Lists: %s",
		formatCount(s.Downloads),
		formatCount(s.Lists),
	)
}

// formatCount formats an integer with thousands separators.
func formatCount(n int) string {
	if n < 0 {
		return "-" + formatCount(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert commas from the right.
	var b strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		b.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// valueOr returns s if non-empty, otherwise the fallback.
func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
