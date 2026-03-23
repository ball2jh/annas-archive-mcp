// Package httpclient provides a centralized HTTP client with retry, exponential
// backoff, DDoS-Guard detection, and configurable headers.
package httpclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
)

const (
	// userAgent mimics a modern browser to reduce the chance of bot detection.
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	// ddosGuardMarker is the string we look for in 403 response bodies.
	ddosGuardMarker = "DDoS-Guard"

	// ddosGuardSniffLimit is the maximum number of bytes read from a 403 body
	// when checking for the DDoS-Guard marker.
	ddosGuardSniffLimit = 4 * 1024 // 4 KB

	// baseBackoff is the initial wait duration between retries.
	baseBackoff = 1 * time.Second

	// jitterMax is the maximum extra random duration added to each backoff.
	jitterMax = 500 * time.Millisecond
)

// Client is a thin wrapper around *http.Client that adds retry logic,
// exponential backoff, and DDoS-Guard detection.
type Client struct {
	http       *http.Client
	logger     *zap.Logger
	maxRetries int
	baseURL    string // bare hostname, no protocol
	userAgent  string
}

// New creates a Client configured from cfg. The underlying *http.Client uses
// cfg.HTTPTimeout as its per-request deadline.
func New(cfg *config.Config, logger *zap.Logger) *Client {
	return &Client{
		http: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
		logger:     logger,
		maxRetries: cfg.MaxRetries,
		baseURL:    cfg.BaseURL,
		userAgent:  userAgent,
	}
}

// Do executes req with retry and exponential backoff.
//
// Retry policy:
//   - 5xx responses and network/timeout errors → retriable
//   - 429 (Too Many Requests) → retriable
//   - 403 → sniff body for DDoS-Guard; NOT retriable in either case
//   - other 4xx → NOT retriable
//   - 2xx / 3xx → success, no retry
//
// The caller is responsible for closing the response body on success.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	var (
		resp     *http.Response
		lastErr  error
		attempts = c.maxRetries + 1 // first attempt + retries
	)

	for attempt := 0; attempt < attempts; attempt++ {
		// Wait before each retry (not before the first attempt).
		if attempt > 0 {
			wait := backoffDuration(attempt-1) // attempt-1 so first retry uses exponent 0
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("httpclient: context cancelled while waiting for retry: %w", ctx.Err())
			case <-time.After(wait):
			}
		}

		// Close the body of any previous non-nil response before reusing the
		// request slot (we only get here on a retriable status).
		if resp != nil {
			_ = resp.Body.Close()
			resp = nil
		}

		// Clone the request so we can reuse it across retries. A shallow
		// clone is sufficient because we do not mutate the body.
		cloned := req.Clone(ctx)

		resp, lastErr = c.http.Do(cloned) //nolint:bodyclose // caller closes on success
		if lastErr != nil {
			// Network / timeout error — always retriable.
			c.logger.Warn("httpclient: request error, will retry",
				zap.Int("attempt", attempt+1),
				zap.Int("maxAttempts", attempts),
				zap.Error(lastErr),
			)
			continue
		}

		switch {
		case resp.StatusCode == http.StatusForbidden:
			// 403: check for DDoS-Guard, then return immediately (not retriable).
			if c.isDDoSGuard(resp) {
				c.logger.Warn("DDoS-Guard challenge detected — this IP may be blocked",
					zap.String("url", req.URL.String()),
				)
			}
			return resp, nil

		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			// Retriable status codes.
			c.logger.Warn("httpclient: retriable status, will retry",
				zap.Int("statusCode", resp.StatusCode),
				zap.Int("attempt", attempt+1),
				zap.Int("maxAttempts", attempts),
				zap.String("url", req.URL.String()),
			)
			lastErr = fmt.Errorf("httpclient: received status %d", resp.StatusCode)
			continue

		default:
			// 2xx, 3xx, or other 4xx — return as-is.
			return resp, nil
		}
	}

	// All attempts exhausted. Close the last (retriable) response body if
	// we have one — the caller will receive an error, not a body.
	if resp != nil {
		_ = resp.Body.Close()
	}

	if lastErr != nil {
		return nil, fmt.Errorf("httpclient: all %d attempt(s) failed: %w", attempts, lastErr)
	}

	// Should be unreachable, but be safe.
	return nil, fmt.Errorf("httpclient: all %d attempt(s) failed", attempts)
}

// Get is a convenience wrapper that builds a GET request with the given headers
// (plus User-Agent) and calls Do.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpclient: build request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.Do(ctx, req)
}

// GetHTML fetches the page at https://{BaseURL}+path, parses it with goquery,
// and returns the document. The response body is closed before returning.
func (c *Client) GetHTML(ctx context.Context, path string) (*goquery.Document, error) {
	url := "https://" + c.baseURL + path

	resp, err := c.Get(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpclient: GetHTML %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("httpclient: GetHTML %s: unexpected status %d", path, resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpclient: GetHTML %s: parse HTML: %w", path, err)
	}

	return doc, nil
}

// GetJSON fetches https://{BaseURL}+path with optional headers and returns the
// raw response bytes. The response body is closed before returning.
func (c *Client) GetJSON(ctx context.Context, path string, headers map[string]string) ([]byte, error) {
	url := "https://" + c.baseURL + path

	resp, err := c.Get(ctx, url, headers)
	if err != nil {
		return nil, fmt.Errorf("httpclient: GetJSON %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("httpclient: GetJSON %s: unexpected status %d", path, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpclient: GetJSON %s: read body: %w", path, err)
	}

	return data, nil
}

// NewForTest creates a Client suitable for unit tests. baseURL should be the
// httptest server address without a scheme (e.g. "127.0.0.1:PORT"). The
// provided httpClient can carry a custom transport that routes HTTPS requests
// to the plain-HTTP test server.
func NewForTest(httpClient *http.Client, baseURL string, logger *zap.Logger) *Client {
	return &Client{
		http:       httpClient,
		logger:     logger,
		maxRetries: 0,
		baseURL:    baseURL,
		userAgent:  userAgent,
	}
}

// isDDoSGuard reads up to ddosGuardSniffLimit bytes from resp.Body, replaces
// the body with a reader over those bytes so callers can still read it, and
// reports whether the DDoS-Guard marker was present.
func (c *Client) isDDoSGuard(resp *http.Response) bool {
	limited := io.LimitReader(resp.Body, ddosGuardSniffLimit)
	sniffed, err := io.ReadAll(limited)
	if err != nil {
		// Can't read the body; replace with empty reader and assume no marker.
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return false
	}

	// Replace the body so any caller that reads it still sees the content.
	resp.Body = io.NopCloser(bytes.NewReader(sniffed))

	return bytes.Contains(sniffed, []byte(ddosGuardMarker))
}

