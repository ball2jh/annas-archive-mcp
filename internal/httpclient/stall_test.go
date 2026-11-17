package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
)

// newStallClient returns a Client whose stall timeout is short enough to test.
func newStallClient(t *testing.T, srv *httptest.Server, stall time.Duration) *Client {
	t.Helper()
	c := New(&config.Config{BaseURL: srv.Listener.Addr().String(), HTTPTimeout: stall}, zaptest.NewLogger(t))
	c.http.Transport = srv.Client().Transport
	return c
}

// A transfer that takes several times the timeout must succeed as long as
// bytes keep arriving: the timeout bounds stalls, not total duration.
func TestSlowButSteadyBodyIsNotTimedOut(t *testing.T) {
	const stall = 150 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 8 {
			_, _ = io.WriteString(w, "chunk")
			w.(http.Flusher).Flush()
			time.Sleep(stall / 3)
		}
	}))
	defer srv.Close()

	resp, err := newStallClient(t, srv, stall).Get(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if want := strings.Repeat("chunk", 8); string(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestStalledBodyTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "start")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	resp, err := newStallClient(t, srv, 100*time.Millisecond).Get(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read error = %v, want one wrapping context.DeadlineExceeded", err)
	}
}

func TestStalledHeadersTimeOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	_, err := newStallClient(t, srv, 100*time.Millisecond).Get(context.Background(), srv.URL, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want one wrapping context.DeadlineExceeded", err)
	}
}

// Transport errors embed the request URL; the secret key must not survive.
func TestTransportErrorRedactsSecretKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/dyn/api/fast_download.json?md5=abc&key=TOPSECRET"
	srv.Close() // connection refused from here on

	c := newStallClient(t, srv, time.Second)
	_, err := c.Get(context.Background(), url, nil)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Errorf("error leaks the secret key: %v", err)
	}
	if !strings.Contains(err.Error(), "md5=abc") {
		t.Errorf("error should keep non-sensitive parameters: %v", err)
	}
}

// Sniffing a 403 for DDoS-Guard must leave the whole body readable.
func TestForbiddenBodySurvivesSniff(t *testing.T) {
	want := strings.Repeat("x", 3*ddosGuardSniffLimit)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, want)
	}))
	defer srv.Close()

	resp, err := newStallClient(t, srv, time.Second).Get(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != want {
		t.Errorf("body length = %d, want %d", len(got), len(want))
	}
}

func TestCancelledContextIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := New(&config.Config{HTTPTimeout: time.Minute, MaxRetries: 3}, zaptest.NewLogger(t))
	c.http.Transport = srv.Client().Transport

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Get(ctx, srv.URL, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server saw %d request(s), want 1", n)
	}
}

// A host that keeps stalling must not consume the whole retry budget: each
// stalled attempt costs the full timeout.
func TestRepeatedStallsStopRetrying(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := New(&config.Config{BaseURL: srv.Listener.Addr().String(), HTTPTimeout: 100 * time.Millisecond, MaxRetries: 5}, zaptest.NewLogger(t))
	c.http.Transport = srv.Client().Transport

	_, err := c.Get(context.Background(), srv.URL, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want one wrapping context.DeadlineExceeded", err)
	}
	if n := calls.Load(); n != maxStalledAttempts {
		t.Errorf("made %d attempt(s), want %d", n, maxStalledAttempts)
	}
}
