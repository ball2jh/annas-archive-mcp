package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestSearchHandlerReturnsStructuredOutput(t *testing.T) {
	const hash = "f87448722f0072549206b63999ec39e1"
	htmlBody, err := os.ReadFile("../../testdata/search_results.html")
	if err != nil {
		t.Fatalf("read search fixture: %v", err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(htmlBody)
		case r.URL.Path == "/dyn/md5/inline_info/"+hash:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"downloads_total": 7, "lists_count": 1, "comments_count": 0, "reports_count": 0, "great_quality_count": 0}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{}
	client := newErrorBoundaryClient(t, srv)
	handler := searchHandler(cfg, client, zaptest.NewLogger(t))

	res, out, err := handler(context.Background(), &mcp.CallToolRequest{}, SearchInput{Query: "example", Limit: 1})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected text content")
	}
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1", out.Count)
	}
	if out.Results[0].Hash != hash {
		t.Fatalf("Hash = %q, want %q", out.Results[0].Hash, hash)
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
// 500, the handler returns a classified public error and does NOT leak the
// server URL or status code to the caller.
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
	if !strings.Contains(msg, "[UPSTREAM_UNAVAILABLE]") {
		t.Errorf("error does not contain [UPSTREAM_UNAVAILABLE]: %q", msg)
	}
	// Internal details must not leak to the caller.
	if strings.Contains(msg, srv.URL) {
		t.Errorf("error leaks server URL: %q", msg)
	}
	if strings.Contains(msg, "500") {
		t.Errorf("error leaks status code: %q", msg)
	}
}

func TestToolErrorClassifiesCommonFailures(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		err       error
		wantCode  string
	}{
		{
			name:      "ddos guard",
			operation: "search",
			err:       &httpclient.StatusError{StatusCode: http.StatusForbidden, DDoSGuard: true},
			wantCode:  "[UPSTREAM_BLOCKED]",
		},
		{
			name:      "rate limit",
			operation: "search",
			err:       &httpclient.StatusError{StatusCode: http.StatusTooManyRequests},
			wantCode:  "[RATE_LIMITED]",
		},
		{
			name:      "details not found",
			operation: "details",
			err:       &httpclient.StatusError{StatusCode: http.StatusNotFound},
			wantCode:  "[NOT_FOUND]",
		},
		{
			name:      "server unavailable",
			operation: "search",
			err:       &httpclient.StatusError{StatusCode: http.StatusBadGateway},
			wantCode:  "[UPSTREAM_UNAVAILABLE]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolError(tt.operation, tt.err).Error()
			if !strings.Contains(got, tt.wantCode) {
				t.Fatalf("toolError() = %q, want code %s", got, tt.wantCode)
			}
		})
	}
}

func TestToolRateLimit(t *testing.T) {
	cfg := &config.Config{ToolRateLimitPerMinute: 1}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid DOI should not make network requests")
	}))
	defer srv.Close()

	handler := doiHandler(cfg, newErrorBoundaryClient(t, srv), zaptest.NewLogger(t))

	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, DOIInput{DOI: "bad"})
	if err == nil || !strings.Contains(err.Error(), "[INVALID_DOI]") {
		t.Fatalf("first call error = %v, want [INVALID_DOI]", err)
	}

	_, _, err = handler(context.Background(), &mcp.CallToolRequest{}, DOIInput{DOI: "bad"})
	if err == nil || !strings.Contains(err.Error(), "[LOCAL_RATE_LIMITED]") {
		t.Fatalf("second call error = %v, want [LOCAL_RATE_LIMITED]", err)
	}
}

func TestToolAnnotations(t *testing.T) {
	srv := New(&config.Config{}, nil, zaptest.NewLogger(t))
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := srv.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	tools, err := clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	byName := make(map[string]*mcp.Tool, len(tools.Tools))
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}

	for _, name := range []string{"search", "lookup_doi", "get_details"} {
		tool := byName[name]
		if tool == nil {
			t.Fatalf("missing tool %q", name)
		}
		if tool.Title == "" {
			t.Fatalf("%s has empty title", name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s should be annotated read-only", name)
		}
		if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
			t.Fatalf("%s should be annotated open-world", name)
		}
		if tool.OutputSchema == nil {
			t.Fatalf("%s should expose an output schema", name)
		}
	}

	for _, name := range []string{"download", "download_by_doi"} {
		tool := byName[name]
		if tool == nil {
			t.Fatalf("missing tool %q", name)
		}
		if tool.Title == "" {
			t.Fatalf("%s has empty title", name)
		}
		if tool.Annotations == nil {
			t.Fatalf("%s missing annotations", name)
		}
		if tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s should not be read-only", name)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("%s should be annotated non-destructive/additive", name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Fatalf("%s should be annotated idempotent", name)
		}
		if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
			t.Fatalf("%s should be annotated open-world", name)
		}
		if tool.OutputSchema == nil {
			t.Fatalf("%s should expose an output schema", name)
		}
	}
}

// TestDownloadHandlerMissingKey verifies that when ANNAS_SECRET_KEY is empty
// AND the libgen.li fallback is disabled, the handler returns [AUTH_REQUIRED].
// Uses a valid 32-char hex hash so we reach the key check rather than fail at
// input validation.
func TestDownloadHandlerMissingKey(t *testing.T) {
	// No HTTP server needed — the handler fails before any network call.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request — should have failed before making any request")
	}))
	defer srv.Close()

	cfg := &config.Config{
		SecretKey:     "",
		DownloadPath:  "/tmp",
		LibgenEnabled: false, // opt out of fallback so the missing key is fatal
	}
	client := newErrorBoundaryClient(t, srv)
	logger := zaptest.NewLogger(t)

	handler := downloadHandler(cfg, client, logger)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{},
		DownloadInput{Hash: "0123456789abcdef0123456789abcdef", Title: "test", Format: "pdf"})

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
