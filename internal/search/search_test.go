package search

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
	"github.com/ball2jh/annas-archive-mcp/internal/model"
)

// newSearchTestClient returns a Client wired to the given TLS test server.
func newSearchTestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	logger := zaptest.NewLogger(t)
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), logger)
}

// loadSearchHTML reads the shared search results testdata fixture.
func loadSearchHTML(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/search_results.html")
	if err != nil {
		t.Fatalf("reading search_results.html: %v", err)
	}
	return data
}

// inlineInfoJSON returns minimal JSON for the inline_info endpoint.
func inlineInfoJSON(downloads int) []byte {
	v := map[string]int{
		"downloads_total":     downloads,
		"lists_count":         1,
		"comments_count":      0,
		"reports_count":       0,
		"great_quality_count": 2,
	}
	b, _ := json.Marshal(v)
	return b
}

// ---------------------------------------------------------------------------
// TestSearchSuccess
// ---------------------------------------------------------------------------

// TestSearchSuccess verifies the full happy path: the server receives the
// correct search request, returns search HTML, and then individual
// inline_info calls are made for each result hash.
func TestSearchSuccess(t *testing.T) {
	htmlBody := loadSearchHTML(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case path == "/search":
			// Verify required query params are present.
			q := r.URL.Query()
			if q.Get("q") == "" {
				http.Error(w, "missing q param", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(htmlBody)

		case strings.HasPrefix(path, "/dyn/md5/inline_info/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(inlineInfoJSON(100))

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newSearchTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	results, err := Search(context.Background(), client, logger, "python programming", model.ContentTypeBookAny, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result, got none")
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results (limit), got %d", len(results))
	}

	// Every returned result should have a hash and stats populated.
	for i, r := range results {
		if r.Hash == "" {
			t.Errorf("result[%d] has empty hash", i)
		}
		if r.Title == "" {
			t.Errorf("result[%d] has empty title", i)
		}
		if r.Stats.Downloads == 0 {
			t.Errorf("result[%d] stats not enriched (downloads=0)", i)
		}
	}
}

// ---------------------------------------------------------------------------
// TestSearchInvalidContentType
// ---------------------------------------------------------------------------

// TestSearchInvalidContentType verifies that an unrecognised content type
// returns an error immediately, without making any HTTP requests.
func TestSearchInvalidContentType(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request — should have failed before making any request")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newSearchTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	_, err := Search(context.Background(), client, logger, "python", model.ContentType("invalid_type"), 5)
	if err == nil {
		t.Fatal("expected error for invalid content type, got nil")
	}
	if !strings.Contains(err.Error(), "[INVALID_CONTENT_TYPE]") {
		t.Errorf("error %q does not contain [INVALID_CONTENT_TYPE]", err.Error())
	}
}

// ---------------------------------------------------------------------------
// TestSearchEmptyResults
// ---------------------------------------------------------------------------

// TestSearchEmptyResults verifies that when the search page contains no result
// items, Search returns nil without error.
func TestSearchEmptyResults(t *testing.T) {
	const emptySearchHTML = `<!DOCTYPE html>
<html><head><title>Anna's Archive</title></head>
<body><div id="search-results"></div></body></html>`

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/search" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(emptySearchHTML))
			return
		}
		// inline_info should not be called when there are no results.
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.Error(w, "unexpected", http.StatusNotFound)
	}))
	defer srv.Close()

	client := newSearchTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	results, err := Search(context.Background(), client, logger, "xyzzy not found", model.ContentTypeBookAny, 5)
	if err != nil {
		t.Fatalf("unexpected error for empty results: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty page, got %v", results)
	}
}

// ---------------------------------------------------------------------------
// TestBuildSearchPath
// ---------------------------------------------------------------------------

// TestBuildSearchPath tests the URL path construction logic in isolation.
func TestBuildSearchPath(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		contentType model.ContentType
		limit       int
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "book_any omits content param",
			query:       "python",
			contentType: model.ContentTypeBookAny,
			limit:       5,
			wantContain: []string{"/search?", "q=python", "limit=5"},
			wantAbsent:  []string{"content="},
		},
		{
			name:        "book_fiction includes content param",
			query:       "harry potter",
			contentType: model.ContentTypeBookFiction,
			limit:       10,
			wantContain: []string{"/search?", "content=book_fiction", "limit=10"},
		},
		{
			name:        "journal uses journal_article segment",
			query:       "quantum",
			contentType: model.ContentTypeJournal,
			limit:       3,
			wantContain: []string{"content=journal_article"},
		},
		{
			name:        "query is URL-encoded",
			query:       "c++ programming",
			contentType: model.ContentTypeBookAny,
			limit:       5,
			wantContain: []string{"q=c"},
			wantAbsent:  []string{"q=c++ programming"}, // must be encoded
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := buildSearchPath(tc.query, tc.contentType, tc.limit)
			for _, want := range tc.wantContain {
				if !strings.Contains(path, want) {
					t.Errorf("path %q does not contain %q", path, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(path, absent) {
					t.Errorf("path %q should not contain %q", path, absent)
				}
			}
		})
	}
}
