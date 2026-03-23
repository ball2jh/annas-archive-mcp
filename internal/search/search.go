// Package search orchestrates fetching and enriching search results from
// Anna's Archive by combining HTML scraping with community stats enrichment.
package search

import (
	"context"
	"fmt"
	"net/url"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
)

const maxConcurrency = 10

// Search performs a search on Anna's Archive and returns enriched results.
//
// Flow:
//  1. Validate content type.
//  2. Build the search URL path with query, content filter, and limit.
//  3. Fetch and parse the search HTML page.
//  4. Fetch community stats in parallel for each result's MD5 hash.
//  5. Enrich each result with its stats and return, trimmed to limit.
func Search(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	query string,
	contentType model.ContentType,
	limit int,
) ([]model.SearchResult, error) {
	if !model.ValidContentType(contentType) {
		return nil, fmt.Errorf("search: invalid content type %q", contentType)
	}

	path := buildSearchPath(query, contentType, limit)

	doc, err := client.GetHTML(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("search: fetch page: %w", err)
	}

	results := scraper.ParseSearchResults(doc, logger)
	if len(results) == 0 {
		return nil, nil
	}

	// Trim to the requested limit before fetching stats to avoid
	// unnecessary API calls for results that will be discarded.
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Collect unique MD5 hashes for parallel stats fetch.
	hashes := make([]string, 0, len(results))
	for _, r := range results {
		if r.Hash != "" {
			hashes = append(hashes, r.Hash)
		}
	}

	if len(hashes) > 0 {
		statsMap := api.FetchStatsParallel(ctx, client, hashes, maxConcurrency, logger)
		for i := range results {
			if stats, ok := statsMap[results[i].Hash]; ok {
				results[i].Stats = stats
			}
		}
	}

	return results, nil
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
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	return "/search?" + params.Encode()
}
