// Package details fetches and parses the detail page for a specific item,
// enriching the result with community stats from the inline_info API.
package details

import (
	"context"
	"fmt"
	"regexp"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
)

// hashRE matches a valid 32-character hexadecimal MD5 hash.
var hashRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

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
		return nil, fmt.Errorf("details: invalid hash %q: must be exactly 32 hex characters", hash)
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
