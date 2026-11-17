package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap/zaptest"
)

const passCookie = "aa_ddg_check"

// guardedServer answers 403 with a DDoS-Guard page unless the pass cookie is
// sent, and records the User-Agent of the request it finally serves.
func guardedServer(t *testing.T, servedUA *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(passCookie); err != nil || c.Value != "ok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<title>DDOS-GUARD</title>")
			return
		}
		servedUA.Store(r.UserAgent())
		_, _ = io.WriteString(w, "real page")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func guardedClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewForTest(&http.Client{Transport: srv.Client().Transport}, srv.Listener.Addr().String(), zaptest.NewLogger(t))
}

func passingSession() Session {
	return Session{UserAgent: "BrowserUA/1.0", Cookies: []*http.Cookie{{Name: passCookie, Value: "ok", Path: "/"}}}
}

func TestChallengeIsRecoveredOnce(t *testing.T) {
	var servedUA atomic.Value
	srv := guardedServer(t, &servedUA)
	c := guardedClient(t, srv)

	var refreshes atomic.Int32
	c.OnChallenge(func(context.Context) (Session, error) {
		refreshes.Add(1)
		return passingSession(), nil
	})

	for range 2 { // the second request must reuse the session
		resp, err := c.Get(context.Background(), srv.URL+"/search", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "real page" {
			t.Fatalf("status = %d, body = %q; want the real page", resp.StatusCode, body)
		}
	}
	if n := refreshes.Load(); n != 1 {
		t.Errorf("session refreshed %d time(s), want 1", n)
	}
	if got := servedUA.Load(); got != "BrowserUA/1.0" {
		t.Errorf("User-Agent = %q, want the session's", got)
	}
}

func TestSavedSessionAvoidsChallenge(t *testing.T) {
	var servedUA atomic.Value
	srv := guardedServer(t, &servedUA)
	c := guardedClient(t, srv)
	c.UseSession(passingSession())
	c.OnChallenge(func(context.Context) (Session, error) {
		t.Error("refresh called although the saved session is valid")
		return Session{}, errors.New("unexpected")
	})

	resp, err := c.Get(context.Background(), srv.URL+"/search", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// When the refresh fails, or its session still does not pass, the caller gets
// the 403 back (once) rather than an endless refresh loop.
func TestChallengeThatCannotBeRecovered(t *testing.T) {
	cases := map[string]SessionRefresher{
		"refresh fails":       func(context.Context) (Session, error) { return Session{}, errors.New("no chrome") },
		"session is rejected": func(context.Context) (Session, error) { return Session{UserAgent: "x"}, nil },
	}
	for name, refresh := range cases {
		t.Run(name, func(t *testing.T) {
			var servedUA atomic.Value
			srv := guardedServer(t, &servedUA)
			c := guardedClient(t, srv)
			var refreshes atomic.Int32
			c.OnChallenge(func(ctx context.Context) (Session, error) {
				refreshes.Add(1)
				return refresh(ctx)
			})

			_, err := c.GetHTML(context.Background(), "/search")
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || !statusErr.DDoSGuard {
				t.Fatalf("error = %v, want a DDoS-Guard StatusError", err)
			}
			if n := refreshes.Load(); n != 1 {
				t.Errorf("session refreshed %d time(s), want 1", n)
			}
		})
	}
}

// Requests challenged at the same moment share one refresh.
func TestConcurrentChallengesRefreshOnce(t *testing.T) {
	var servedUA atomic.Value
	srv := guardedServer(t, &servedUA)
	c := guardedClient(t, srv)

	var refreshes atomic.Int32
	c.OnChallenge(func(context.Context) (Session, error) {
		refreshes.Add(1)
		return passingSession(), nil
	})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			resp, err := c.Get(context.Background(), srv.URL+"/search", nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
	wg.Wait()
	if n := refreshes.Load(); n != 1 {
		t.Errorf("session refreshed %d time(s), want 1", n)
	}
}
