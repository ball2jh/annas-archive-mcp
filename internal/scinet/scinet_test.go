package scinet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// newTestClient wires a Client to the given TLS httptest server, with
// timeouts tight enough to fail tests fast but generous enough to avoid flakes.
func newTestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	logger := zaptest.NewLogger(t)
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), logger)
}

// pageHTML returns a minimal sci-net-style page embedding an iframe src.
func pageHTML(pdfPath string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Sci-Net: Something</title></head>
<body>
<div class="pdf">
<iframe src = "%s#view=FitH&navpanes=0"></iframe>
</div>
</body></html>`, pdfPath)
}

// ---------------------------------------------------------------------------
// TestFetchPDF_Success
// ---------------------------------------------------------------------------

func TestFetchPDF_Success(t *testing.T) {
	const (
		doi       = "10.1016/j.jaad.2025.09.031"
		pdfSlug   = "Oral-minoxidil-2-5-mg-versus-5-mg.pdf"
		pdfStored = "/storage/6021860/abc123deadbeef/" + pdfSlug
		pdfBytes  = "%PDF-1.7\n<binary pdf bytes>\n%%EOF"
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+doi:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, pageHTML(pdfStored))
		case r.URL.Path == pdfStored:
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, pdfBytes)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	baseURL := srv.Listener.Addr().String()

	body, filename, err := FetchPDF(context.Background(), client, baseURL, doi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer body.Close()

	if filename != pdfSlug {
		t.Errorf("filename = %q, want %q", filename, pdfSlug)
	}

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != pdfBytes {
		t.Errorf("body mismatch:\n got  %q\n want %q", got, pdfBytes)
	}
}

// ---------------------------------------------------------------------------
// TestFetchPDF_NotFound
// ---------------------------------------------------------------------------

// TestFetchPDF_NoIframe: the page returns 200 but has no <iframe> → classified
// as NOT_FOUND_ON_SCINET (article exists in DB but no PDF).
func TestFetchPDF_NoIframe(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!DOCTYPE html><html><body>
<div class="buttons"><a>request</a></div>
<!-- no iframe here -->
</body></html>`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	baseURL := srv.Listener.Addr().String()

	_, _, err := FetchPDF(context.Background(), client, baseURL, "10.9999/nope")
	if err == nil {
		t.Fatal("expected NOT_FOUND_ON_SCINET error, got nil")
	}
	var ue *usererror.Error
	if !errors.As(err, &ue) {
		t.Fatalf("expected *usererror.Error in chain, got %T: %v", err, err)
	}
	if ue.Code != "NOT_FOUND_ON_SCINET" {
		t.Errorf("Code = %q, want NOT_FOUND_ON_SCINET", ue.Code)
	}
}

// TestFetchPDF_PageError: the DOI page itself returns a non-200 → classified
// as UPSTREAM_REJECTED so the tool handler can say "sci-net rejected".
func TestFetchPDF_PageError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "blocked")
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	baseURL := srv.Listener.Addr().String()

	_, _, err := FetchPDF(context.Background(), client, baseURL, "10.1016/j.jaad.2025.08.079")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ue *usererror.Error
	if !errors.As(err, &ue) {
		t.Fatalf("expected *usererror.Error, got %T: %v", err, err)
	}
	if ue.Code != "UPSTREAM_REJECTED" {
		t.Errorf("Code = %q, want UPSTREAM_REJECTED", ue.Code)
	}
}

// TestFetchPDF_PDFError: page is fine but the storage URL returns non-200.
func TestFetchPDF_PDFError(t *testing.T) {
	const (
		doi       = "10.9999/broken"
		pdfStored = "/storage/1/deadbeef/broken.pdf"
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+doi:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, pageHTML(pdfStored))
		default:
			http.Error(w, "gone", http.StatusGone)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	baseURL := srv.Listener.Addr().String()

	_, _, err := FetchPDF(context.Background(), client, baseURL, doi)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ue *usererror.Error
	if !errors.As(err, &ue) {
		t.Fatalf("expected *usererror.Error, got %T: %v", err, err)
	}
	if ue.Code != "UPSTREAM_REJECTED" {
		t.Errorf("Code = %q, want UPSTREAM_REJECTED", ue.Code)
	}
}

// ---------------------------------------------------------------------------
// TestFetchPDF_EmptyInputs
// ---------------------------------------------------------------------------

func TestFetchPDF_EmptyDOI(t *testing.T) {
	_, _, err := FetchPDF(context.Background(), nil, "sci-net.xyz", "")
	if err == nil {
		t.Fatal("expected INVALID_DOI error, got nil")
	}
	if !strings.Contains(err.Error(), "INVALID_DOI") {
		t.Errorf("error %q does not mention INVALID_DOI", err.Error())
	}
}

func TestFetchPDF_EmptyBaseURL(t *testing.T) {
	_, _, err := FetchPDF(context.Background(), nil, "", "10.0/x")
	if err == nil {
		t.Fatal("expected CONFIG error, got nil")
	}
	if !strings.Contains(err.Error(), "SCINET_BASE_URL") {
		t.Errorf("error %q does not mention SCINET_BASE_URL", err.Error())
	}
}

// ---------------------------------------------------------------------------
// TestIframeRegex — quick regex sanity. Using realistic HTML.
// ---------------------------------------------------------------------------

func TestIframeRegex(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string // empty means "no match"
	}{
		{
			name: "space-padded equals",
			html: `<iframe src = "/storage/1/abc/x.pdf#view=FitH"></iframe>`,
			want: "/storage/1/abc/x.pdf",
		},
		{
			name: "single quotes",
			html: `<iframe src='/storage/2/def/y.pdf'></iframe>`,
			want: "/storage/2/def/y.pdf",
		},
		{
			name: "no fragment",
			html: `<iframe src="/storage/3/ghi/z.pdf" class="pdf"></iframe>`,
			want: "/storage/3/ghi/z.pdf",
		},
		{
			name: "other attrs before src",
			html: `<iframe class="pdf" width="100" src="/storage/4/j/k.pdf"></iframe>`,
			want: "/storage/4/j/k.pdf",
		},
		{
			name: "no iframe",
			html: `<div>nothing here</div>`,
			want: "",
		},
		{
			name: "iframe without storage path",
			html: `<iframe src="/some-other-path.pdf"></iframe>`,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := pdfSrcRE.FindStringSubmatch(tc.html)
			var got string
			if len(m) >= 2 {
				got = m[1]
			}
			if got != tc.want {
				t.Errorf("regex match = %q, want %q", got, tc.want)
			}
		})
	}
}
