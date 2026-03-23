package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
)

// fastDownloadResponse is the raw JSON shape returned by fast_download.json.
// download_url is nullable in the API, so we decode it as *string.
type fastDownloadResponse struct {
	DownloadURL *string `json:"download_url"`
	Error       string  `json:"error"`
}

// domainIndexFallbacks lists the domain_index values tried in order before
// giving up. The API sometimes returns an error for index 0 but succeeds for
// a higher index on the same server cluster.
var domainIndexFallbacks = []int{0, 1, 2}

// ResolveDownloadURL retrieves the download URL for a file identified by its
// MD5 hash. It tries domain_index values 0, 1, 2 in order and returns the
// first successful URL.
func ResolveDownloadURL(ctx context.Context, client *httpclient.Client, hash, secretKey string) (string, error) {
	var lastErr error

	for _, domainIdx := range domainIndexFallbacks {
		url, err := resolveSingle(ctx, client, hash, secretKey, domainIdx)
		if err == nil {
			return url, nil
		}
		lastErr = err
	}

	return "", fmt.Errorf("api: ResolveDownloadURL %s: all domain indices failed: %w", hash, lastErr)
}

// resolveSingle performs a single fast_download.json request for the given
// domain_index. It returns the download URL on success, or an error describing
// why the request failed (including API-level error messages).
func resolveSingle(ctx context.Context, client *httpclient.Client, hash, secretKey string, domainIndex int) (string, error) {
	params := url.Values{}
	params.Set("md5", hash)
	params.Set("key", secretKey)
	params.Set("path_index", "0")
	params.Set("domain_index", fmt.Sprintf("%d", domainIndex))
	path := "/dyn/api/fast_download.json?" + params.Encode()

	data, err := client.GetJSON(ctx, path, nil)
	if err != nil {
		return "", fmt.Errorf("domain_index=%d: %w", domainIndex, err)
	}

	var raw fastDownloadResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("domain_index=%d: parse JSON: %w", domainIndex, err)
	}

	// API signals failure via a non-empty error string and null download_url.
	if raw.Error != "" {
		return "", errors.New(raw.Error)
	}

	if raw.DownloadURL == nil || *raw.DownloadURL == "" {
		return "", fmt.Errorf("domain_index=%d: download_url is empty", domainIndex)
	}

	return *raw.DownloadURL, nil
}
