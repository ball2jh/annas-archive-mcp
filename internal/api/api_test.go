package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
)

// newAPITestClient returns an httpclient.Client wired to the given TLS test
// server. The client's baseURL is set to the server's address so GetJSON
// builds https://<addr>+path and the custom TLS transport routes it correctly.
func newAPITestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	logger := zaptest.NewLogger(t)
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), logger)
}

// ---------------------------------------------------------------------------
// TestFetchStats
// ---------------------------------------------------------------------------

func TestFetchStats(t *testing.T) {
	const hash = "abc123"

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/dyn/md5/inline_info/" + hash
		if r.URL.Path != want {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Accept") != "text/css" {
			http.Error(w, "missing Accept header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"reports_count": 1,
			"comments_count": 2,
			"lists_count": 3,
			"downloads_total": 3486,
			"great_quality_count": 5
		}`)
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	stats, err := FetchStats(context.Background(), client, hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := model.CommunityStats{
		Downloads: 3486,
		Lists:     3,
		Quality:   "5",
		Comments:  2,
		Reports:   1,
	}
	if stats != want {
		t.Errorf("stats mismatch:\n  got  %+v\n  want %+v", stats, want)
	}
}

// ---------------------------------------------------------------------------
// TestFetchStatsParallel
// ---------------------------------------------------------------------------

func TestFetchStatsParallel(t *testing.T) {
	hashes := []string{"aaa", "bbb", "ccc"}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Each hash gets a unique downloads_total so we can verify correct routing.
		counts := map[string]int{
			"/dyn/md5/inline_info/aaa": 100,
			"/dyn/md5/inline_info/bbb": 200,
			"/dyn/md5/inline_info/ccc": 300,
		}
		n, ok := counts[r.URL.Path]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"downloads_total": %d, "lists_count": 0, "comments_count": 0, "reports_count": 0, "great_quality_count": 0}`, n)
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	logger := zaptest.NewLogger(t)
	results := FetchStatsParallel(context.Background(), client, hashes, 3, logger)

	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	expected := map[string]int{"aaa": 100, "bbb": 200, "ccc": 300}
	for hash, wantDL := range expected {
		got, ok := results[hash]
		if !ok {
			t.Errorf("missing result for hash %q", hash)
			continue
		}
		if got.Downloads != wantDL {
			t.Errorf("hash %q: want Downloads=%d, got %d", hash, wantDL, got.Downloads)
		}
	}
}

// ---------------------------------------------------------------------------
// TestFetchStatsError
// ---------------------------------------------------------------------------

func TestFetchStatsError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	_, err := FetchStats(context.Background(), client, "deadbeef")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

// TestFetchStatsParallelPartialFailure verifies that failures are silently
// skipped and successful hashes are still returned.
func TestFetchStatsParallelPartialFailure(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Only "good" succeeds; "bad" returns 500.
		if r.URL.Path == "/dyn/md5/inline_info/good" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"downloads_total": 42, "lists_count": 0, "comments_count": 0, "reports_count": 0, "great_quality_count": 0}`)
			return
		}
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	logger := zaptest.NewLogger(t)
	results := FetchStatsParallel(context.Background(), client, []string{"good", "bad"}, 2, logger)

	if _, ok := results["good"]; !ok {
		t.Error("expected result for hash 'good'")
	}
	if _, ok := results["bad"]; ok {
		t.Error("expected 'bad' to be absent from results (it errored)")
	}
}

// ---------------------------------------------------------------------------
// TestResolveDownloadURL
// ---------------------------------------------------------------------------

func TestResolveDownloadURL(t *testing.T) {
	const (
		hash      = "deadbeef"
		secretKey = "mysecret"
		wantURL   = "https://cdn.example.com/files/book.epub"
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("md5") != hash || q.Get("key") != secretKey {
			http.Error(w, "bad params", http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"download_url": wantURL,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "encode error", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	got, err := ResolveDownloadURL(context.Background(), client, hash, secretKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantURL {
		t.Errorf("want URL %q, got %q", wantURL, got)
	}
}

// ---------------------------------------------------------------------------
// TestResolveDownloadURLError
// ---------------------------------------------------------------------------

func TestResolveDownloadURLError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return the API error shape for all domain_index values.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"download_url": null, "error": "Invalid secret key"}`)
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	_, err := ResolveDownloadURL(context.Background(), client, "abc", "badkey")
	if err == nil {
		t.Fatal("expected error for API error response, got nil")
	}
	// The original API error message must be preserved somewhere in the chain.
	const wantMsg = "Invalid secret key"
	if !containsString(err.Error(), wantMsg) {
		t.Errorf("error %q does not contain %q", err.Error(), wantMsg)
	}
}

// ---------------------------------------------------------------------------
// TestResolveDownloadURLFallback
// ---------------------------------------------------------------------------

func TestResolveDownloadURLFallback(t *testing.T) {
	const wantURL = "https://cdn2.example.com/files/book.epub"

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domainIdx := r.URL.Query().Get("domain_index")
		w.Header().Set("Content-Type", "application/json")

		switch domainIdx {
		case "0":
			// First index fails with an API error.
			fmt.Fprintln(w, `{"download_url": null, "error": "Server unavailable"}`)
		case "1":
			// Second index succeeds.
			resp := map[string]interface{}{"download_url": wantURL}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			fmt.Fprintln(w, `{"download_url": null, "error": "unknown index"}`)
		}
	}))
	defer srv.Close()

	client := newAPITestClient(t, srv)
	got, err := ResolveDownloadURL(context.Background(), client, "deadbeef", "key123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantURL {
		t.Errorf("want URL %q, got %q", wantURL, got)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
