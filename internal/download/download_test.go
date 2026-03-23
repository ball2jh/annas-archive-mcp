package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
)

// ---------------------------------------------------------------------------
// TestSanitizeFilename
// ---------------------------------------------------------------------------

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		format string
		want   string
	}{
		{
			name:   "simple title with format",
			title:  "Python Programming",
			format: "epub",
			want:   "Python Programming.epub",
		},
		{
			name:   "forward slash replaced",
			title:  "A/B Testing",
			format: "pdf",
			want:   "A_B Testing.pdf",
		},
		{
			name:   "backslash replaced",
			title:  `A\B`,
			format: "pdf",
			want:   "A_B.pdf",
		},
		{
			name:   "colon replaced",
			title:  "Go: The Language",
			format: "pdf",
			want:   "Go_ The Language.pdf",
		},
		{
			name:   "asterisk replaced",
			title:  "Star*Wars",
			format: "mobi",
			want:   "Star_Wars.mobi",
		},
		{
			name:   "question mark replaced",
			title:  "What?",
			format: "epub",
			want:   "What_.epub",
		},
		{
			name:   "double quote replaced",
			title:  `Say "Hello"`,
			format: "txt",
			want:   "Say _Hello_.txt",
		},
		{
			name:   "angle brackets replaced",
			title:  "<HTML>",
			format: "html",
			want:   "_HTML_.html",
		},
		{
			name:   "pipe replaced",
			title:  "A|B",
			format: "pdf",
			want:   "A_B.pdf",
		},
		{
			name:   "all invalid chars",
			title:  `/\:*?"<>|`,
			format: "epub",
			want:   "_________.epub",
		},
		{
			name:   "empty title falls back to 'download'",
			title:  "",
			format: "epub",
			want:   "download.epub",
		},
		{
			name:   "whitespace-only title falls back to 'download'",
			title:  "   ",
			format: "epub",
			want:   "download.epub",
		},
		{
			name:   "no format — no extension added",
			title:  "MyBook",
			format: "",
			want:   "MyBook",
		},
		{
			name:   "length truncated at 200 runes",
			title:  strings.Repeat("a", 250),
			format: "pdf",
			want:   strings.Repeat("a", 200) + ".pdf",
		},
		{
			name:   "unicode title preserved and truncated correctly",
			title:  strings.Repeat("ä", 210),
			format: "pdf",
			want:   strings.Repeat("ä", 200) + ".pdf",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFilename(tc.title, tc.format)
			if got != tc.want {
				t.Errorf("SanitizeFilename(%q, %q) = %q, want %q", tc.title, tc.format, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestAtomicWrite
// ---------------------------------------------------------------------------

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	content := "hello, world!\n"

	path, err := AtomicWrite(dir, "test.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File must exist at the returned path.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read output file: %v", err)
	}

	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}

	// No temp files must remain.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("could not read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".download-") {
			t.Errorf("temp file not cleaned up: %s", e.Name())
		}
	}
}

func TestAtomicWriteBinaryContent(t *testing.T) {
	dir := t.TempDir()
	// Simulate binary epub-like content.
	content := []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0xFF, 0xAB}

	path, err := AtomicWrite(dir, "book.epub", strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read output file: %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("binary content mismatch")
	}
}

// ---------------------------------------------------------------------------
// TestDownloadMissingSecretKey
// ---------------------------------------------------------------------------

func TestDownloadMissingSecretKey(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:    "",
		DownloadPath: t.TempDir(),
	}

	// We need a non-nil client but no requests should be made.
	client := newDownloadTestClient(t, httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made when SecretKey is empty")
	})))

	_, err := Download(context.Background(), client, logger, cfg, "abc123", "My Book", "epub")
	if err == nil {
		t.Fatal("expected error for missing secret key, got nil")
	}

	const wantMsg = "Set ANNAS_SECRET_KEY"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("error %q does not contain %q", err.Error(), wantMsg)
	}
}

// ---------------------------------------------------------------------------
// TestDownloadMissingDownloadPath
// ---------------------------------------------------------------------------

func TestDownloadMissingDownloadPath(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:    "mysecret",
		DownloadPath: "",
	}

	client := newDownloadTestClient(t, httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made when DownloadPath is empty")
	})))

	_, err := Download(context.Background(), client, logger, cfg, "abc123", "My Book", "epub")
	if err == nil {
		t.Fatal("expected error for missing download path, got nil")
	}

	const wantMsg = "Set ANNAS_DOWNLOAD_PATH"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("error %q does not contain %q", err.Error(), wantMsg)
	}
}

// ---------------------------------------------------------------------------
// TestDownloadSuccess
// ---------------------------------------------------------------------------

func TestDownloadSuccess(t *testing.T) {
	const (
		hash      = "deadbeef"
		secretKey = "mysecret"
		title     = "Python Programming"
		format    = "epub"
	)

	fileContent := "fake epub content"
	downloadDir := t.TempDir()

	// The test server handles two distinct URL patterns:
	//   - /dyn/api/fast_download.json  → returns the download URL
	//   - /files/book.epub             → serves the file bytes
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/dyn/api/fast_download.json"):
			q := r.URL.Query()
			if q.Get("md5") != hash || q.Get("key") != secretKey {
				http.Error(w, "bad params", http.StatusBadRequest)
				return
			}
			// Return the full URL of the file endpoint on this same server.
			downloadURL := "https://" + r.Host + "/files/book.epub"
			resp := map[string]interface{}{"download_url": downloadURL}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.URL.Path == "/files/book.epub":
			w.Header().Set("Content-Type", "application/epub+zip")
			_, _ = io.WriteString(w, fileContent)

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newDownloadTestClient(t, srv)
	logger := zaptest.NewLogger(t)

	cfg := &config.Config{
		SecretKey:    secretKey,
		DownloadPath: downloadDir,
		MaxRetries:   0,
	}

	result, err := Download(context.Background(), client, logger, cfg, hash, title, format)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("result is nil")
	}

	if result.Message != "Downloaded successfully" {
		t.Errorf("message = %q, want %q", result.Message, "Downloaded successfully")
	}

	// The file must exist and have the right content.
	data, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("could not read downloaded file at %s: %v", result.FilePath, err)
	}
	if string(data) != fileContent {
		t.Errorf("file content = %q, want %q", string(data), fileContent)
	}

	// The filename must be based on the sanitized title.
	wantFilename := SanitizeFilename(title, format)
	if !strings.HasSuffix(result.FilePath, wantFilename) {
		t.Errorf("file path %q does not end with %q", result.FilePath, wantFilename)
	}
}

// ---------------------------------------------------------------------------
// TestDownloadInvalidPath
// ---------------------------------------------------------------------------

func TestDownloadInvalidPath(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:    "mysecret",
		DownloadPath: "/nonexistent/path/that/does/not/exist",
	}

	client := newDownloadTestClient(t, httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made when download path does not exist")
	})))

	_, err := Download(context.Background(), client, logger, cfg, "abc123", "My Book", "epub")
	if err == nil {
		t.Fatal("expected error for non-existent download path, got nil")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newDownloadTestClient returns a *httpclient.Client wired to a TLS test
// server using the server's own TLS transport. This mirrors the pattern used
// in api_test.go.
func newDownloadTestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	logger := zaptest.NewLogger(t)
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), logger)
}
