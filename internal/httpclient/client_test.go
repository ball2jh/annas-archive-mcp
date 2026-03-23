package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
)

// newTestClient returns a Client wired to the given test server with short
// timeouts and the supplied maxRetries. baseURL is set to the test server's
// host so GetHTML/GetJSON helpers also work.
func newTestClient(t *testing.T, srv *httptest.Server, maxRetries int) *Client {
	t.Helper()
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		BaseURL:     srv.Listener.Addr().String(),
		HTTPTimeout: 2 * time.Second,
		MaxRetries:  maxRetries,
	}
	c := New(cfg, logger)
	// Point the transport at the test server so the plain http:// scheme works.
	c.http.Transport = srv.Client().Transport
	return c
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// countingHandler returns an http.HandlerFunc that responds with statusCode on
// each call and atomically increments *calls.
func countingHandler(statusCode int, body string, calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if body != "" {
			w.Header().Set("Content-Type", "text/html")
		}
		w.WriteHeader(statusCode)
		if body != "" {
			fmt.Fprint(w, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSuccessfulRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(countingHandler(http.StatusOK, "OK", &calls))
	defer srv.Close()

	c := newTestClient(t, srv, 3)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Errorf("want exactly 1 call, got %d", calls.Load())
	}
}

func TestRetryOn500(t *testing.T) {
	const maxRetries = 3
	var calls atomic.Int32
	srv := httptest.NewServer(countingHandler(http.StatusInternalServerError, "", &calls))
	defer srv.Close()

	c := newTestClient(t, srv, maxRetries)
	// Override backoff to be instant so the test runs fast.
	overrideBackoffForTest(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
	_, err := c.Do(context.Background(), req)
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}

	// 1 initial attempt + maxRetries retries = maxRetries+1 total calls.
	want := int32(maxRetries + 1)
	if calls.Load() != want {
		t.Errorf("want %d calls, got %d", want, calls.Load())
	}
}

func TestNoRetryOn400(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(countingHandler(http.StatusBadRequest, "", &calls))
	defer srv.Close()

	c := newTestClient(t, srv, 3)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Errorf("400 should not be retried; want 1 call, got %d", calls.Load())
	}
}

func TestRetryOn429(t *testing.T) {
	const maxRetries = 2
	var calls atomic.Int32
	srv := httptest.NewServer(countingHandler(http.StatusTooManyRequests, "", &calls))
	defer srv.Close()

	c := newTestClient(t, srv, maxRetries)
	overrideBackoffForTest(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
	_, err := c.Do(context.Background(), req)
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}

	want := int32(maxRetries + 1)
	if calls.Load() != want {
		t.Errorf("want %d calls, got %d", want, calls.Load())
	}
}

func TestDDoSGuardDetectionOn403(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(countingHandler(http.StatusForbidden, "Protected by DDoS-Guard", &calls))
	defer srv.Close()

	// Use an observed logger so we can assert the warning was emitted.
	obs, logs := newObservedLogger(t)
	cfg := &config.Config{
		BaseURL:     srv.Listener.Addr().String(),
		HTTPTimeout: 2 * time.Second,
		MaxRetries:  3,
	}
	c := New(cfg, obs)
	c.http.Transport = srv.Client().Transport

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	// 403 is not retriable — exactly one call.
	if calls.Load() != 1 {
		t.Errorf("403 should not be retried; want 1 call, got %d", calls.Load())
	}

	// The DDoS-Guard warning must have been logged.
	found := false
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "DDoS-Guard") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DDoS-Guard warning log, but none was found")
	}
}

func TestNoDDoSGuardWarningOn403WithoutMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Access denied")
	}))
	defer srv.Close()

	obs, logs := newObservedLogger(t)
	cfg := &config.Config{
		BaseURL:     srv.Listener.Addr().String(),
		HTTPTimeout: 2 * time.Second,
		MaxRetries:  3,
	}
	c := New(cfg, obs)
	c.http.Transport = srv.Client().Transport

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "DDoS-Guard") {
			t.Error("unexpected DDoS-Guard warning for a plain 403")
		}
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	var calls atomic.Int32
	// Use a 50ms backoff so the test doesn't take long, but long enough that
	// a context cancelled right after the first attempt fires during the wait.
	const testBackoff = 50 * time.Millisecond

	srv := httptest.NewServer(countingHandler(http.StatusInternalServerError, "", &calls))
	defer srv.Close()

	c := newTestClient(t, srv, 10) // high retry count

	// Set a backoff long enough that we can cancel before a second attempt.
	prev := backoffOverride
	backoffOverride = testBackoff
	t.Cleanup(func() { backoffOverride = prev })

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after the first request completes but during the first backoff wait.
	go func() {
		time.Sleep(5 * time.Millisecond) // after first attempt, before backoff expires
		cancel()
	}()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
	_, err := c.Do(ctx, req)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}

	// Only the first attempt should have run before the cancel took effect.
	if calls.Load() > 2 {
		t.Errorf("context cancel did not stop retries; got %d calls", calls.Load())
	}
}

func TestExponentialBackoffTiming(t *testing.T) {
	// Ensure no override is active for this test.
	prev := backoffOverride
	backoffOverride = 0
	t.Cleanup(func() { backoffOverride = prev })

	// Verify that backoffDuration grows exponentially.
	// We use controlled inputs and check relative ordering + lower bounds.
	// Because of jitter we can only assert >= base duration.
	for n, want := range []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	} {
		got := backoffDuration(n)
		if got < want {
			t.Errorf("backoffDuration(%d): got %v, want >= %v", n, got, want)
		}
		if got > want+jitterMax {
			t.Errorf("backoffDuration(%d): got %v, want <= %v", n, got, want+jitterMax)
		}
	}
}

func TestGetHelperSetsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, 0)
	resp, err := c.Get(context.Background(), srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if gotUA != userAgent {
		t.Errorf("want User-Agent %q, got %q", userAgent, gotUA)
	}
}

func TestGetHelperPassesExtraHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, 0)
	resp, err := c.Get(context.Background(), srv.URL+"/", map[string]string{"X-Custom": "test-value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if gotHeader != "test-value" {
		t.Errorf("want X-Custom: test-value, got %q", gotHeader)
	}
}

// ---------------------------------------------------------------------------
// Test infrastructure helpers
// ---------------------------------------------------------------------------

// overrideBackoffForTest replaces the effective backoff with a near-zero
// duration for the duration of the test and restores the original value via
// t.Cleanup.
func overrideBackoffForTest(t *testing.T) {
	t.Helper()
	prev := backoffOverride
	backoffOverride = 1 * time.Millisecond
	t.Cleanup(func() { backoffOverride = prev })
}

// newObservedLogger returns a zap.Logger and an observer so tests can inspect
// logged entries.
func newObservedLogger(t *testing.T) (*zap.Logger, *logObserver) {
	t.Helper()
	obs := &logObserver{}
	core := &observerCore{obs: obs, level: zap.WarnLevel}
	logger := zap.New(core)
	t.Cleanup(func() { _ = logger.Sync() })
	return logger, obs
}
