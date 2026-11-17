package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

const (
	testDOI      = "10.1038/nature12373"
	scidbHash    = "d89c394b00116f093b5d9d6a6611f975" // hash linked from testdata/scidb_page.html
	sciNetPDF    = "/storage/1/abc/paper.pdf"
	sciNetBody   = "%PDF-1.7 from sci-net"
	fastBody     = "%PDF-1.7 from fast_download"
	sciNetIframe = `<html><body><iframe src="` + sciNetPDF + `#view=FitH"></iframe></body></html>`
)

// doiServer simulates Anna's Archive and Sci-Net on one host. scidbStatus
// controls whether Anna's Archive knows the DOI.
func doiServer(t *testing.T, scidbStatus int, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	scidbPage, err := os.ReadFile("../../testdata/scidb_page.html")
	if err != nil {
		t.Fatalf("read scidb fixture: %v", err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.URL.Path == "/scidb/"+testDOI:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(scidbStatus)
			_, _ = w.Write(scidbPage)
		case r.URL.Path == "/dyn/api/fast_download.json":
			if r.URL.Query().Get("md5") != scidbHash {
				t.Errorf("fast_download called with md5 %q, want %q", r.URL.Query().Get("md5"), scidbHash)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"download_url": "https://`+r.Host+`/files/paper.pdf"}`)
		case r.URL.Path == "/files/paper.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, fastBody)
		case r.URL.Path == "/"+testDOI:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, sciNetIframe)
		case r.URL.Path == sciNetPDF:
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, sciNetBody)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doiConfig(srv *httptest.Server, dir, secretKey string) *config.Config {
	return &config.Config{
		SecretKey:     secretKey,
		DownloadPath:  dir,
		ScinetBaseURL: srv.Listener.Addr().String(),
	}
}

func TestByDOIViaFastDownload(t *testing.T) {
	var requests atomic.Int32
	srv := doiServer(t, http.StatusOK, &requests)
	dir := t.TempDir()

	// A doi.org URL exercises prefix cleaning; the empty title and format
	// exercise the defaults.
	result, err := ByDOI(context.Background(), newDownloadTestClient(t, srv), zaptest.NewLogger(t),
		doiConfig(srv, dir, "secret"), "https://doi.org/"+testDOI, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Source != SourceFastDownload {
		t.Errorf("Source = %q, want %q", result.Source, SourceFastDownload)
	}
	if want := filepath.Join(dir, "10.1038_nature12373.pdf"); result.FilePath != want {
		t.Errorf("FilePath = %q, want %q", result.FilePath, want)
	}
	if data, _ := os.ReadFile(result.FilePath); string(data) != fastBody {
		t.Errorf("file content = %q, want %q", data, fastBody)
	}
}

func TestByDOIFallsBackToSciNet(t *testing.T) {
	cases := []struct {
		name        string
		scidbStatus int
		secretKey   string
	}{
		{"DOI unknown to Anna's Archive", http.StatusNotFound, "secret"},
		{"no secret key and Libgen disabled", http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := doiServer(t, tc.scidbStatus, &requests)

			result, err := ByDOI(context.Background(), newDownloadTestClient(t, srv), zaptest.NewLogger(t),
				doiConfig(srv, t.TempDir(), tc.secretKey), testDOI, "Paper", "pdf")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Source != SourceSciNet {
				t.Errorf("Source = %q, want %q", result.Source, SourceSciNet)
			}
			if !strings.Contains(result.Message, SourceSciNet) {
				t.Errorf("Message = %q, want it to name %q", result.Message, SourceSciNet)
			}
			if data, _ := os.ReadFile(result.FilePath); string(data) != sciNetBody {
				t.Errorf("file content = %q, want %q", data, sciNetBody)
			}
		})
	}
}

func TestByDOISkipsExistingFile(t *testing.T) {
	var requests atomic.Int32
	srv := doiServer(t, http.StatusOK, &requests)
	dir := t.TempDir()
	existing := filepath.Join(dir, "Paper.pdf")
	if err := os.WriteFile(existing, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ByDOI(context.Background(), newDownloadTestClient(t, srv), zaptest.NewLogger(t),
		doiConfig(srv, dir, "secret"), testDOI, "Paper", "pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.AlreadyExisted || result.Source != SourceCache || result.FilePath != existing {
		t.Errorf("result = %+v, want cached hit at %q", result, existing)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("made %d upstream request(s), want 0", n)
	}
}

func TestByDOIValidatesBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int32
	srv := doiServer(t, http.StatusOK, &requests)

	cases := []struct {
		name     string
		doi      string
		dir      string
		wantCode string
	}{
		{"empty DOI", "  ", t.TempDir(), usererror.CodeInvalidDOI},
		{"malformed DOI", "not-a-doi", t.TempDir(), usererror.CodeInvalidDOI},
		{"no download path", testDOI, "", usererror.CodePathRequired},
		{"missing download path", testDOI, "/nonexistent/annas-archive-mcp-test", usererror.CodePathUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ByDOI(context.Background(), newDownloadTestClient(t, srv), zaptest.NewLogger(t),
				doiConfig(srv, tc.dir, "secret"), tc.doi, "Paper", "pdf")
			if !usererror.HasCode(err, tc.wantCode) {
				t.Errorf("error = %v, want code %s", err, tc.wantCode)
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("made %d upstream request(s), want 0", n)
	}
}

func TestAtomicWriteRejectsEmptyBody(t *testing.T) {
	dir := t.TempDir()
	if _, err := AtomicWrite(dir, "empty.pdf", strings.NewReader("")); !errors.Is(err, errEmptyBody) {
		t.Fatalf("error = %v, want errEmptyBody", err)
	}
	assertDirEmpty(t, dir)
}

func TestAtomicWriteReportsSourceFailure(t *testing.T) {
	dir := t.TempDir()
	boom := errors.New("connection reset")
	src := io.MultiReader(strings.NewReader("partial"), errReader{boom})

	_, err := AtomicWrite(dir, "partial.pdf", src)
	var readErr *bodyReadError
	if !errors.As(err, &readErr) || !errors.Is(err, boom) {
		t.Fatalf("error = %v, want *bodyReadError wrapping %v", err, boom)
	}
	assertDirEmpty(t, dir)
}

func TestAtomicWriteFileMode(t *testing.T) {
	path, err := AtomicWrite(t.TempDir(), "book.epub", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %o, want 644", got)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("directory has %d leftover file(s), want none", len(entries))
	}
}
