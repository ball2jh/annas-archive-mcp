// Package session persists the browser session (cookies and User-Agent) that
// lets the server through Anna's Archive's DDoS-Guard browser check, and
// renews it with a real browser when the site starts challenging again.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// failureCooldown is how long Renew waits after a failed browser run before
// launching another, so a broken setup does not open a window on every call.
const failureCooldown = 2 * time.Minute

// Cookie is one browser cookie as stored in the session file.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"` // Unix seconds; <= 0 for a session cookie
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
}

// State is the content of the session file.
type State struct {
	Host      string    `json:"host"` // Anna's Archive hostname the cookies belong to
	UserAgent string    `json:"userAgent"`
	SavedAt   time.Time `json:"savedAt"`
	Cookies   []Cookie  `json:"cookies"`
}

// HTTPCookies converts the stored cookies for use in a cookie jar.
func (s *State) HTTPCookies() []*http.Cookie {
	out := make([]*http.Cookie, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		hc := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		}
		// A leading dot marks a cookie shared with subdomains; without it the
		// cookie is host-only, which a jar expresses as an empty Domain.
		if len(c.Domain) > 0 && c.Domain[0] == '.' {
			hc.Domain = c.Domain
		}
		if c.Expires > 0 {
			hc.Expires = time.Unix(int64(c.Expires), 0)
		}
		out = append(out, hc)
	}
	return out
}

// Solver passes the browser check for host and returns the resulting session.
type Solver func(ctx context.Context, host string) (*State, error)

// Store reads and renews the session file. It is safe for concurrent use, and
// several server processes may share one file.
type Store struct {
	path   string
	host   string
	solve  Solver // nil when browser use is disabled
	logger *zap.Logger

	mu          sync.Mutex
	loadedMod   time.Time // mtime of the file as last read or written here
	lastFailure time.Time
}

// NewStore returns a Store for the session file at path. solve may be nil, in
// which case the Store only ever reads a file written by someone else.
func NewStore(path, host string, solve Solver, logger *zap.Logger) *Store {
	return &Store{path: path, host: host, solve: solve, logger: logger}
}

// Load returns the saved session, or nil when there is none for this host.
func (s *Store) Load() *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() *State {
	info, err := os.Stat(s.path)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		s.logger.Warn("session: cannot read session file", zap.String("path", s.path), zap.Error(err))
		return nil
	}
	s.loadedMod = info.ModTime()

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		s.logger.Warn("session: ignoring corrupt session file", zap.String("path", s.path), zap.Error(err))
		return nil
	}
	if state.Host != s.host {
		return nil // saved for a different mirror; its cookies do not apply
	}
	return &state
}

// Renew returns a session newer than the one the caller holds. If another
// process has rewritten the file since it was last read, that copy is
// returned; otherwise a browser is launched to pass the check again.
func (s *Store) Renew(ctx context.Context) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state := s.newerOnDisk(); state != nil {
		s.logger.Info("session: using session renewed by another process")
		return state, nil
	}
	if s.solve == nil {
		return nil, errors.New("session: browser use is disabled (ANNAS_BROWSER=off)")
	}
	if wait := failureCooldown - time.Since(s.lastFailure); wait > 0 {
		return nil, fmt.Errorf("session: the last browser attempt failed; not retrying for another %s", wait.Round(time.Second))
	}

	state, err := s.solve(ctx, s.host)
	if err != nil {
		// Two processes racing for the same browser profile: the loser's
		// browser exits at once, but the winner's file is what we want.
		if other := s.newerOnDisk(); other != nil {
			return other, nil
		}
		if ctx.Err() == nil {
			s.lastFailure = time.Now()
		}
		return nil, err
	}

	state.Host = s.host
	state.SavedAt = time.Now().UTC()
	if err := s.save(state); err != nil {
		// The session still works for this process; it just will not persist.
		s.logger.Warn("session: could not save session file", zap.String("path", s.path), zap.Error(err))
	}
	return state, nil
}

// newerOnDisk loads the file if it changed since this Store last touched it.
func (s *Store) newerOnDisk() *State {
	info, err := os.Stat(s.path)
	if err != nil || !info.ModTime().After(s.loadedMod) {
		return nil
	}
	return s.load()
}

// save writes the file atomically with owner-only permissions: the cookies
// are credentials for the user's browser session.
func (s *Store) save(state *State) error {
	data, err := json.MarshalIndent(state, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	if info, err := os.Stat(s.path); err == nil {
		s.loadedMod = info.ModTime()
	}
	return nil
}
