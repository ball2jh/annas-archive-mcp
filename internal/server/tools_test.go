package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestApplySearchDefaults(t *testing.T) {
	tests := []struct {
		name            string
		input           SearchInput
		wantContentType string
		wantLimit       int
	}{
		{
			name:            "empty defaults",
			input:           SearchInput{Query: "go"},
			wantContentType: "book_any",
			wantLimit:       5,
		},
		{
			name:            "explicit values preserved",
			input:           SearchInput{Query: "go", ContentType: "journal", Limit: 10},
			wantContentType: "journal",
			wantLimit:       10,
		},
		{
			name:            "limit clamped to max",
			input:           SearchInput{Query: "go", Limit: 50},
			wantContentType: "book_any",
			wantLimit:       20,
		},
		{
			name:            "negative limit gets default",
			input:           SearchInput{Query: "go", Limit: -1},
			wantContentType: "book_any",
			wantLimit:       5,
		},
		{
			name:            "zero limit gets default",
			input:           SearchInput{Query: "go", Limit: 0},
			wantContentType: "book_any",
			wantLimit:       5,
		},
		{
			name:            "limit at max boundary",
			input:           SearchInput{Query: "go", Limit: 20},
			wantContentType: "book_any",
			wantLimit:       20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applySearchDefaults(&tt.input)
			if tt.input.ContentType != tt.wantContentType {
				t.Errorf("ContentType = %q, want %q", tt.input.ContentType, tt.wantContentType)
			}
			if tt.input.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", tt.input.Limit, tt.wantLimit)
			}
		})
	}
}

func TestFormatSearchResults_Empty(t *testing.T) {
	got := formatSearchResults(nil)
	if got != "No results found." {
		t.Errorf("got %q, want %q", got, "No results found.")
	}
}

func TestFormatSearchResults(t *testing.T) {
	results := []model.SearchResult{
		{
			Title:    "Python Programming for Beginners",
			Authors:  []string{"Publishing", "AMZ"},
			Format:   "EPUB",
			Size:     "12.0MB",
			Language: "English",
			Hash:     "f87448722f0072549206b63999ec39e1",
			Stats:    model.CommunityStats{Downloads: 3486, Lists: 3, Reports: 1},
		},
		{
			Title:    "Python Programming",
			Authors:  []string{"Adam Stewart"},
			Format:   "PDF",
			Size:     "5.2MB",
			Language: "English",
			Hash:     "abc123def456abc123def456abc12345",
			Stats:    model.CommunityStats{Downloads: 100, Lists: 0, Reports: 0},
		},
	}

	got := formatSearchResults(results)

	// Verify structure.
	if !strings.Contains(got, "Found 2 result(s):") {
		t.Errorf("missing header, got:\n%s", got)
	}
	if !strings.Contains(got, "1. Python Programming for Beginners") {
		t.Errorf("missing first result title, got:\n%s", got)
	}
	if !strings.Contains(got, "2. Python Programming") {
		t.Errorf("missing second result title, got:\n%s", got)
	}
	if !strings.Contains(got, "Authors: Publishing, AMZ") {
		t.Errorf("missing authors, got:\n%s", got)
	}
	if !strings.Contains(got, "EPUB · 12.0MB") {
		t.Errorf("missing format info, got:\n%s", got)
	}
	if !strings.Contains(got, "3,486") {
		t.Errorf("missing formatted download count, got:\n%s", got)
	}
}

func TestFormatDetails(t *testing.T) {
	d := &model.BookDetails{
		Title:       "Test Book",
		Authors:     []string{"Author A", "Author B"},
		Publisher:   "Test Publisher",
		Year:        "2023",
		Format:      "PDF",
		Size:        "10MB",
		Language:    "English",
		Hash:        "abc123",
		ISBN:        "978-1234567890",
		DOI:         "10.1234/test",
		ISSN:        "",
		Description: "A test book.",
		Stats:       model.CommunityStats{Downloads: 1000, Lists: 5},
	}

	got := formatDetails(d)

	checks := []string{
		"Title: Test Book",
		"Authors: Author A, Author B",
		"Publisher: Test Publisher",
		"Year: 2023",
		"Format: PDF · 10MB",
		"ISBN: 978-1234567890",
		"DOI: 10.1234/test",
		"ISSN: —",
		"Description: A test book.",
		"1,000 · Lists: 5",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestFormatDOIResult(t *testing.T) {
	r := &model.DOIResult{
		Title:   "A Great Paper",
		Authors: []string{"Smith", "Jones"},
		Journal: "Nature",
		Year:    "2020",
		DOI:     "10.1038/nature12373",
		Hash:    "abc123def456",
	}

	got := formatDOIResult(r)

	checks := []string{
		"Title: A Great Paper",
		"Authors: Smith, Jones",
		"Journal: Nature",
		"Year: 2020",
		"DOI: 10.1038/nature12373",
		"Hash: abc123def456",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1,000"},
		{3486, "3,486"},
		{1000000, "1,000,000"},
		{12345678, "12,345,678"},
	}
	for _, tt := range tests {
		got := formatCount(tt.n)
		if got != tt.want {
			t.Errorf("formatCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatFileInfo(t *testing.T) {
	tests := []struct {
		format, size, want string
	}{
		{"PDF", "10MB", "PDF · 10MB"},
		{"EPUB", "", "EPUB"},
		{"", "5MB", "5MB"},
		{"", "", "—"},
	}
	for _, tt := range tests {
		got := formatFileInfo(tt.format, tt.size)
		if got != tt.want {
			t.Errorf("formatFileInfo(%q, %q) = %q, want %q", tt.format, tt.size, got, tt.want)
		}
	}
}

func TestValueOr(t *testing.T) {
	if got := valueOr("hello", "—"); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if got := valueOr("", "—"); got != "—" {
		t.Errorf("got %q, want %q", got, "—")
	}
}

// ---------------------------------------------------------------------------
// Error boundary tests
// ---------------------------------------------------------------------------

// newErrorBoundaryClient creates a Client wired to a TLS test server, matching
// the pattern used throughout the test suite.
func newErrorBoundaryClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	hc := &http.Client{
		Timeout:   2 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), zaptest.NewLogger(t))
}

// TestSearchHandlerErrorBoundary verifies that when the upstream server returns
// 500, the handler returns [SEARCH_FAILED] and does NOT leak the server URL or
// status code to the caller.
func TestSearchHandlerErrorBoundary(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	client := newErrorBoundaryClient(t, srv)
	logger := zaptest.NewLogger(t)

	handler := searchHandler(cfg, client, logger)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, SearchInput{Query: "python"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[SEARCH_FAILED]") {
		t.Errorf("error does not contain [SEARCH_FAILED]: %q", msg)
	}
	// Internal details must not leak to the caller.
	if strings.Contains(msg, srv.URL) {
		t.Errorf("error leaks server URL: %q", msg)
	}
	if strings.Contains(msg, "500") {
		t.Errorf("error leaks status code: %q", msg)
	}
}

// TestDownloadHandlerMissingKey verifies that calling the download handler
// without a configured SecretKey returns [AUTH_REQUIRED].
func TestDownloadHandlerMissingKey(t *testing.T) {
	// No HTTP server needed — the handler fails on config validation before any
	// network call is made.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request — should have failed before making any request")
	}))
	defer srv.Close()

	cfg := &config.Config{SecretKey: "", DownloadPath: "/tmp"}
	client := newErrorBoundaryClient(t, srv)
	logger := zaptest.NewLogger(t)

	handler := downloadHandler(cfg, client, logger)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, DownloadInput{Hash: "abc", Title: "test", Format: "pdf"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "[AUTH_REQUIRED]") {
		t.Errorf("error does not contain [AUTH_REQUIRED]: %q", err.Error())
	}
}

// TestDownloadHandlerMissingPath verifies that calling the download handler
// with a SecretKey but no DownloadPath returns [PATH_REQUIRED].
func TestDownloadHandlerMissingPath(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request — should have failed before making any request")
	}))
	defer srv.Close()

	cfg := &config.Config{SecretKey: "secret", DownloadPath: ""}
	client := newErrorBoundaryClient(t, srv)
	logger := zaptest.NewLogger(t)

	handler := downloadHandler(cfg, client, logger)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, DownloadInput{Hash: "abc", Title: "test", Format: "pdf"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "[PATH_REQUIRED]") {
		t.Errorf("error does not contain [PATH_REQUIRED]: %q", err.Error())
	}
}

// TestDOIHandlerInvalidDOI verifies that passing a non-DOI string returns
// [INVALID_DOI] without making any network requests.
func TestDOIHandlerInvalidDOI(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request — should have failed before making any request")
	}))
	defer srv.Close()

	cfg := &config.Config{}
	client := newErrorBoundaryClient(t, srv)
	logger := zaptest.NewLogger(t)

	handler := doiHandler(cfg, client, logger)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, DOIInput{DOI: "not-a-doi"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "[INVALID_DOI]") {
		t.Errorf("error does not contain [INVALID_DOI]: %q", err.Error())
	}
}

// TestDetailsHandlerInvalidHash verifies that passing a short hash string
// returns [INVALID_HASH] without making any network requests.
func TestDetailsHandlerInvalidHash(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request — should have failed before making any request")
	}))
	defer srv.Close()

	cfg := &config.Config{}
	client := newErrorBoundaryClient(t, srv)
	logger := zaptest.NewLogger(t)

	handler := detailsHandler(cfg, client, logger)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, DetailsInput{Hash: "xyz"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "[INVALID_HASH]") {
		t.Errorf("error does not contain [INVALID_HASH]: %q", err.Error())
	}
}
