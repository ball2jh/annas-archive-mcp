package doi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
)

// newDOITestClient returns a Client wired to the given TLS test server.
func newDOITestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), zaptest.NewLogger(t))
}

// loadSciDBHTML reads the shared SciDB testdata fixture.
func loadSciDBHTML(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/scidb_page.html")
	if err != nil {
		t.Fatalf("reading scidb_page.html: %v", err)
	}
	return data
}

// ---------------------------------------------------------------------------
// TestResolveSuccess
// ---------------------------------------------------------------------------

// TestResolveSuccess verifies the happy path: the server returns the SciDB page
// HTML and all fields are populated correctly.
func TestResolveSuccess(t *testing.T) {
	htmlBody := loadSciDBHTML(t)
	const doi = "10.1038/nature12373"

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/scidb/"+doi {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(htmlBody)
	}))
	defer srv.Close()

	client := newDOITestClient(t, srv)
	logger := zaptest.NewLogger(t)

	result, err := Resolve(context.Background(), client, logger, doi)
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}

	if result.Title != "Nanometre-scale thermometry in a living cell" {
		t.Errorf("title = %q, want 'Nanometre-scale thermometry in a living cell'", result.Title)
	}
	if result.DOI != doi {
		t.Errorf("doi = %q, want %q", result.DOI, doi)
	}
	if len(result.Authors) == 0 {
		t.Fatal("no authors")
	}
	if result.Authors[0] != "Kucsko, G." {
		t.Errorf("authors[0] = %q, want 'Kucsko, G.'", result.Authors[0])
	}
	if len(result.Authors) != 8 {
		t.Errorf("len(authors) = %d, want 8", len(result.Authors))
	}
	if result.Journal != "Nature" {
		t.Errorf("journal = %q, want 'Nature'", result.Journal)
	}
	if result.Year != "2013" {
		t.Errorf("year = %q, want '2013'", result.Year)
	}
	if result.Hash != "d89c394b00116f093b5d9d6a6611f975" {
		t.Errorf("hash = %q, want 'd89c394b00116f093b5d9d6a6611f975'", result.Hash)
	}
}

// ---------------------------------------------------------------------------
// TestResolveInvalidDOI
// ---------------------------------------------------------------------------

// TestResolveInvalidDOI verifies that malformed DOIs are rejected before any
// HTTP request is made.
func TestResolveInvalidDOI(t *testing.T) {
	cases := []struct {
		name string
		doi  string
	}{
		{"empty string", ""},
		{"not a doi", "not-a-doi"},
		{"missing slash", "10.1234"},
		{"only numbers", "1234567890"},
		{"wrong prefix", "20.1234/abc"},
	}

	// The server must never be reached for invalid DOIs.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request for invalid DOI test: %s", r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newDOITestClient(t, srv)
	logger := zaptest.NewLogger(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Resolve(context.Background(), client, logger, tc.doi)
			if err == nil {
				t.Errorf("expected error for DOI %q, got result: %+v", tc.doi, result)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestResolvePrefixStripping
// ---------------------------------------------------------------------------

// TestResolvePrefixStripping verifies that doi: and https://doi.org/ prefixes
// are stripped before the request path is built.
func TestResolvePrefixStripping(t *testing.T) {
	htmlBody := loadSciDBHTML(t)
	const canonicalDOI = "10.1038/nature12373"

	cases := []struct {
		name  string
		input string
	}{
		{"https prefix", "https://doi.org/" + canonicalDOI},
		{"http prefix", "http://doi.org/" + canonicalDOI},
		{"doi colon prefix", "doi:" + canonicalDOI},
		{"bare doi", canonicalDOI},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Must receive the bare DOI path — no prefix.
				if r.URL.Path != "/scidb/"+canonicalDOI {
					t.Errorf("unexpected path %q; want /scidb/%s", r.URL.Path, canonicalDOI)
					http.Error(w, "bad path", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(htmlBody)
			}))
			defer srv.Close()

			client := newDOITestClient(t, srv)
			logger := zaptest.NewLogger(t)

			result, err := Resolve(context.Background(), client, logger, tc.input)
			if err != nil {
				t.Fatalf("Resolve(%q): unexpected error: %v", tc.input, err)
			}
			if result.DOI != canonicalDOI {
				t.Errorf("result.DOI = %q, want %q", result.DOI, canonicalDOI)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestResolve404
// ---------------------------------------------------------------------------

// TestResolve404 verifies that a 404 from the server propagates as an error.
func TestResolve404(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := newDOITestClient(t, srv)
	logger := zaptest.NewLogger(t)

	result, err := Resolve(context.Background(), client, logger, "10.1038/nature12373")
	if err == nil {
		t.Fatalf("expected error for 404 response, got result: %+v", result)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not mention 404", err.Error())
	}
}
