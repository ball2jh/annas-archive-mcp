// Package libgen fetches files directly from libgen.li using their
// ads.php → get.php flow. This provides a fallback tier of file delivery
// that complements Anna's fast_download API and Sci-Net. The flow works
// for both books and journal articles, keyed by MD5 hash, and does NOT
// require an Anna's Archive membership key.
//
// Two-step dance:
//
//  1. GET https://{baseURL}/ads.php?md5={hash}
//     Response is an HTML page containing one or more
//     `get.php?md5={hash}&key={SESSION_KEY}` links. The session key is
//     short-lived (minutes), so each fetch re-issues this request.
//
//  2. GET https://{baseURL}/get.php?md5={hash}&key={SESSION_KEY}
//     Response follows a 307 redirect and delivers the file as
//     application/octet-stream. Our HTTP client follows redirects by
//     default, so callers receive the final body directly.
package libgen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// getKeyRE matches the session-keyed get.php link on an ads.php page.
// Libgen accepts hex-case-insensitive md5 and alphanumeric session keys.
// Match groups: 1 = md5 (echoed back), 2 = session key.
var getKeyRE = regexp.MustCompile(`(?i)get\.php\?md5=([0-9a-f]{32})&key=([A-Z0-9]+)`)

// FetchByHash resolves the given MD5 hash on libgen and returns an open
// body for the file. The caller is responsible for closing the body.
//
// Errors are *usererror.Error with one of:
//   - INVALID_HASH — hash is not a 32-character MD5 hex string
//   - NOT_FOUND_ON_LIBGEN — libgen's ads page does not list this file
//   - UPSTREAM_REJECTED — libgen returned an unexpected HTTP status
//   - IO_ERROR — local I/O failure reading a response body
//
// Network / timeout errors are returned wrapped (not classified) so that
// the caller's error policy (retry, report, etc.) can treat them as
// transient. Use errors.As with *usererror.Error to check classification.
func FetchByHash(ctx context.Context, client *httpclient.Client, baseURL, hash string) (io.ReadCloser, error) {
	// Libgen stores md5s in lowercase; Anna's Archive sometimes emits uppercase.
	hashLower, ok := model.NormalizeHash(hash)
	if !ok {
		return nil, usererror.New(usererror.CodeInvalidHash, "hash must be a 32-character MD5 hex string.")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, usererror.New(usererror.CodeConfig, "LIBGEN_BASE_URL is empty.")
	}

	adsURL := "https://" + baseURL + "/ads.php?md5=" + hashLower
	adsResp, err := client.Get(ctx, adsURL, nil)
	if err != nil {
		return nil, httpclient.SourceError("Libgen", fmt.Errorf("libgen: GET ads %s: %w", adsURL, err))
	}
	body, readErr := httpclient.ReadPage(adsResp)
	if readErr != nil {
		return nil, usererror.Wrap(usererror.CodeIOError,
			"Could not read libgen.li ads page body.", readErr)
	}
	if adsResp.StatusCode == http.StatusNotFound {
		return nil, errNotFound(hashLower)
	}
	if adsResp.StatusCode != http.StatusOK {
		return nil, usererror.Wrap(usererror.CodeUpstreamRejected,
			fmt.Sprintf("Libgen returned HTTP %d for hash %s.", adsResp.StatusCode, hashLower),
			fmt.Errorf("HTTP %d", adsResp.StatusCode))
	}

	match := getKeyRE.FindSubmatch(body)
	if match == nil {
		return nil, errNotFound(hashLower)
	}
	key := string(match[2])

	getURL := "https://" + baseURL + "/get.php?md5=" + hashLower + "&key=" + key
	getResp, err := client.Get(ctx, getURL, nil)
	if err != nil {
		return nil, httpclient.SourceError("Libgen", fmt.Errorf("libgen: GET file %s: %w", getURL, err))
	}
	if getResp.StatusCode != http.StatusOK {
		_ = getResp.Body.Close()
		return nil, usererror.Wrap(usererror.CodeUpstreamRejected,
			fmt.Sprintf("Libgen get.php returned HTTP %d for hash %s.", getResp.StatusCode, hashLower),
			fmt.Errorf("HTTP %d", getResp.StatusCode))
	}

	if httpclient.IsHTML(getResp) {
		// Libgen answers 200 with an error page when the session key has
		// expired or the mirror has lost the file.
		_ = getResp.Body.Close()
		return nil, usererror.New(usererror.CodeUpstreamRejected,
			fmt.Sprintf("Libgen returned a web page instead of the file for hash %s.", hashLower))
	}

	return getResp.Body, nil
}

func errNotFound(hash string) error {
	return usererror.New(usererror.CodeNotFoundOnLibgen,
		fmt.Sprintf("Libgen has no copy of hash %s.", hash))
}
