// Package server implements the MCP server that exposes Anna's Archive
// functionality as MCP tools over stdio.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/details"
	"github.com/ball2jh/annas-archive-mcp/internal/doi"
	"github.com/ball2jh/annas-archive-mcp/internal/download"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/search"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool names as registered with the MCP server.
const (
	toolSearch        = "search"
	toolDownload      = "download"
	toolLookupDOI     = "lookup_doi"
	toolGetDetails    = "get_details"
	toolDownloadByDOI = "download_by_doi"
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
	Hash   string `json:"hash" jsonschema:"32-character MD5 hash from search results"`
	Title  string `json:"title" jsonschema:"Title used for the saved filename"`
	Format string `json:"format" jsonschema:"File extension (pdf, epub, etc.)"`
}

// DOIInput defines the input schema for the lookup_doi tool.
type DOIInput struct {
	DOI string `json:"doi" jsonschema:"DOI identifier (e.g. 10.1038/nature12373); doi.org URLs are accepted"`
}

// DetailsInput defines the input schema for the get_details tool.
type DetailsInput struct {
	Hash string `json:"hash" jsonschema:"32-character MD5 hash of the item"`
}

// DOIDownloadInput defines the input schema for the download_by_doi tool.
type DOIDownloadInput struct {
	DOI    string `json:"doi" jsonschema:"DOI identifier (e.g. 10.1038/nature12373); doi.org URLs are accepted"`
	Title  string `json:"title,omitempty" jsonschema:"Optional title for the saved filename (defaults to the DOI)"`
	Format string `json:"format,omitempty" jsonschema:"File extension (default: pdf)"`
}

// SearchOutput is the structured result of the search tool.
type SearchOutput struct {
	Count   int                  `json:"count"`
	Results []model.SearchResult `json:"results"`
}

// DownloadOutput is the structured result of the download and
// download_by_doi tools.
type DownloadOutput struct {
	FilePath       string `json:"file_path"`
	Message        string `json:"message"`
	Source         string `json:"source,omitempty"` // "fast_download" | "libgen.li" | "sci-net" | "cache"
	AlreadyExisted bool   `json:"already_existed,omitempty"`
}

// DOIOutput is the structured result of the lookup_doi tool.
type DOIOutput struct {
	Result model.DOIResult `json:"result"`
}

// DetailsOutput is the structured result of the get_details tool.
type DetailsOutput struct {
	Result model.BookDetails `json:"result"`
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

// newHandler wraps a tool implementation with the behaviour every tool
// shares: the local rate limit, error logging, and translation of internal
// errors into stable user-facing ones. fn returns the human-readable text and
// the structured output.
func newHandler[In, Out any](
	tool string,
	cfg *config.Config,
	logger *zap.Logger,
	fn func(ctx context.Context, input In) (string, Out, error),
) mcp.ToolHandlerFor[In, Out] {
	limiter := newRateLimiter(tool, cfg.ToolRateLimitPerMinute)
	return func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		if err := limiter.allow(); err != nil {
			return nil, zero, err
		}

		text, output, err := fn(ctx, input)
		if err != nil {
			// Errors that already carry a user-facing code (bad input, missing
			// config, upstream "not found") are expected; the rest are not.
			level := zapcore.ErrorLevel
			if _, expected := errors.AsType[*usererror.Error](err); expected {
				level = zapcore.InfoLevel
			}
			logger.Log(level, "tool call failed", zap.String("tool", tool), zap.Error(err))
			return nil, zero, toolError(tool, err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, output, nil
	}
}

func searchHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[SearchInput, SearchOutput] {
	opts := search.Options{StatsTimeout: cfg.StatsTimeout, MaxConcurrency: cfg.MaxConcurrency}
	return newHandler(toolSearch, cfg, logger, func(ctx context.Context, input SearchInput) (string, SearchOutput, error) {
		applySearchDefaults(&input)

		results, err := search.Search(ctx, client, logger, input.Query, model.ContentType(input.ContentType), input.Limit, opts)
		if err != nil {
			return "", SearchOutput{}, err
		}
		if results == nil {
			results = []model.SearchResult{} // encode as [] rather than null
		}
		return formatSearchResults(results), SearchOutput{Count: len(results), Results: results}, nil
	})
}

func downloadHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DownloadInput, DownloadOutput] {
	return newHandler(toolDownload, cfg, logger, func(ctx context.Context, input DownloadInput) (string, DownloadOutput, error) {
		result, err := download.Download(ctx, client, logger, cfg, input.Hash, input.Title, input.Format)
		if err != nil {
			return "", DownloadOutput{}, err
		}
		return formatDownload(result), downloadOutput(result), nil
	})
}

func doiHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DOIInput, DOIOutput] {
	return newHandler(toolLookupDOI, cfg, logger, func(ctx context.Context, input DOIInput) (string, DOIOutput, error) {
		result, err := doi.Resolve(ctx, client, logger, input.DOI)
		if err != nil {
			return "", DOIOutput{}, err
		}
		return formatDOIResult(result), DOIOutput{Result: *result}, nil
	})
}

func detailsHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DetailsInput, DetailsOutput] {
	return newHandler(toolGetDetails, cfg, logger, func(ctx context.Context, input DetailsInput) (string, DetailsOutput, error) {
		result, err := details.GetDetails(ctx, client, logger, input.Hash, cfg.StatsTimeout)
		if err != nil {
			return "", DetailsOutput{}, err
		}
		return formatDetails(result), DetailsOutput{Result: *result}, nil
	})
}

func downloadByDOIHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DOIDownloadInput, DownloadOutput] {
	return newHandler(toolDownloadByDOI, cfg, logger, func(ctx context.Context, input DOIDownloadInput) (string, DownloadOutput, error) {
		result, err := download.ByDOI(ctx, client, logger, cfg, input.DOI, input.Title, input.Format)
		if err != nil {
			return "", DownloadOutput{}, err
		}
		return formatDownload(result), downloadOutput(result), nil
	})
}

func downloadOutput(r *model.DownloadResult) DownloadOutput {
	return DownloadOutput{
		FilePath:       r.FilePath,
		Message:        r.Message,
		Source:         r.Source,
		AlreadyExisted: r.AlreadyExisted,
	}
}

// formatDownload renders the two-line text body for a successful download:
// the status message, then where the file is.
func formatDownload(r *model.DownloadResult) string {
	label := "File saved to"
	if r.AlreadyExisted {
		label = "File already at"
	}
	return fmt.Sprintf("%s\n%s: %s", r.Message, label, r.FilePath)
}

// --- Error translation ---

// toolError converts any error into a *usererror.Error that is safe to show
// the MCP caller. Errors that already carry a user-facing code pass through;
// everything else is classified by cause, never by exposing its text.
func toolError(tool string, err error) error {
	var userErr *usererror.Error
	if errors.As(err, &userErr) {
		return userErr
	}

	if errors.Is(err, context.Canceled) {
		return usererror.New(usererror.CodeRequestCancelled, "Request cancelled.")
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return usererror.New(usererror.CodeRequestTimeout, timeoutMessage(tool))
	}

	var statusErr *httpclient.StatusError
	if errors.As(err, &statusErr) {
		if statusErr.DDoSGuard {
			return usererror.New(usererror.CodeUpstreamBlocked, "Anna's Archive is showing its DDoS-Guard browser check and the server could not pass it. Passing it needs Google Chrome or Chromium on a machine with a desktop session (set ANNAS_CHROME_PATH if it is not found, and make sure ANNAS_BROWSER is not off). See the server log for the reason, then try again.")
		}

		// Rate limits and outages. Fallback sources name themselves before
		// their errors get here, so what is left is Anna's Archive's.
		if errors.As(httpclient.SourceError("Anna's Archive", statusErr), &userErr) {
			return userErr
		}

		switch statusErr.StatusCode {
		case http.StatusNotFound:
			return usererror.New(usererror.CodeNotFound, notFoundMessage(tool))
		case http.StatusUnauthorized, http.StatusForbidden:
			return usererror.New(usererror.CodeUpstreamRejected, rejectedMessage(tool))
		case http.StatusUnavailableForLegalReasons:
			return usererror.New(usererror.CodeUpstreamBlocked, "This Anna's Archive mirror is blocked on your network or in your region (HTTP 451). Configure another mirror with ANNAS_BASE_URL.")
		}
	}

	code, message := fallbackError(tool)
	return usererror.New(code, message)
}

func timeoutMessage(tool string) string {
	switch tool {
	case toolSearch:
		return "Anna's Archive did not respond before the timeout. Try a narrower search or try again later."
	case toolDownload, toolDownloadByDOI:
		return "The download stalled or did not respond before the timeout. Try again later."
	default:
		return "Anna's Archive did not respond before the timeout. Try again later."
	}
}

func notFoundMessage(tool string) string {
	switch tool {
	case toolGetDetails:
		return "No Anna's Archive item was found for that hash."
	case toolLookupDOI:
		return "No Anna's Archive record was found for that DOI."
	case toolDownload:
		return "No downloadable file was found for that hash."
	default:
		return "Anna's Archive could not find the requested resource."
	}
}

func rejectedMessage(tool string) string {
	if tool == toolDownload || tool == toolDownloadByDOI {
		return "Anna's Archive rejected the download request. Check ANNAS_SECRET_KEY and try again."
	}
	return "Anna's Archive rejected the request. Try again later or configure another mirror with ANNAS_BASE_URL."
}

func fallbackError(tool string) (string, string) {
	switch tool {
	case toolSearch:
		return usererror.CodeSearchFailed, "Search failed. Anna's Archive may be temporarily unavailable."
	case toolDownload:
		return usererror.CodeDownloadFailed, "Download failed. Try again later."
	case toolDownloadByDOI:
		return usererror.CodeDOIDownloadFailed, "Could not download this DOI from any configured source. Check the DOI, try again later, or download manually."
	case toolLookupDOI:
		return usererror.CodeDOILookupFailed, "DOI lookup failed. Verify the DOI and try again."
	case toolGetDetails:
		return usererror.CodeDetailsFailed, "Could not retrieve details. Verify the hash and try again."
	default:
		return usererror.CodeToolFailed, "Tool call failed. Try again later."
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
	s := strconv.Itoa(n)
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
