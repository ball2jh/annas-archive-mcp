package details

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
)

// newDetailsTestClient returns a Client wired to the given TLS test server.
func newDetailsTestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), zaptest.NewLogger(t))
}

// loadDetailHTML reads the shared detail page testdata fixture.
func loadDetailHTML(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/detail_page.html")
	if err != nil {
		t.Fatalf("reading detail_page.html: %v", err)
	}
	return data
}

// inlineInfoJSON returns minimal inline_info JSON for the given download count.
func inlineInfoJSON(downloads int) []byte {
	v := map[string]int{
		"downloads_total":     downloads,
		"lists_count":         3,
		"comments_count":      2,
		"reports_count":       1,
		"great_quality_count": 5,
	}
	b, _ := json.Marshal(v)
	return b
}

// ---------------------------------------------------------------------------
// TestGetDetailsSuccess
// ---------------------------------------------------------------------------

// TestGetDetailsSuccess exercises the full happy path: the mock server serves
// the detail page HTML at /md5/{hash} and stats JSON at /dyn/md5/inline_info/{hash}.
// All populated fields from the fixture are verified.
func TestGetDetailsSuccess(t *testing.T) {
	const hash = "f87448722f0072549206b63999ec39e1"
	htmlBody := loadDetailHTML(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/md5/"+hash:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(htmlBody)

		case path == "/dyn/md5/inline_info/"+hash:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(inlineInfoJSON(42))

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newDetailsTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	result, err := GetDetails(context.Background(), client, logger, hash)
	if err != nil {
		t.Fatalf("GetDetails: unexpected error: %v", err)
	}

	// Hash — must match input.
	if result.Hash != hash {
		t.Errorf("hash = %q, want %q", result.Hash, hash)
	}

	// Title — must contain the known fixture title.
	if !strings.Contains(result.Title, "Python Programming for Beginners") {
		t.Errorf("title = %q, expected to contain 'Python Programming for Beginners'", result.Title)
	}

	// Authors — fixture has "Publishing, AMZ".
	if len(result.Authors) == 0 {
		t.Fatal("no authors")
	}
	if result.Authors[0] != "Publishing, AMZ" {
		t.Errorf("authors[0] = %q, want 'Publishing, AMZ'", result.Authors[0])
	}

	// Metadata fields from the fixture metadata line.
	if result.Language != "English" {
		t.Errorf("language = %q, want 'English'", result.Language)
	}
	if result.Format != "EPUB" {
		t.Errorf("format = %q, want 'EPUB'", result.Format)
	}
	if result.Size != "12.0MB" {
		t.Errorf("size = %q, want '12.0MB'", result.Size)
	}
	if result.Year != "2021" {
		t.Errorf("year = %q, want '2021'", result.Year)
	}

	// Community stats — must reflect the JSON we served.
	if result.Stats.Downloads != 42 {
		t.Errorf("stats.downloads = %d, want 42", result.Stats.Downloads)
	}
	if result.Stats.Lists != 3 {
		t.Errorf("stats.lists = %d, want 3", result.Stats.Lists)
	}
	if result.Stats.Comments != 2 {
		t.Errorf("stats.comments = %d, want 2", result.Stats.Comments)
	}
	if result.Stats.Reports != 1 {
		t.Errorf("stats.reports = %d, want 1", result.Stats.Reports)
	}
	if result.Stats.Quality != "5" {
		t.Errorf("stats.quality = %q, want '5'", result.Stats.Quality)
	}
}

// ---------------------------------------------------------------------------
// TestGetDetailsInvalidHash
// ---------------------------------------------------------------------------

// TestGetDetailsInvalidHash verifies that malformed hashes are rejected before
// any HTTP request is made.
func TestGetDetailsInvalidHash(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"empty string", ""},
		{"too short", "short"},
		{"not hex", "not-hex-at-all!!!!!!!!!!!!!!!"},
		{"31 chars", "f87448722f0072549206b63999ec39e"},  // one char short
		{"33 chars", "f87448722f0072549206b63999ec39e1a"}, // one char over
		{"uppercase valid but wrong", "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"}, // 32 non-hex
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request for invalid hash test: %s", r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newDetailsTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := GetDetails(context.Background(), client, logger, tc.hash)
			if err == nil {
				t.Errorf("expected error for hash %q, got result: %+v", tc.hash, result)
			}
			if result != nil {
				t.Errorf("expected nil result for invalid hash %q, got: %+v", tc.hash, result)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGetDetails404
// ---------------------------------------------------------------------------

// TestGetDetails404 verifies that a 404 from the detail page endpoint
// propagates as an error.
func TestGetDetails404(t *testing.T) {
	const hash = "aaaabbbbccccdddd1111222233334444"

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := newDetailsTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	result, err := GetDetails(context.Background(), client, logger, hash)
	if err == nil {
		t.Fatalf("expected error for 404 response, got result: %+v", result)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not mention 404", err.Error())
	}
	if result != nil {
		t.Errorf("expected nil result on error, got: %+v", result)
	}
}
