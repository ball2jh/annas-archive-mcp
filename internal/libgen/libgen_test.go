package libgen

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

func newTestClient(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	logger := zaptest.NewLogger(t)
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: srv.Client().Transport,
	}
	return httpclient.NewForTest(hc, srv.Listener.Addr().String(), logger)
}

// adsHTML is a minimal libgen-style ads.php response with a session-key link.
func adsHTML(hash, key string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html>
<head><title>Library Genesis</title></head>
<body>
<a href="get.php?md5=%s&key=%s">GET</a>
</body></html>`, hash, key)
}

// ---------------------------------------------------------------------------
// TestFetchByHash_Success
// ---------------------------------------------------------------------------

func TestFetchByHash_Success(t *testing.T) {
	const (
		hash = "4693089fb653fcaab65ba10d800593ea"
		key  = "ABCDEF123456"
		file = "%PDF-1.7\nfake pdf bytes\n%%EOF"
	)

	var saw struct{ ads, get bool }

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ads.php":
			if r.URL.Query().Get("md5") != hash {
				http.Error(w, "bad md5", http.StatusBadRequest)
				return
			}
			saw.ads = true
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, adsHTML(hash, key))
		case r.URL.Path == "/get.php":
			q := r.URL.Query()
			if q.Get("md5") != hash || q.Get("key") != key {
				http.Error(w, "bad params", http.StatusBadRequest)
				return
			}
			saw.get = true
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, file)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	baseURL := srv.Listener.Addr().String()

	body, err := FetchByHash(context.Background(), client, baseURL, hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer body.Close()

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != file {
		t.Errorf("body = %q, want %q", got, file)
	}
	if !saw.ads || !saw.get {
		t.Errorf("expected both ads.php and get.php to be hit; got ads=%v get=%v", saw.ads, saw.get)
	}
}

// TestFetchByHash_UppercaseHash confirms we normalise to lowercase before
// asking libgen (libgen stores md5s lowercase).
func TestFetchByHash_UppercaseHash(t *testing.T) {
	const (
		hashLower = "4693089fb653fcaab65ba10d800593ea"
		hashUpper = "4693089FB653FCAAB65BA10D800593EA"
		key       = "ABC"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("md5") != hashLower {
			t.Errorf("request had md5 %q, want lowercase %q", r.URL.Query().Get("md5"), hashLower)
		}
		switch r.URL.Path {
		case "/ads.php":
			_, _ = io.WriteString(w, adsHTML(hashLower, key))
		case "/get.php":
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	body, err := FetchByHash(context.Background(), client, srv.Listener.Addr().String(), hashUpper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer body.Close()
	_, _ = io.Copy(io.Discard, body)
}

// ---------------------------------------------------------------------------
// TestFetchByHash_NotFound
// ---------------------------------------------------------------------------

// ads.php returns 200 but has no get.php link → NOT_FOUND_ON_LIBGEN.
func TestFetchByHash_NoKeyInAds(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!DOCTYPE html><html><body>
<!-- no get.php link here -->
<div>File not listed.</div>
</body></html>`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := FetchByHash(context.Background(), client, srv.Listener.Addr().String(),
		"4693089fb653fcaab65ba10d800593ea")
	if err == nil {
		t.Fatal("expected NOT_FOUND_ON_LIBGEN, got nil")
	}
	var ue *usererror.Error
	if !errors.As(err, &ue) {
		t.Fatalf("expected *usererror.Error, got %T: %v", err, err)
	}
	if ue.Code != "NOT_FOUND_ON_LIBGEN" {
		t.Errorf("Code = %q, want NOT_FOUND_ON_LIBGEN", ue.Code)
	}
}

// ---------------------------------------------------------------------------
// TestFetchByHash_AdsError / GetError
// ---------------------------------------------------------------------------

func TestFetchByHash_AdsError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := FetchByHash(context.Background(), client, srv.Listener.Addr().String(),
		"4693089fb653fcaab65ba10d800593ea")
	if err == nil {
		t.Fatal("expected UPSTREAM_REJECTED, got nil")
	}
	var ue *usererror.Error
	if !errors.As(err, &ue) || ue.Code != "UPSTREAM_REJECTED" {
		t.Errorf("want UPSTREAM_REJECTED, got: %v", err)
	}
}

func TestFetchByHash_GetError(t *testing.T) {
	const (
		hash = "4693089fb653fcaab65ba10d800593ea"
		key  = "XYZ"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ads.php":
			_, _ = io.WriteString(w, adsHTML(hash, key))
		case "/get.php":
			w.WriteHeader(http.StatusGone)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := FetchByHash(context.Background(), client, srv.Listener.Addr().String(), hash)
	if err == nil {
		t.Fatal("expected UPSTREAM_REJECTED, got nil")
	}
	var ue *usererror.Error
	if !errors.As(err, &ue) || ue.Code != "UPSTREAM_REJECTED" {
		t.Errorf("want UPSTREAM_REJECTED, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestFetchByHash_InvalidInputs
// ---------------------------------------------------------------------------

func TestFetchByHash_InvalidHash(t *testing.T) {
	cases := []string{"", "xxx", strings.Repeat("z", 32), "short"}
	for _, c := range cases {
		t.Run(fmt.Sprintf("hash=%q", c), func(t *testing.T) {
			_, err := FetchByHash(context.Background(), nil, "libgen.li", c)
			if err == nil {
				t.Fatal("expected INVALID_HASH error, got nil")
			}
			if !strings.Contains(err.Error(), "INVALID_HASH") {
				t.Errorf("error %q does not mention INVALID_HASH", err.Error())
			}
		})
	}
}

func TestFetchByHash_EmptyBaseURL(t *testing.T) {
	_, err := FetchByHash(context.Background(), nil, "",
		"4693089fb653fcaab65ba10d800593ea")
	if err == nil {
		t.Fatal("expected CONFIG error, got nil")
	}
	if !strings.Contains(err.Error(), "LIBGEN_BASE_URL") {
		t.Errorf("error %q does not mention LIBGEN_BASE_URL", err.Error())
	}
}

// ---------------------------------------------------------------------------
// TestGetKeyRegex — quick pattern sanity
// ---------------------------------------------------------------------------

func TestGetKeyRegex(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantHash string
		wantKey  string
	}{
		{
			name:     "plain anchor",
			input:    `<a href="get.php?md5=4693089fb653fcaab65ba10d800593ea&key=ABCDEF">GET</a>`,
			wantHash: "4693089fb653fcaab65ba10d800593ea",
			wantKey:  "ABCDEF",
		},
		{
			name:     "extra attrs",
			input:    `<a class="btn" id="x" href="get.php?md5=deadbeefcafebabe0123456789abcdef&key=ZZZ999"`,
			wantHash: "deadbeefcafebabe0123456789abcdef",
			wantKey:  "ZZZ999",
		},
		{
			name:     "no match",
			input:    `<div>nothing</div>`,
			wantHash: "",
			wantKey:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := getKeyRE.FindStringSubmatch(tc.input)
			var gotHash, gotKey string
			if len(m) >= 3 {
				gotHash, gotKey = m[1], m[2]
			}
			if gotHash != tc.wantHash || gotKey != tc.wantKey {
				t.Errorf("got (%q, %q), want (%q, %q)", gotHash, gotKey, tc.wantHash, tc.wantKey)
			}
		})
	}
}
