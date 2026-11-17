// Package browser passes Anna's Archive's DDoS-Guard browser check with a
// real Chrome window and exports the session it earns.
//
// The check runs JavaScript and inspects the browser, so it cannot be
// answered by an HTTP client, and headless Chrome is turned away. A normal
// Chrome window passes it in a few seconds without any interaction. Chrome is
// started with its own profile directory (never the user's everyday one),
// pointed at the site, and closed again as soon as the cookies are read. It is
// driven over a private pipe (--remote-debugging-pipe), so no debugging port
// is opened.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/session"
)

const (
	// solveTimeout bounds one whole browser run.
	solveTimeout = 60 * time.Second
	// pollInterval is how often the page is checked for having passed.
	pollInterval = 500 * time.Millisecond
	// challengePath is a page the site guards with the check (its home page
	// is not guarded).
	challengePath = "/search?q=a"
)

// ErrNoChrome is returned when no Chrome or Chromium binary can be found.
var ErrNoChrome = errors.New("browser: no Chrome or Chromium found; install one or set ANNAS_CHROME_PATH")

// chromeNames are looked up on PATH, in order of preference.
var chromeNames = []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome"}

// chromePaths are well-known install locations for platforms where Chrome is
// not on PATH.
var chromePaths = map[string][]string{
	"darwin": {
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	},
}

// FindChrome returns the Chrome binary to use: override if set, otherwise the
// first one found on PATH or in a well-known location.
func FindChrome(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("browser: ANNAS_CHROME_PATH %q: %w", override, err)
		}
		return override, nil
	}
	for _, name := range chromeNames {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	for _, path := range chromePaths[runtime.GOOS] {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", ErrNoChrome
}

// Solver passes the browser check using a dedicated Chrome profile.
type Solver struct {
	// ChromePath overrides Chrome discovery when non-empty.
	ChromePath string
	// ProfileDir is Chrome's user-data directory. It is created if missing
	// and kept between runs.
	ProfileDir string
	Logger     *zap.Logger
}

// Solve opens a guarded page of host in Chrome, waits for the check to pass,
// and returns the cookies for host together with Chrome's User-Agent.
func (s *Solver) Solve(ctx context.Context, host string) (*session.State, error) {
	chrome, err := FindChrome(s.ChromePath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.ProfileDir, 0o700); err != nil {
		return nil, fmt.Errorf("browser: create profile directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, solveTimeout)
	defer cancel()

	s.Logger.Info("browser: opening Chrome to pass the DDoS-Guard check", zap.String("chrome", chrome))
	conn, err := launch(ctx, chrome, []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + s.ProfileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=900,700",
		"about:blank",
	})
	if err != nil {
		return nil, err
	}
	defer conn.close()

	state, err := solve(ctx, conn, host)
	if err != nil {
		return nil, err
	}
	s.Logger.Info("browser: DDoS-Guard check passed", zap.Int("cookies", len(state.Cookies)))
	return state, nil
}

// pageStatus is what the polling script reports about the open tab.
type pageStatus struct {
	Host      string `json:"host"`
	Title     string `json:"title"`
	Ready     string `json:"ready"`
	UserAgent string `json:"ua"`
}

const statusScript = `JSON.stringify({host: location.hostname, title: document.title, ready: document.readyState, ua: navigator.userAgent})`

// passed reports whether the tab shows the real site rather than the check.
func (p pageStatus) passed(host string) bool {
	return p.Host == host && p.Ready != "loading" && p.Title != "" &&
		!strings.Contains(strings.ToLower(p.Title), "ddos-guard")
}

// solve drives an already-running browser through the check.
func solve(ctx context.Context, conn *cdpConn, host string) (*session.State, error) {
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "https://" + host + challengePath}, &target); err != nil {
		return nil, fmt.Errorf("browser: open page: %w", err)
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return nil, fmt.Errorf("browser: attach to page: %w", err)
	}

	status, err := waitForPass(ctx, conn, attached.SessionID, host)
	if err != nil {
		return nil, err
	}

	var jar struct {
		Cookies []session.Cookie `json:"cookies"`
	}
	if err := conn.call(ctx, "", "Storage.getCookies", nil, &jar); err != nil {
		return nil, fmt.Errorf("browser: read cookies: %w", err)
	}

	state := &session.State{UserAgent: status.UserAgent}
	for _, c := range jar.Cookies {
		if cookieAppliesTo(c.Domain, host) {
			state.Cookies = append(state.Cookies, c)
		}
	}
	if len(state.Cookies) == 0 {
		return nil, errors.New("browser: the check passed but Chrome holds no cookies for the site")
	}
	return state, nil
}

// waitForPass polls the tab until it shows the real site.
func waitForPass(ctx context.Context, conn *cdpConn, sessionID, host string) (pageStatus, error) {
	var last pageStatus
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("browser: the DDoS-Guard check did not pass in time (page title %q): %w", last.Title, ctx.Err())
		case <-ticker.C:
		}

		var eval struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		err := conn.call(ctx, sessionID, "Runtime.evaluate", map[string]any{"expression": statusScript, "returnByValue": true}, &eval)
		if err != nil {
			if errors.Is(err, errBrowserClosed) || ctx.Err() != nil {
				return last, fmt.Errorf("browser: %w", err)
			}
			continue // the page is mid-navigation; ask again
		}
		if json.Unmarshal([]byte(eval.Result.Value), &last) == nil && last.passed(host) {
			return last, nil
		}
	}
}

// cookieAppliesTo reports whether a cookie with the given Domain attribute
// would be sent to host.
func cookieAppliesTo(domain, host string) bool {
	domain = strings.TrimPrefix(domain, ".")
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}
