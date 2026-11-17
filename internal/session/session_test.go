package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

const host = "annas-archive.example"

func solved() *State {
	return &State{
		UserAgent: "BrowserUA/1.0",
		Cookies: []Cookie{
			{Name: "shared", Value: "a", Domain: "." + host, Path: "/", Expires: float64(time.Now().Add(time.Hour).Unix())},
			{Name: "hostonly", Value: "b", Domain: host, Path: "/", Expires: -1},
		},
	}
}

func TestRenewSavesAndLoadReturnsSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "session.json")
	solves := 0
	store := NewStore(path, host, func(context.Context, string) (*State, error) {
		solves++
		return solved(), nil
	}, zaptest.NewLogger(t))

	if store.Load() != nil {
		t.Fatal("Load() returned a session before any was saved")
	}
	if _, err := store.Renew(context.Background()); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("session file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session file mode = %o, want 600 (it holds credentials)", perm)
	}

	// A fresh Store, as after a restart, finds it without solving again.
	loaded := NewStore(path, host, nil, zaptest.NewLogger(t)).Load()
	if loaded == nil || loaded.UserAgent != "BrowserUA/1.0" || len(loaded.Cookies) != 2 {
		t.Fatalf("Load() = %+v, want the saved session", loaded)
	}
	if solves != 1 {
		t.Errorf("solved %d time(s), want 1", solves)
	}
}

func TestSessionForAnotherHostIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := NewStore(path, host, func(context.Context, string) (*State, error) { return solved(), nil }, zaptest.NewLogger(t))
	if _, err := store.Renew(context.Background()); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if got := NewStore(path, "other.example", nil, zaptest.NewLogger(t)).Load(); got != nil {
		t.Errorf("Load() = %+v for a different host, want nil", got)
	}
}

// A second process must pick up the file the first one renewed instead of
// opening its own browser.
func TestRenewPrefersFileWrittenByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	logger := zaptest.NewLogger(t)

	second := NewStore(path, host, func(context.Context, string) (*State, error) {
		t.Error("second process launched a browser although the file was already renewed")
		return nil, errors.New("unexpected")
	}, logger)
	second.Load() // starts with no session

	first := NewStore(path, host, func(context.Context, string) (*State, error) { return solved(), nil }, logger)
	if _, err := first.Renew(context.Background()); err != nil {
		t.Fatalf("first Renew: %v", err)
	}

	state, err := second.Renew(context.Background())
	if err != nil || state == nil || state.UserAgent != "BrowserUA/1.0" {
		t.Fatalf("second Renew = %+v, %v; want the first process's session", state, err)
	}
}

func TestFailedRenewIsNotRepeatedImmediately(t *testing.T) {
	solves := 0
	store := NewStore(filepath.Join(t.TempDir(), "session.json"), host, func(context.Context, string) (*State, error) {
		solves++
		return nil, errors.New("no display")
	}, zaptest.NewLogger(t))

	for range 3 {
		if _, err := store.Renew(context.Background()); err == nil {
			t.Fatal("Renew succeeded, want an error")
		}
	}
	if solves != 1 {
		t.Errorf("browser launched %d time(s), want 1 within the cooldown", solves)
	}
}

func TestRenewWithoutSolverFails(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "session.json"), host, nil, zaptest.NewLogger(t))
	if _, err := store.Renew(context.Background()); err == nil {
		t.Fatal("Renew succeeded with browser use disabled")
	}
}

func TestHTTPCookies(t *testing.T) {
	cookies := solved().HTTPCookies()
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2", len(cookies))
	}
	if cookies[0].Domain != "."+host || cookies[0].Expires.IsZero() {
		t.Errorf("shared cookie = %+v, want its domain and expiry kept", cookies[0])
	}
	if cookies[1].Domain != "" || !cookies[1].Expires.IsZero() {
		t.Errorf("host-only session cookie = %+v, want no domain and no expiry", cookies[1])
	}
}
