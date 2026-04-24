// Package server implements the MCP server that exposes Anna's Archive
// functionality as MCP tools over stdio.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/details"
	"github.com/ball2jh/annas-archive-mcp/internal/doi"
	"github.com/ball2jh/annas-archive-mcp/internal/download"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scinet"
	"github.com/ball2jh/annas-archive-mcp/internal/search"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
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

// DOIDownloadInput defines the input schema for the download_by_doi tool.
type DOIDownloadInput struct {
	DOI    string `json:"doi" jsonschema:"DOI identifier (e.g. 10.1038/nature12373)"`
	Title  string `json:"title,omitempty" jsonschema:"Optional title for the saved filename; inferred from metadata when omitted"`
	Format string `json:"format,omitempty" jsonschema:"File extension (default: pdf)"`
}

type SearchOutput struct {
	Count   int                  `json:"count"`
	Results []model.SearchResult `json:"results"`
}

type DownloadOutput struct {
	FilePath       string `json:"file_path"`
	Message        string `json:"message"`
	Source         string `json:"source,omitempty"` // "fast_download" | "libgen.li" | "cache"
	AlreadyExisted bool   `json:"already_existed,omitempty"`
}

type DOIOutput struct {
	Result model.DOIResult `json:"result"`
}

type DetailsOutput struct {
	Result model.BookDetails `json:"result"`
}

// DOIDownloadOutput reports which source succeeded and where the file landed.
type DOIDownloadOutput struct {
	FilePath       string `json:"file_path"`
	Message        string `json:"message"`
	Source         string `json:"source"` // "fast_download" | "sci-net"
	AlreadyExisted bool   `json:"already_existed,omitempty"`
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

func searchHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[SearchInput, SearchOutput] {
	limiter := newRateLimiter("search", cfg.ToolRateLimitPerMinute)
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		if err := limiter.allow(); err != nil {
			return nil, SearchOutput{}, err
		}

		applySearchDefaults(&input)

		results, err := search.Search(ctx, client, logger, input.Query, model.ContentType(input.ContentType), input.Limit)
		if err != nil {
			logger.Error("search failed", zap.Error(err))
			return nil, SearchOutput{}, toolError("search", err)
		}

		output := SearchOutput{Count: len(results), Results: results}
		text := formatSearchResults(results)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, output, nil
	}
}

func downloadHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DownloadInput, DownloadOutput] {
	limiter := newRateLimiter("download", cfg.ToolRateLimitPerMinute)
	return func(ctx context.Context, req *mcp.CallToolRequest, input DownloadInput) (*mcp.CallToolResult, DownloadOutput, error) {
		if err := limiter.allow(); err != nil {
			return nil, DownloadOutput{}, err
		}

		result, err := download.Download(ctx, client, logger, cfg, input.Hash, input.Title, input.Format)
		if err != nil {
			logger.Error("download failed", zap.Error(err))
			return nil, DownloadOutput{}, toolError("download", err)
		}

		label := "File saved to"
		if result.AlreadyExisted {
			label = "File already at"
		}
		line2 := fmt.Sprintf("%s: %s", label, result.FilePath)
		if result.Source != "" && result.Source != "cache" {
			line2 = fmt.Sprintf("%s (via %s)", line2, result.Source)
		}
		text := fmt.Sprintf("%s\n%s", result.Message, line2)
		output := DownloadOutput{
			FilePath:       result.FilePath,
			Message:        result.Message,
			Source:         result.Source,
			AlreadyExisted: result.AlreadyExisted,
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, output, nil
	}
}

func doiHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DOIInput, DOIOutput] {
	limiter := newRateLimiter("lookup_doi", cfg.ToolRateLimitPerMinute)
	return func(ctx context.Context, req *mcp.CallToolRequest, input DOIInput) (*mcp.CallToolResult, DOIOutput, error) {
		if err := limiter.allow(); err != nil {
			return nil, DOIOutput{}, err
		}

		result, err := doi.Resolve(ctx, client, logger, input.DOI)
		if err != nil {
			logger.Error("DOI lookup failed", zap.Error(err))
			return nil, DOIOutput{}, toolError("doi", err)
		}

		output := DOIOutput{Result: *result}
		text := formatDOIResult(result)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, output, nil
	}
}

func detailsHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DetailsInput, DetailsOutput] {
	limiter := newRateLimiter("get_details", cfg.ToolRateLimitPerMinute)
	return func(ctx context.Context, req *mcp.CallToolRequest, input DetailsInput) (*mcp.CallToolResult, DetailsOutput, error) {
		if err := limiter.allow(); err != nil {
			return nil, DetailsOutput{}, err
		}

		result, err := details.GetDetails(ctx, client, logger, input.Hash)
		if err != nil {
			logger.Error("get details failed", zap.Error(err))
			return nil, DetailsOutput{}, toolError("details", err)
		}

		output := DetailsOutput{Result: *result}
		text := formatDetails(result)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, output, nil
	}
}

func downloadByDOIHandler(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) mcp.ToolHandlerFor[DOIDownloadInput, DOIDownloadOutput] {
	limiter := newRateLimiter("download_by_doi", cfg.ToolRateLimitPerMinute)
	return func(ctx context.Context, req *mcp.CallToolRequest, input DOIDownloadInput) (*mcp.CallToolResult, DOIDownloadOutput, error) {
		if err := limiter.allow(); err != nil {
			return nil, DOIDownloadOutput{}, err
		}

		if strings.TrimSpace(input.DOI) == "" {
			return nil, DOIDownloadOutput{}, usererror.New("INVALID_DOI", "DOI is required.")
		}
		if cfg.DownloadPath == "" {
			return nil, DOIDownloadOutput{}, usererror.New("PATH_REQUIRED",
				"Downloads require a download path. Set ANNAS_DOWNLOAD_PATH in your MCP server configuration.")
		}
		if info, err := os.Stat(cfg.DownloadPath); err != nil {
			return nil, DOIDownloadOutput{}, usererror.Wrap("PATH_UNAVAILABLE",
				"ANNAS_DOWNLOAD_PATH is not accessible. Check that the directory exists and is writable.", err)
		} else if !info.IsDir() {
			return nil, DOIDownloadOutput{}, usererror.New("PATH_UNAVAILABLE", "ANNAS_DOWNLOAD_PATH must be a directory.")
		}

		format := strings.TrimSpace(input.Format)
		if format == "" {
			format = "pdf"
		}
		title := strings.TrimSpace(input.Title)
		if title == "" {
			title = input.DOI
		}

		filename := download.SanitizeFilename(title, format)
		finalPath := filepath.Join(cfg.DownloadPath, filename)

		// Skip-if-exists — same contract as the `download` tool. Keeps the
		// interface idempotent and preserves rate-limit budget on both upstreams.
		if info, err := os.Stat(finalPath); err == nil && !info.IsDir() && info.Size() > 0 {
			logger.Info("download_by_doi skipped — file already exists",
				zap.String("path", finalPath), zap.Int64("size", info.Size()))
			out := DOIDownloadOutput{
				FilePath:       finalPath,
				Message:        "File already exists — download skipped.",
				Source:         "cache",
				AlreadyExisted: true,
			}
			return renderDOIDownload(&out), out, nil
		}

		// Try Anna's fast_download first (DOI → hash → download). Preferred
		// because it preserves Anna's metadata and runs through the
		// already-tested retry/backoff path.
		if annaResult, annaErr := tryAnnaByDOI(ctx, client, logger, cfg, input.DOI, title, format); annaErr == nil {
			// download.Download may have itself fallen back to libgen.li
			// internally; preserve whichever tier actually delivered.
			source := annaResult.Source
			if source == "" {
				source = "fast_download"
			}
			out := DOIDownloadOutput{
				FilePath:       annaResult.FilePath,
				Message:        annaResult.Message,
				Source:         source,
				AlreadyExisted: annaResult.AlreadyExisted,
			}
			return renderDOIDownload(&out), out, nil
		} else {
			// Config errors shouldn't fall through to sci-net — they signal
			// the operator needs to fix the setup, not that Anna's is missing
			// the paper.
			if isConfigError(annaErr) {
				logger.Error("download_by_doi: Anna's path blocked by config", zap.Error(annaErr))
				return nil, DOIDownloadOutput{}, toolError("download_by_doi", annaErr)
			}
			logger.Info("download_by_doi: Anna's path failed, falling back to sci-net", zap.Error(annaErr))
		}

		// Fall back to sci-net.xyz. Doesn't require ANNAS_SECRET_KEY, so this
		// also rescues users who haven't configured a donor key.
		body, _, sciErr := scinet.FetchPDF(ctx, client, cfg.ScinetBaseURL, input.DOI)
		if sciErr != nil {
			logger.Error("download_by_doi: sci-net fallback failed", zap.Error(sciErr))
			return nil, DOIDownloadOutput{}, toolError("download_by_doi", sciErr)
		}
		defer body.Close()

		path, err := download.AtomicWrite(cfg.DownloadPath, filename, body)
		if err != nil {
			return nil, DOIDownloadOutput{}, usererror.Wrap("IO_ERROR",
				"Could not save the downloaded file. Check ANNAS_DOWNLOAD_PATH is writable and has free space.", err)
		}
		logger.Info("download_by_doi complete via sci-net", zap.String("path", path))

		out := DOIDownloadOutput{
			FilePath: path,
			Message:  "Downloaded successfully via sci-net.",
			Source:   "sci-net",
		}
		return renderDOIDownload(&out), out, nil
	}
}

// tryAnnaByDOI resolves a DOI to a hash via Anna's Archive and then downloads
// it with the existing download pipeline. Returns the DownloadResult on
// success, or the unmodified underlying error on failure so the caller can
// distinguish config problems from content-missing.
func tryAnnaByDOI(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	doiStr, title, format string,
) (*model.DownloadResult, error) {
	meta, err := doi.Resolve(ctx, client, logger, doiStr)
	if err != nil {
		return nil, fmt.Errorf("resolve DOI: %w", err)
	}
	if meta == nil || meta.Hash == "" {
		return nil, usererror.New("NOT_FOUND", "Anna's Archive returned no hash for this DOI.")
	}
	return download.Download(ctx, client, logger, cfg, meta.Hash, title, format)
}

// isConfigError reports whether err describes an operator/config problem that
// the sci-net fallback cannot paper over. These are surfaced directly so the
// user fixes setup rather than silently trying an alternate path.
func isConfigError(err error) bool {
	var ue *usererror.Error
	if !errors.As(err, &ue) {
		return false
	}
	switch ue.Code {
	case "PATH_REQUIRED", "PATH_UNAVAILABLE", "INVALID_DOI", "INVALID_HASH":
		return true
	}
	return false
}

// renderDOIDownload builds the text body the MCP caller sees for a
// download_by_doi success. Matches the terse two-line shape the `download`
// tool produces, appending "(via <source>)" when the source is not cache.
func renderDOIDownload(out *DOIDownloadOutput) *mcp.CallToolResult {
	label := "File saved to"
	if out.AlreadyExisted {
		label = "File already at"
	}
	line2 := fmt.Sprintf("%s: %s", label, out.FilePath)
	if out.Source != "" && out.Source != "cache" {
		line2 = fmt.Sprintf("%s (via %s)", line2, out.Source)
	}
	text := fmt.Sprintf("%s\n%s", out.Message, line2)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func toolError(operation string, err error) error {
	var userErr *usererror.Error
	if errors.As(err, &userErr) {
		return userErr
	}

	if errors.Is(err, context.Canceled) {
		return usererror.New("REQUEST_CANCELLED", "Request cancelled.")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return usererror.New("REQUEST_TIMEOUT", timeoutMessage(operation))
	}

	var statusErr *httpclient.StatusError
	if errors.As(err, &statusErr) {
		if statusErr.DDoSGuard {
			return usererror.New("UPSTREAM_BLOCKED", "Anna's Archive is blocking automated requests from this network. Try again later or configure another mirror with ANNAS_BASE_URL.")
		}

		switch statusErr.StatusCode {
		case http.StatusTooManyRequests:
			msg := "Anna's Archive rate-limited the request. Wait a bit and try again."
			if statusErr.RetryAfter > 0 {
				msg = fmt.Sprintf("Anna's Archive rate-limited the request. Retry after %s.",
					statusErr.RetryAfter.Round(time.Second))
			}
			return usererror.New("RATE_LIMITED", msg)
		case http.StatusNotFound:
			return usererror.New("NOT_FOUND", notFoundMessage(operation))
		case http.StatusUnauthorized, http.StatusForbidden:
			return usererror.New("UPSTREAM_REJECTED", rejectedMessage(operation))
		}

		if statusErr.StatusCode >= 500 {
			return usererror.New("UPSTREAM_UNAVAILABLE", "Anna's Archive is temporarily unavailable. Try again later.")
		}
	}

	code, message := fallbackError(operation)
	return usererror.New(code, message)
}

func timeoutMessage(operation string) string {
	switch operation {
	case "search":
		return "Anna's Archive did not respond before the timeout. Try a narrower search or try again later."
	case "download":
		return "The download did not respond before the timeout. Try again later."
	default:
		return "Anna's Archive did not respond before the timeout. Try again later."
	}
}

func notFoundMessage(operation string) string {
	switch operation {
	case "details":
		return "No Anna's Archive item was found for that hash."
	case "doi":
		return "No Anna's Archive record was found for that DOI."
	case "download":
		return "No downloadable file was found for that hash."
	default:
		return "Anna's Archive could not find the requested resource."
	}
}

func rejectedMessage(operation string) string {
	if operation == "download" {
		return "Anna's Archive rejected the download request. Check ANNAS_SECRET_KEY and try again."
	}
	return "Anna's Archive rejected the request. Try again later or configure another mirror with ANNAS_BASE_URL."
}

func fallbackError(operation string) (string, string) {
	switch operation {
	case "search":
		return "SEARCH_FAILED", "Search failed. Anna's Archive may be temporarily unavailable."
	case "download":
		return "DOWNLOAD_FAILED", "Download failed. Try again later."
	case "download_by_doi":
		return "DOI_DOWNLOAD_FAILED", "Could not download this DOI from any configured source. Check the DOI, try again later, or download manually."
	case "doi":
		return "DOI_LOOKUP_FAILED", "DOI lookup failed. Verify the DOI and try again."
	case "details":
		return "DETAILS_FAILED", "Could not retrieve details. Verify the hash and try again."
	default:
		return "TOOL_FAILED", "Tool call failed. Try again later."
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
