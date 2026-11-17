// Package details fetches and parses the detail page for a specific item,
// enriching the result with community stats from the inline_info API.
package details

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

var dateOnlyRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var sourceIDRE = regexp.MustCompile(`^(lg|libgen|zlib)[a-z0-9_-]*\d*$`)

// GetDetails fetches full metadata for an item by MD5 hash.
//
// The detail page (GET /md5/{hash}) and the community stats (inline_info API)
// are fetched concurrently. Stats are supplementary: if they fail or exceed
// statsTimeout, the scraped metadata is returned without them. A zero
// statsTimeout means no separate limit.
func GetDetails(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	rawHash string,
	statsTimeout time.Duration,
) (*model.BookDetails, error) {
	hash, ok := model.NormalizeHash(rawHash)
	if !ok {
		return nil, usererror.New(usererror.CodeInvalidHash, "hash must be a 32-character MD5 hex string.")
	}

	statsCtx, cancelStats := context.WithCancel(ctx)
	defer cancelStats()
	if statsTimeout > 0 {
		statsCtx, cancelStats = context.WithTimeout(statsCtx, statsTimeout)
		defer cancelStats()
	}

	type statsResult struct {
		stats model.CommunityStats
		err   error
	}
	statsCh := make(chan statsResult, 1)
	go func() {
		stats, err := api.FetchStats(statsCtx, client, hash)
		statsCh <- statsResult{stats, err}
	}()

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

	if sr := <-statsCh; sr.err != nil {
		logger.Warn("details: stats unavailable, returning metadata without stats",
			zap.String("hash", hash),
			zap.Error(sr.err),
		)
	} else {
		result.Stats = sr.stats
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
