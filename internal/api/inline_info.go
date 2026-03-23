// Package api wraps Anna's Archive JSON endpoints.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
)

// inlineInfoResponse is the raw JSON shape returned by the inline_info endpoint.
type inlineInfoResponse struct {
	ReportsCount      int `json:"reports_count"`
	CommentsCount     int `json:"comments_count"`
	ListsCount        int `json:"lists_count"`
	DownloadsTotal    int `json:"downloads_total"`
	GreatQualityCount int `json:"great_quality_count"`
}

// FetchStats fetches community stats for a single MD5 hash.
//
// The Accept: text/css header is sent as a known DDoS-Guard caching bypass
// used by Anna's Archive's own frontend.
func FetchStats(ctx context.Context, client *httpclient.Client, hash string) (model.CommunityStats, error) {
	path := "/dyn/md5/inline_info/" + hash
	headers := map[string]string{
		"Accept": "text/css",
	}

	data, err := client.GetJSON(ctx, path, headers)
	if err != nil {
		return model.CommunityStats{}, fmt.Errorf("api: FetchStats %s: %w", hash, err)
	}

	var raw inlineInfoResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return model.CommunityStats{}, fmt.Errorf("api: FetchStats %s: parse JSON: %w", hash, err)
	}

	quality := ""
	if raw.GreatQualityCount > 0 {
		quality = strconv.Itoa(raw.GreatQualityCount)
	}

	return model.CommunityStats{
		Downloads: raw.DownloadsTotal,
		Lists:     raw.ListsCount,
		Quality:   quality,
		Comments:  raw.CommentsCount,
		Reports:   raw.ReportsCount,
	}, nil
}

// FetchStatsParallel fetches community stats for multiple MD5 hashes
// concurrently. maxConcurrency limits how many requests are in flight at once.
//
// Results are collected in a map keyed by MD5 hash. Hashes that fail are
// logged and omitted from the result map; the caller should treat a missing
// key as "stats unavailable" rather than a fatal error.
func FetchStatsParallel(
	ctx context.Context,
	client *httpclient.Client,
	hashes []string,
	maxConcurrency int,
	logger *zap.Logger,
) map[string]model.CommunityStats {
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	sem := make(chan struct{}, maxConcurrency)
	results := make(map[string]model.CommunityStats, len(hashes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, h := range hashes {
		h := h // capture loop variable
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Acquire semaphore slot.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			stats, err := FetchStats(ctx, client, h)
			if err != nil {
				if logger != nil {
					logger.Warn("api: FetchStatsParallel: skipping hash",
						zap.String("hash", h),
						zap.Error(err),
					)
				}
				return
			}

			mu.Lock()
			results[h] = stats
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}
