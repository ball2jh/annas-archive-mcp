package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// TestDownloadMissingSecretKeyNoFallback
// ---------------------------------------------------------------------------

// When ANNAS_SECRET_KEY is empty AND LibgenEnabled is false, Download should
// surface AUTH_REQUIRED without making any network calls.
func TestDownloadMissingSecretKeyNoFallback(t *testing.T) {
	const validHash = "0123456789abcdef0123456789abcdef"

	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:     "",
		DownloadPath:  t.TempDir(),
		LibgenEnabled: false,
	}

	// Fail the test if any HTTP request fires.
	client := newDownloadTestClient(t, httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no HTTP request should be made when SecretKey empty + LibgenEnabled=false; got %s %s",
			r.Method, r.URL.Path)
	})))

	_, err := Download(context.Background(), client, logger, cfg, validHash, "My Book", "epub")
	if err == nil {
		t.Fatal("expected AUTH_REQUIRED error, got nil")
	}
	if !strings.Contains(err.Error(), "AUTH_REQUIRED") {
		t.Errorf("error %q does not contain AUTH_REQUIRED", err.Error())
	}
}

// TestDownloadInvalidHash verifies hash validation fires before any network or
// config-related check. Important because the previous contract returned
// AUTH_REQUIRED for any missing-key call — we don't want to regress that into
// leaking AUTH_REQUIRED for a bad hash.
func TestDownloadInvalidHash(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:    "mysecret",
		DownloadPath: t.TempDir(),
	}
	client := newDownloadTestClient(t, httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no HTTP request should be made for an invalid hash; got %s %s",
			r.Method, r.URL.Path)
	})))

	_, err := Download(context.Background(), client, logger, cfg, "abc123", "My Book", "epub")
	if err == nil {
		t.Fatal("expected INVALID_HASH error, got nil")
	}
	if !strings.Contains(err.Error(), "INVALID_HASH") {
		t.Errorf("error %q does not contain INVALID_HASH", err.Error())
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
		hash      = "0123456789abcdef0123456789abcdef"
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
// TestDownloadLibgenFallback — Anna's fast_download fails, libgen succeeds.
// ---------------------------------------------------------------------------

func TestDownloadLibgenFallback(t *testing.T) {
	const (
		hash      = "0123456789abcdef0123456789abcdef"
		secretKey = "mysecret"
		libgenKey = "LIBGENSESSIONKEY"
		pdfBody   = "%PDF-1.7 fake libgen body"
	)
	downloadDir := t.TempDir()

	// Single test server handles all three paths Anna's + Libgen hit.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Anna's fast_download.json → simulate a NOT_FAST_DOWNLOADABLE response
		// (HTTP 400 + JSON error body), which is exactly the case libgen should rescue.
		case strings.HasPrefix(r.URL.Path, "/dyn/api/fast_download.json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"download_url": null, "error": "Invalid domain_index or path_index"}`)

		// Libgen ads.php → return the HTML containing a get.php link.
		case r.URL.Path == "/ads.php":
			if r.URL.Query().Get("md5") != hash {
				http.Error(w, "bad md5", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, fmt.Sprintf(`<a href="get.php?md5=%s&key=%s">GET</a>`,
				hash, libgenKey))

		// Libgen get.php → deliver the file bytes.
		case r.URL.Path == "/get.php":
			q := r.URL.Query()
			if q.Get("md5") != hash || q.Get("key") != libgenKey {
				http.Error(w, "bad params", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, pdfBody)

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newDownloadTestClient(t, srv)
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:     secretKey,
		DownloadPath:  downloadDir,
		LibgenBaseURL: srv.Listener.Addr().String(),
		LibgenEnabled: true,
		MaxRetries:    0,
	}

	result, err := Download(context.Background(), client, logger, cfg, hash, "Thang", "pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Source != "libgen.li" {
		t.Errorf("Source = %q, want libgen.li", result.Source)
	}
	if result.AlreadyExisted {
		t.Error("AlreadyExisted should be false for a fresh download")
	}

	got, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != pdfBody {
		t.Errorf("file body mismatch:\n got  %q\n want %q", got, pdfBody)
	}
}

// TestDownloadLibgenDisabled verifies the fallback is genuinely opt-out when
// LibgenEnabled=false — the Anna's error should surface directly.
func TestDownloadLibgenDisabled(t *testing.T) {
	const (
		hash      = "0123456789abcdef0123456789abcdef"
		secretKey = "mysecret"
	)
	downloadDir := t.TempDir()

	var sawLibgen int32 // would be non-zero if libgen was hit
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ads.php" || r.URL.Path == "/get.php" {
			t.Errorf("libgen %s should not be hit when LibgenEnabled=false", r.URL.Path)
			sawLibgen++
		}
		// Anna's fast_download → fail with unrecognized API error so we'd
		// otherwise cascade. If the cascade fires, the test server will
		// return 404 for libgen paths (which we assert above).
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"download_url": null, "error": "Invalid domain_index or path_index"}`)
	}))
	defer srv.Close()

	client := newDownloadTestClient(t, srv)
	logger := zaptest.NewLogger(t)
	cfg := &config.Config{
		SecretKey:     secretKey,
		DownloadPath:  downloadDir,
		LibgenEnabled: false, // opt-out
		MaxRetries:    0,
	}

	_, err := Download(context.Background(), client, logger, cfg, hash, "X", "pdf")
	if err == nil {
		t.Fatal("expected Anna's error to bubble up, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestDownloadSkipIfExists
// ---------------------------------------------------------------------------

func TestDownloadSkipIfExists(t *testing.T) {
	const (
		hash      = "0123456789abcdef0123456789abcdef"
		secretKey = "mysecret"
		title     = "Already Here"
		format    = "pdf"
	)
	downloadDir := t.TempDir()

	// Pre-populate the target file with non-empty content.
	existing := []byte("pre-existing content")
	targetName := SanitizeFilename(title, format)
	if err := os.WriteFile(downloadDir+"/"+targetName, existing, 0o644); err != nil {
		t.Fatalf("could not seed existing file: %v", err)
	}

	// Use a server that fails the test if any HTTP request is made.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request when file already exists: %s %s", r.Method, r.URL.Path)
		http.Error(w, "no request should be made", http.StatusInternalServerError)
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
	if !result.AlreadyExisted {
		t.Errorf("AlreadyExisted = false, want true")
	}
	if result.Message != "File already exists — download skipped." {
		t.Errorf("Message = %q", result.Message)
	}

	// Content must not have been overwritten.
	got, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("read result file: %v", err)
	}
	if string(got) != string(existing) {
		t.Errorf("file was overwritten; content = %q, want %q", got, existing)
	}
}

// TestDownloadDoesNotSkipEmptyFile verifies a zero-byte file at the target
// path is not treated as "already exists" — it gets re-downloaded.
func TestDownloadDoesNotSkipEmptyFile(t *testing.T) {
	const (
		hash      = "0123456789abcdef0123456789abcdef"
		secretKey = "mysecret"
		title     = "Empty"
		format    = "pdf"
	)
	downloadDir := t.TempDir()

	targetName := SanitizeFilename(title, format)
	if err := os.WriteFile(downloadDir+"/"+targetName, []byte{}, 0o644); err != nil {
		t.Fatalf("could not create empty file: %v", err)
	}

	fileContent := "real content"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/dyn/api/fast_download.json"):
			downloadURL := "https://" + r.Host + "/files/book.pdf"
			resp := map[string]interface{}{"download_url": downloadURL}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/files/book.pdf":
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
	if result.AlreadyExisted {
		t.Errorf("AlreadyExisted = true, want false for empty file")
	}
}

func TestDownloadClassifiesDDoSGuardFileResponse(t *testing.T) {
	const (
		hash      = "0123456789abcdef0123456789abcdef"
		secretKey = "mysecret"
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/dyn/api/fast_download.json"):
			downloadURL := "https://" + r.Host + "/files/book.pdf"
			resp := map[string]interface{}{"download_url": downloadURL}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/files/book.pdf":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "Protected by DDoS-Guard")
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newDownloadTestClient(t, srv)
	cfg := &config.Config{
		SecretKey:    secretKey,
		DownloadPath: t.TempDir(),
		MaxRetries:   0,
	}

	_, err := Download(context.Background(), client, zaptest.NewLogger(t), cfg, hash, "Book", "pdf")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var statusErr *httpclient.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected StatusError, got %T: %v", err, err)
	}
	if !statusErr.DDoSGuard {
		t.Fatalf("DDoSGuard = false, want true")
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

	_, err := Download(context.Background(), client, logger, cfg, "0123456789abcdef0123456789abcdef", "My Book", "epub")
	if err == nil {
		t.Fatal("expected error for non-existent download path, got nil")
	}
}

func TestDownloadPathMustBeDirectory(t *testing.T) {
	logger := zaptest.NewLogger(t)
	dir := t.TempDir()
	filePath := dir + "/not-a-dir"
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed file path: %v", err)
	}

	cfg := &config.Config{
		SecretKey:    "mysecret",
		DownloadPath: filePath,
	}

	client := newDownloadTestClient(t, httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP request should be made when download path is a file")
	})))

	_, err := Download(context.Background(), client, logger, cfg, "0123456789abcdef0123456789abcdef", "My Book", "epub")
	if err == nil {
		t.Fatal("expected error when download path is not a directory, got nil")
	}
	if !strings.Contains(err.Error(), "[PATH_UNAVAILABLE]") {
		t.Fatalf("error = %q, want [PATH_UNAVAILABLE]", err.Error())
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
