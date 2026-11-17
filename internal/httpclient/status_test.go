package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// newStatusClient returns a Client pointed at a TLS test server, with retries.
func newStatusClient(t *testing.T, srv *httptest.Server, retries int) *Client {
	t.Helper()
	c := New(&config.Config{BaseURL: srv.Listener.Addr().String(), MaxRetries: retries}, zaptest.NewLogger(t))
	c.http.Transport = srv.Client().Transport
	return c
}

// A Retry-After longer than a tool call should reasonably block for must be
// reported to the caller, not slept through.
func TestLongRetryAfterIsReportedNotWaited(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	start := time.Now()
	_, err := newStatusClient(t, srv, 3).GetJSON(context.Background(), "/x", nil)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s; the client waited instead of reporting", elapsed)
	}

	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *StatusError", err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests || statusErr.RetryAfter != 600*time.Second {
		t.Errorf("got status %d retry-after %s, want 429 and 10m", statusErr.StatusCode, statusErr.RetryAfter)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("made %d request(s), want 1", n)
	}
}

// A burst of parallel calls must not reach the upstream all at once.
func TestUpstreamRequestsAreCapped(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()

	client := newStatusClient(t, srv, 0)
	var wg sync.WaitGroup
	for i := range 5 * maxUpstreamInFlight {
		wg.Go(func() {
			if _, err := client.GetJSON(context.Background(), fmt.Sprintf("/x?i=%d", i), nil); err != nil {
				t.Errorf("request %d: %v", i, err)
			}
		})
	}
	wg.Wait()

	if p := peak.Load(); p > maxUpstreamInFlight {
		t.Errorf("peak concurrent requests = %d, want at most %d", p, maxUpstreamInFlight)
	}
}

// Waiting for a slot must give way to cancellation.
func TestWaitingForSlotHonoursCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	client := newStatusClient(t, srv, 0)
	for range maxUpstreamInFlight {
		go func() { _, _ = client.GetJSON(context.Background(), "/hold", nil) }()
	}
	// Let the holders take every slot.
	deadline := time.Now().Add(2 * time.Second)
	for len(client.upstreamSlots) < maxUpstreamInFlight {
		if time.Now().After(deadline) {
			t.Fatal("slots were never filled")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.GetJSON(ctx, "/blocked", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

// A challenge page served with a 200 must surface as a challenge, not be
// scraped as an empty page.
func TestChallengePageWith200IsNotScraped(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><head><title>DDoS-Guard</title></head><body>checking</body></html>")
	}))
	defer srv.Close()

	_, err := newStatusClient(t, srv, 0).GetHTML(context.Background(), "/search?q=a")
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || !statusErr.DDoSGuard {
		t.Fatalf("error = %v, want a DDoS-Guard *StatusError", err)
	}
}

func TestSourceErrorNamesTheSource(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string // "" = returned unchanged
	}{
		{"rate limit", &StatusError{StatusCode: http.StatusTooManyRequests}, usererror.CodeRateLimited},
		{"outage", fmt.Errorf("wrapped: %w", &StatusError{StatusCode: http.StatusBadGateway}), usererror.CodeUpstreamUnavailable},
		{"other status", &StatusError{StatusCode: http.StatusNotFound}, ""},
		{"challenge", &StatusError{StatusCode: http.StatusForbidden, DDoSGuard: true}, ""},
		{"not a status", context.DeadlineExceeded, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SourceError("Libgen", tc.err)
			if tc.wantCode == "" {
				if got != tc.err {
					t.Fatalf("got %v, want the error unchanged", got)
				}
				return
			}
			if !usererror.HasCode(got, tc.wantCode) {
				t.Fatalf("got %v, want code %s", got, tc.wantCode)
			}
			var ue *usererror.Error
			errors.As(got, &ue)
			if !strings.HasPrefix(ue.Message, "Libgen ") {
				t.Errorf("message %q does not name the source", ue.Message)
			}
			// The cause stays reachable for logs and classification.
			var statusErr *StatusError
			if !errors.As(got, &statusErr) {
				t.Error("the StatusError cause was lost")
			}
		})
	}
}
