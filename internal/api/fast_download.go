package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
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
//
// The Anna's Archive fast_download API returns structured JSON errors on both
// 200 and 400 responses, so we read the body regardless of status and parse
// the "error" field.
func resolveSingle(ctx context.Context, client *httpclient.Client, hash, secretKey string, domainIndex int) (string, error) {
	params := url.Values{}
	params.Set("md5", hash)
	params.Set("key", secretKey)
	params.Set("path_index", "0")
	params.Set("domain_index", fmt.Sprintf("%d", domainIndex))
	path := "/dyn/api/fast_download.json?" + params.Encode()

	data, status, err := client.GetJSONStatus(ctx, path, nil)
	if err != nil {
		return "", fmt.Errorf("domain_index=%d: %w", domainIndex, err)
	}

	// 401/403/404/429/5xx — treat as HTTP failure (body may not be JSON).
	// 200 and 400 both carry the structured JSON body we need to inspect.
	if status != 200 && status != 400 {
		return "", &httpclient.StatusError{
			Operation:  "fast_download",
			Path:       sanitizeAPIPath(hash, domainIndex),
			StatusCode: status,
		}
	}

	var raw fastDownloadResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("domain_index=%d: parse JSON from status %d: %w", domainIndex, status, err)
	}

	// API signals failure via a non-empty error string and null download_url.
	if raw.Error != "" {
		err := errors.New(raw.Error)
		lower := strings.ToLower(raw.Error)
		switch {
		case strings.Contains(lower, "secret key"):
			return "", usererror.Wrap("AUTH_INVALID",
				"Anna's Archive rejected ANNAS_SECRET_KEY. Update the key in your MCP configuration.", err)
		case strings.Contains(lower, "daily download limit"),
			strings.Contains(lower, "quota"),
			strings.Contains(lower, "limit reached"):
			return "", usererror.Wrap("QUOTA_EXHAUSTED",
				"Anna's Archive daily download quota reached. The limit resets every 24 hours; higher donation tiers have larger quotas.", err)
		case strings.Contains(lower, "invalid domain_index"),
			strings.Contains(lower, "invalid path_index"):
			// The file isn't in any of Anna's fast_download mirrors — it's only
			// reachable via external collections (Libgen.li, SciNet, Z-Library,
			// etc.). Give the user a direct link to the file's page so they can
			// download manually.
			return "", usererror.Wrap("NOT_FAST_DOWNLOADABLE",
				fmt.Sprintf("This file is not available via Anna's Archive's fast_download API; it lives in an external collection (Libgen, SciNet, Z-Library, etc.) that the API cannot proxy. Download it manually at https://annas-archive.org/md5/%s.", hash),
				err)
		}
		// Unknown API error — surface the raw message so the user learns
		// something actionable instead of the opaque fallback.
		return "", usererror.Wrap("UPSTREAM_API_ERROR",
			fmt.Sprintf("Anna's Archive API error: %s", raw.Error), err)
	}

	if raw.DownloadURL == nil || *raw.DownloadURL == "" {
		return "", fmt.Errorf("domain_index=%d: download_url is empty", domainIndex)
	}

	return *raw.DownloadURL, nil
}

// sanitizeAPIPath returns a redacted /dyn/api/fast_download.json URL for
// embedding in errors without leaking the API key.
func sanitizeAPIPath(hash string, domainIndex int) string {
	return fmt.Sprintf("/dyn/api/fast_download.json?md5=%s&key=REDACTED&path_index=0&domain_index=%d",
		hash, domainIndex)
}
