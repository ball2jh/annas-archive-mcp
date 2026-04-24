// Package details fetches and parses the detail page for a specific item,
// enriching the result with community stats from the inline_info API.
package details

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// hashRE matches a valid 32-character hexadecimal MD5 hash.
var hashRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
var dateOnlyRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var sourceIDRE = regexp.MustCompile(`^(lg|libgen|zlib)[a-z0-9_-]*\d*$`)

// GetDetails fetches full metadata for an item by MD5 hash.
//
// Flow:
//  1. Validate that hash is exactly 32 hex characters.
//  2. Fetch the detail page: GET /md5/{hash}.
//  3. Parse the HTML with scraper.ParseDetailPage.
//  4. If the scraper did not populate the hash, set it from the input.
//  5. Fetch community stats via api.FetchStats.
//  6. Attach stats to the result and return.
func GetDetails(ctx context.Context, client *httpclient.Client, logger *zap.Logger, hash string) (*model.BookDetails, error) {
	if !hashRE.MatchString(hash) {
		return nil, usererror.New("INVALID_HASH", "hash must be a 32-character MD5 hex string.")
	}

	doc, err := client.GetHTML(ctx, "/md5/"+hash)
	if err != nil {
		return nil, fmt.Errorf("details: fetch detail page for %q: %w", hash, err)
	}

	result, err := scraper.ParseDetailPage(doc, logger)
	if err != nil {
		return nil, fmt.Errorf("details: parse detail page for %q: %w", hash, err)
	}

	if result.Hash == "" {
		result.Hash = hash
	}
	result.Description = cleanDescription(result.Description)

	stats, err := api.FetchStats(ctx, client, hash)
	if err != nil {
		// Soft-fail: stats are supplementary. Log the error but return
		// the metadata we already scraped rather than failing the whole call.
		logger.Warn("details: stats unavailable, returning metadata without stats",
			zap.String("hash", hash),
			zap.Error(err),
		)
	} else {
		result.Stats = stats
	}

	return result, nil
}

func cleanDescription(raw string) string {
	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || looksLikeSourceMetadata(line) {
			continue
		}
		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, "\n")
}

func looksLikeSourceMetadata(line string) bool {
	lower := strings.ToLower(line)
	if dateOnlyRE.MatchString(lower) {
		return true
	}
	if strings.HasPrefix(lower, "lgli/") ||
		strings.HasPrefix(lower, "lgrsnf/") ||
		strings.HasPrefix(lower, "zlib/") ||
		strings.HasPrefix(lower, "libgen/") {
		return true
	}
	if strings.Contains(lower, "/") && hasFileExtension(lower) {
		return true
	}
	return sourceIDRE.MatchString(lower)
}

func hasFileExtension(line string) bool {
	for _, ext := range []string{".epub", ".pdf", ".mobi", ".azw3", ".djvu", ".cbz", ".cbr"} {
		if strings.Contains(line, ext) {
			return true
		}
	}
	return false
}
