// Package httpclient provides a centralized HTTP client with retry, exponential
// backoff, DDoS-Guard detection, and configurable headers.
package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
)

const (
	// userAgent mimics a modern browser to reduce the chance of bot detection.
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

	// ddosGuardMarker is the string we look for (case-insensitively) in 403
	// response bodies.
	ddosGuardMarker = "ddos-guard"

	// ddosGuardSniffLimit is the maximum number of bytes read from a 403 body
	// when checking for the DDoS-Guard marker.
	ddosGuardSniffLimit = 4 * 1024 // 4 KB

	// maxBufferedBody caps how much of an HTML or JSON response is read into
	// memory. Real pages are well under 1 MB; this only guards against a
	// misbehaving upstream.
	maxBufferedBody = 32 << 20 // 32 MB

	// baseBackoff is the initial wait duration between retries.
	baseBackoff = 1 * time.Second

	// jitterMax is the maximum extra random duration added to each backoff.
	jitterMax = 500 * time.Millisecond
)

// Client is a thin wrapper around *http.Client that adds retry logic,
// exponential backoff, stall detection, and DDoS-Guard detection.
type Client struct {
	http       *http.Client
	logger     *zap.Logger
	maxRetries int
	baseURL    string // bare hostname, no protocol

	// userAgent is sent on every request. It starts as the built-in default
	// and is replaced by the browser's own when a session is applied.
	userAgent atomic.Pointer[string]

	// refresh obtains a fresh browser session when the upstream answers with
	// a DDoS-Guard challenge. Nil disables challenge recovery.
	refresh SessionRefresher
	// refreshMu serialises refreshes; sessionGen counts applied sessions so a
	// request that was challenged while another one refreshed just retries.
	refreshMu  sync.Mutex
	sessionGen atomic.Int64

	// stallTimeout bounds how long a request may go without progress: first
	// waiting for response headers, then inside any single body Read. It is
	// deliberately not a total-request deadline, so large downloads can take
	// as long as they need provided bytes keep arriving. Zero disables it.
	stallTimeout time.Duration
}

// errStalled is the cancellation cause used when the stall watchdog fires. It
// wraps context.DeadlineExceeded so callers classify it as a timeout.
var errStalled = fmt.Errorf("httpclient: no response activity within the timeout: %w", context.DeadlineExceeded)

// StatusError reports a non-OK upstream HTTP response. It intentionally keeps
// the raw path/status available for logs while allowing callers to classify the
// failure without parsing strings.
//
// RetryAfter carries a parsed Retry-After hint from the server when present
// (0 otherwise). Tool-level handlers use it to tell the user exactly how long
// to wait before retrying — the `httpclient.Do` loop has already waited for
// the hint when applicable, so callers see it as context, not as a requirement.
type StatusError struct {
	Operation  string
	Path       string
	StatusCode int
	DDoSGuard  bool
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.DDoSGuard {
		return fmt.Sprintf("httpclient: %s %s: upstream DDoS-Guard challenge", e.Operation, e.Path)
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("httpclient: %s %s: status %d (Retry-After %s)",
			e.Operation, e.Path, e.StatusCode, e.RetryAfter.Round(time.Second))
	}
	return fmt.Sprintf("httpclient: %s %s: unexpected status %d", e.Operation, e.Path, e.StatusCode)
}

// New creates a Client configured from cfg. cfg.HTTPTimeout is applied as a
// stall timeout (see Client.stallTimeout), not as a cap on total request time.
func New(cfg *config.Config, logger *zap.Logger) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The default of 2 idle connections per host forces a fresh TLS handshake
	// for most of the parallel stats requests issued by every search.
	transport.MaxIdleConnsPerHost = max(cfg.MaxConcurrency, transport.MaxIdleConnsPerHost, 2)

	return newClient(&http.Client{Transport: transport}, cfg.BaseURL, logger, cfg.MaxRetries, cfg.HTTPTimeout)
}

func newClient(hc *http.Client, baseURL string, logger *zap.Logger, maxRetries int, stallTimeout time.Duration) *Client {
	if hc.Jar == nil {
		// Holds the browser-session cookies (see UseSession) and any the
		// upstream rotates afterwards.
		hc.Jar, _ = cookiejar.New(nil) // never fails with nil options
	}
	c := &Client{
		http:         hc,
		logger:       logger,
		maxRetries:   maxRetries,
		baseURL:      baseURL,
		stallTimeout: stallTimeout,
	}
	defaultUA := userAgent
	c.userAgent.Store(&defaultUA)
	return c
}

// Session is what a real browser holds after passing the upstream's
// DDoS-Guard challenge: its cookies and the User-Agent it presented.
type Session struct {
	UserAgent string
	Cookies   []*http.Cookie
}

// SessionRefresher obtains a fresh Session, typically by letting a real
// browser pass the challenge. It is called at most once at a time.
type SessionRefresher func(ctx context.Context) (Session, error)

// UseSession makes the client send the session's cookies to the Anna's
// Archive host and adopt its User-Agent.
func (c *Client) UseSession(s Session) {
	if s.UserAgent != "" {
		c.userAgent.Store(&s.UserAgent)
	}
	c.http.Jar.SetCookies(&url.URL{Scheme: "https", Host: c.baseURL, Path: "/"}, s.Cookies)
	c.sessionGen.Add(1)
}

// OnChallenge sets the function used to recover from a DDoS-Guard challenge.
// It must be called before the client is used.
func (c *Client) OnChallenge(refresh SessionRefresher) {
	c.refresh = refresh
}

// BaseURL returns the configured Anna's Archive hostname (no scheme).
func (c *Client) BaseURL() string {
	return c.baseURL
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
		lastErr        error
		lastRetryAfter time.Duration
		attempts       = c.maxRetries + 1 // first attempt + retries
		recovered      bool               // challenge recovery is tried once per request
	)

	for attempt := 0; attempt < attempts; attempt++ {
		// Wait before each retry (not before the first attempt). Honor any
		// server Retry-After hint from the previous response.
		if attempt > 0 {
			wait := retryWait(backoffDuration(attempt-1), lastRetryAfter)
			// Fail fast when the requested wait would exceed the context
			// deadline — don't burn time we don't have.
			if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
				return nil, fmt.Errorf("httpclient: retry wait (%s) exceeds remaining context deadline: %w (last error: %v)",
					wait.Round(time.Second), context.DeadlineExceeded, lastErr)
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("httpclient: context cancelled while waiting for retry: %w", ctx.Err())
			case <-timer.C:
			}
		}

		gen := c.sessionGen.Load()
		resp, err := c.attempt(ctx, req)
		if err == nil && !recovered && resp.StatusCode == http.StatusForbidden && c.refresh != nil && isDDoSGuard(resp) {
			recovered = true
			if c.recoverSession(ctx, gen) {
				_ = resp.Body.Close()
				resp, err = c.attempt(ctx, req)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				// The caller gave up; retrying cannot succeed.
				return nil, fmt.Errorf("httpclient: %w", err)
			}
			lastErr = err
			lastRetryAfter = 0 // no header to honor on a network failure
			c.logger.Warn("httpclient: request error, will retry",
				zap.Int("attempt", attempt+1),
				zap.Int("maxAttempts", attempts),
				zap.Error(err),
			)
			continue
		}

		switch {
		case resp.StatusCode == http.StatusForbidden:
			// 403: check for DDoS-Guard, then return immediately (not retriable).
			if isDDoSGuard(resp) {
				c.logger.Warn("DDoS-Guard challenge was not passed",
					zap.String("url", sanitizeURL(req.URL)),
				)
			}
			return resp, nil

		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			// Retriable status codes. Extract any Retry-After hint so the next
			// iteration can wait at least that long.
			lastRetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			_ = resp.Body.Close()
			c.logger.Warn("httpclient: retriable status, will retry",
				zap.Int("statusCode", resp.StatusCode),
				zap.Int("attempt", attempt+1),
				zap.Int("maxAttempts", attempts),
				zap.Duration("retryAfter", lastRetryAfter),
				zap.String("url", sanitizeURL(req.URL)),
			)
			lastErr = &StatusError{
				Operation:  "request",
				Path:       sanitizeURL(req.URL),
				StatusCode: resp.StatusCode,
				RetryAfter: lastRetryAfter,
			}

		default:
			// 2xx, 3xx, or other 4xx — return as-is.
			return resp, nil
		}
	}

	return nil, fmt.Errorf("httpclient: all %d attempt(s) failed: %w", attempts, lastErr)
}

// recoverSession refreshes the browser session after a challenge and reports
// whether the request is worth repeating. gen is the session generation the
// challenged request was sent with.
func (c *Client) recoverSession(ctx context.Context, gen int64) bool {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if c.sessionGen.Load() != gen {
		return true // another request refreshed the session while we waited
	}
	c.logger.Info("httpclient: DDoS-Guard challenge — refreshing browser session")
	session, err := c.refresh(ctx)
	if err != nil {
		c.logger.Warn("httpclient: could not refresh browser session", zap.Error(err))
		return false
	}
	c.UseSession(session)
	return true
}

// attempt performs a single request guarded by the stall watchdog. On success
// the response body keeps the watchdog armed for the duration of each Read.
func (c *Client) attempt(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c.stallTimeout <= 0 {
		resp, err := c.http.Do(c.prepare(ctx, req))
		return resp, redactURLError(err)
	}

	attemptCtx, cancel := context.WithCancelCause(ctx)
	watchdog := time.AfterFunc(c.stallTimeout, func() { cancel(errStalled) })

	resp, err := c.http.Do(c.prepare(attemptCtx, req))
	watchdog.Stop()
	if err != nil {
		if cause := context.Cause(attemptCtx); errors.Is(cause, errStalled) {
			err = fmt.Errorf("%s %s: %w", req.Method, sanitizeURL(req.URL), cause)
		}
		cancel(nil)
		return nil, redactURLError(err)
	}

	resp.Body = &watchedBody{
		ReadCloser: resp.Body,
		ctx:        attemptCtx,
		cancel:     cancel,
		watchdog:   watchdog,
		timeout:    c.stallTimeout,
	}
	return resp, nil
}

// prepare clones req for one attempt and stamps the current User-Agent, so a
// retry after a session refresh presents the browser's own.
func (c *Client) prepare(ctx context.Context, req *http.Request) *http.Request {
	clone := req.Clone(ctx)
	clone.Header.Set("User-Agent", *c.userAgent.Load())
	return clone
}

// watchedBody arms the stall watchdog for the duration of each Read so a
// connection that stops delivering bytes fails instead of hanging forever.
type watchedBody struct {
	io.ReadCloser
	ctx      context.Context
	cancel   context.CancelCauseFunc
	watchdog *time.Timer
	timeout  time.Duration
}

func (b *watchedBody) Read(p []byte) (int, error) {
	b.watchdog.Reset(b.timeout)
	n, err := b.ReadCloser.Read(p)
	b.watchdog.Stop()
	if err != nil && err != io.EOF {
		if cause := context.Cause(b.ctx); errors.Is(cause, errStalled) {
			err = cause
		}
	}
	return n, err
}

func (b *watchedBody) Close() error {
	b.watchdog.Stop()
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// redactURLError strips sensitive query parameters from the URL that
// net/http embeds in its errors, so the secret key never reaches the logs.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if u, parseErr := url.Parse(urlErr.URL); parseErr == nil {
			urlErr.URL = sanitizeURL(u)
		} else {
			urlErr.URL = "<unparseable URL>"
		}
	}
	return err
}

// Get is a convenience wrapper that builds a GET request with the given headers
// and calls Do.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpclient: build request: %w", err)
	}

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
		return nil, StatusErrorFromResponse("GetHTML", path, resp)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxBufferedBody))
	if err != nil {
		return nil, fmt.Errorf("httpclient: GetHTML %s: parse HTML: %w", path, err)
	}

	return doc, nil
}

// GetJSON fetches https://{BaseURL}+path with optional headers and returns the
// raw response bytes. The response body is closed before returning. Non-200
// responses are converted to *StatusError and the body is discarded. Callers
// that need to inspect an error body (e.g. the fast_download API returns a
// structured JSON error on HTTP 400) should use GetJSONStatus instead.
func (c *Client) GetJSON(ctx context.Context, path string, headers map[string]string) ([]byte, error) {
	body, status, err := c.GetJSONStatus(ctx, path, headers)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, NewStatusError("GetJSON", path, status, body)
	}
	return body, nil
}

// GetJSONStatus is like GetJSON but returns the body and HTTP status code for
// every non-transport success, allowing callers to parse JSON error bodies on
// 4xx responses. Network errors and retry-exhausted errors still return a
// non-nil error (e.g. *StatusError for 5xx/429 after retries).
//
// On success the returned status is always the final HTTP status code (may be
// 2xx or 4xx); callers decide how to interpret it.
func (c *Client) GetJSONStatus(ctx context.Context, path string, headers map[string]string) ([]byte, int, error) {
	url := "https://" + c.baseURL + path

	resp, err := c.Get(ctx, url, headers)
	if err != nil {
		return nil, 0, fmt.Errorf("httpclient: GetJSON %s: %w", path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBufferedBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("httpclient: GetJSON %s: read body: %w", path, err)
	}
	return data, resp.StatusCode, nil
}

// IsHTML reports whether resp declares an HTML body. File endpoints that
// answer 200 with HTML are serving an error or challenge page, not the file.
func IsHTML(resp *http.Response) bool {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

// NewForTest creates a Client suitable for unit tests. baseURL should be the
// httptest server address without a scheme (e.g. "127.0.0.1:PORT"). The
// provided httpClient can carry a custom transport that routes HTTPS requests
// to the plain-HTTP test server.
func NewForTest(httpClient *http.Client, baseURL string, logger *zap.Logger) *Client {
	return newClient(httpClient, baseURL, logger, 0, 0)
}

// sanitizeURL strips sensitive query parameters (like "key") from a URL for
// safe logging. Returns the URL path and any non-sensitive query params.
func sanitizeURL(u *url.URL) string {
	if u == nil {
		return "<nil>"
	}
	clean := *u
	q := clean.Query()
	if q.Has("key") {
		q.Set("key", "REDACTED")
	}
	clean.RawQuery = q.Encode()
	return clean.String()
}

// isDDoSGuard peeks at the first ddosGuardSniffLimit bytes of resp.Body and
// reports whether the DDoS-Guard marker is present. The body is re-assembled
// afterwards so callers still see the full content and can close it normally.
func isDDoSGuard(resp *http.Response) bool {
	orig := resp.Body
	sniffed, _ := io.ReadAll(io.LimitReader(orig, ddosGuardSniffLimit))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(sniffed), orig), orig}

	return hasDDoSGuardMarker(sniffed)
}

func hasDDoSGuardMarker(body []byte) bool {
	return bytes.Contains(bytes.ToLower(body), []byte(ddosGuardMarker))
}

// NewStatusError builds a StatusError for a non-OK response whose body (or a
// prefix of it) has already been read. Only the first ddosGuardSniffLimit
// bytes are inspected for the DDoS-Guard marker.
func NewStatusError(operation, path string, statusCode int, body []byte) *StatusError {
	if len(body) > ddosGuardSniffLimit {
		body = body[:ddosGuardSniffLimit]
	}
	return &StatusError{
		Operation:  operation,
		Path:       path,
		StatusCode: statusCode,
		DDoSGuard:  hasDDoSGuardMarker(body),
	}
}

// StatusErrorFromResponse builds a StatusError from a non-OK response. It may
// read a small prefix of the body to detect DDoS-Guard challenges.
func StatusErrorFromResponse(operation, path string, resp *http.Response) error {
	var sniffed []byte
	if resp.Body != nil {
		sniffed, _ = io.ReadAll(io.LimitReader(resp.Body, ddosGuardSniffLimit))
	}
	statusErr := NewStatusError(operation, path, resp.StatusCode, sniffed)
	statusErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	return statusErr
}
