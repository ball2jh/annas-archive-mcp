// Package search orchestrates fetching and enriching search results from
// Anna's Archive by combining HTML scraping with community stats enrichment.
package search

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// Options tunes the supplementary stats enrichment. Zero values are safe:
// no separate stats deadline and one request at a time.
type Options struct {
	// StatsTimeout bounds the whole stats-enrichment phase.
	StatsTimeout time.Duration
	// MaxConcurrency caps parallel stats requests.
	MaxConcurrency int
}

// Search performs a search on Anna's Archive and returns enriched results.
//
// Flow:
//  1. Validate the query and content type.
//  2. Fetch and parse the search HTML page.
//  3. Fetch community stats in parallel for each result's MD5 hash. Stats are
//     best-effort: results whose stats fail or time out are returned without.
func Search(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	query string,
	contentType model.ContentType,
	limit int,
	opts Options,
) ([]model.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, usererror.New(usererror.CodeInvalidQuery, "Search query is required.")
	}
	if !model.ValidContentType(contentType) {
		return nil, usererror.New(usererror.CodeInvalidContentType, fmt.Sprintf("content_type must be one of: %s.", validContentTypes()))
	}

	doc, err := client.GetHTML(ctx, buildSearchPath(query, contentType, limit))
	if err != nil {
		return nil, fmt.Errorf("search: fetch page: %w", err)
	}

	results := scraper.ParseSearchResults(doc, logger)

	// Trim to the requested limit before fetching stats to avoid
	// unnecessary API calls for results that will be discarded.
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	if len(results) == 0 {
		return nil, nil
	}

	hashes := make([]string, len(results))
	for i, r := range results {
		hashes[i] = r.Hash // ParseSearchResults drops entries without a hash
	}

	statsCtx := ctx
	if opts.StatsTimeout > 0 {
		var cancel context.CancelFunc
		statsCtx, cancel = context.WithTimeout(ctx, opts.StatsTimeout)
		defer cancel()
	}
	statsMap := api.FetchStatsParallel(statsCtx, client, hashes, opts.MaxConcurrency, logger)
	for i := range results {
		if stats, ok := statsMap[results[i].Hash]; ok {
			results[i].Stats = stats
		}
	}

	return results, nil
}

func validContentTypes() string {
	values := make([]string, len(model.ContentTypes))
	for i, ct := range model.ContentTypes {
		values[i] = string(ct)
	}
	return strings.Join(values, ", ")
}

// buildSearchPath constructs the /search query path from the provided
// parameters. The content param is omitted when the content type maps to an
// empty string (i.e. book_any = no filter).
func buildSearchPath(query string, contentType model.ContentType, limit int) string {
	params := url.Values{}
	params.Set("q", query)

	if segment := model.ContentTypePath[contentType]; segment != "" {
		params.Set("content", segment)
	}

	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	return "/search?" + params.Encode()
}
