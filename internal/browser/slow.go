package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// slowTimeout bounds one slow-download lookup. It is generous because a
// waitlisted server counts down before showing its link, and because a person
// may need time to complete a manual check.
const slowTimeout = 3 * time.Minute

var (
	// ErrNeedsPerson is returned when the site asks for a manual check (a
	// captcha) and nobody completes it in the Chrome window. Once a person
	// has, the profile is let through unattended for a while.
	ErrNeedsPerson = errors.New("browser: the site asked for a manual check that nobody completed")

	// ErrNoSlowServer is returned when a file's page lists no slow-download
	// server.
	ErrNoSlowServer = errors.New("browser: the file's page lists no slow-download server")
)

// pickSlowScript returns the path of the slow-download server to use: one
// without a waitlist when there is one, otherwise the first listed.
const pickSlowScript = `(() => {
  const links = [...document.querySelectorAll('a[href^="/slow_download/"]')];
  const note = a => ((a.closest('li') || a.parentElement || a).textContent || '');
  const pick = links.find(a => /no waitlist/i.test(note(a))) || links[0];
  return pick ? pick.getAttribute('href') : '';
})()`

// slowStatusScript reports what the slow-download tab shows: the check, a
// manual check, or the partner page with its download link.
const slowStatusScript = `JSON.stringify((() => {
  const link = [...document.querySelectorAll('a[href^="http"]')].find(a =>
    /download now/i.test(a.innerText) && new URL(a.href).hostname !== location.hostname);
  return {
    path: location.pathname,
    title: document.title,
    manual: ` + manualCheckJS + `,
    link: link ? link.href : ''
  };
})())`

// slowStatus is what slowStatusScript reports.
type slowStatus struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Manual bool   `json:"manual"`
	Link   string `json:"link"`
}

// SlowLink opens the slow-download page for hash in Chrome and returns the
// partner-server link it hands out. Unlike the ordinary pages, a slow-download
// page runs the browser check afresh on every visit, so a saved session cannot
// stand in for the browser here.
func (s *Solver) SlowLink(ctx context.Context, host, hash string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, slowTimeout)
	defer cancel()

	s.Logger.Info("browser: opening Chrome for a slow-download link")
	conn, err := s.open(ctx)
	if err != nil {
		return "", err
	}
	defer conn.close()

	return slowLink(ctx, conn, host, hash)
}

// slowLink drives an already-running browser to a partner-server link.
func slowLink(ctx context.Context, conn *cdpConn, host, hash string) (string, error) {
	sessionID, err := openTab(ctx, conn, "https://"+host+"/md5/"+hash)
	if err != nil {
		return "", err
	}
	if _, err := waitForPass(ctx, conn, sessionID, host); err != nil {
		return "", err
	}

	path, err := evalString(ctx, conn, sessionID, pickSlowScript)
	if err != nil {
		return "", fmt.Errorf("browser: read the file's page: %w", err)
	}
	if !strings.HasPrefix(path, "/slow_download/") {
		return "", ErrNoSlowServer
	}
	if err := conn.call(ctx, sessionID, "Page.navigate", map[string]any{"url": "https://" + host + path}, nil); err != nil {
		return "", fmt.Errorf("browser: open the slow-download page: %w", err)
	}

	var last slowStatus
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if last.Manual {
				return "", fmt.Errorf("%w: %w", ErrNeedsPerson, ctx.Err())
			}
			return "", fmt.Errorf("browser: the slow-download page gave no link in time (page title %q): %w", last.Title, ctx.Err())
		case <-ticker.C:
		}

		raw, err := evalString(ctx, conn, sessionID, slowStatusScript)
		if err != nil {
			if errors.Is(err, errBrowserClosed) {
				return "", fmt.Errorf("browser: %w", err)
			}
			continue // mid-navigation or out of time; the deadline case reports the latter
		}
		if json.Unmarshal([]byte(raw), &last) != nil || !strings.HasPrefix(last.Path, "/slow_download/") {
			continue
		}
		if link, ok := partnerLink(last.Link, host); ok {
			return link, nil
		}
	}
}

// partnerLink accepts a download link only when it is an http(s) URL on a
// host other than the site itself.
func partnerLink(raw, host string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.Hostname() == host {
		return "", false
	}
	return u.String(), true
}

// openTab opens url in a new tab and returns the session that addresses it.
func openTab(ctx context.Context, conn *cdpConn, url string) (string, error) {
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := conn.call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &target); err != nil {
		return "", fmt.Errorf("browser: open page: %w", err)
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return "", fmt.Errorf("browser: attach to page: %w", err)
	}
	return attached.SessionID, nil
}

// evalString evaluates a JavaScript expression that yields a string.
func evalString(ctx context.Context, conn *cdpConn, sessionID, expression string) (string, error) {
	var eval struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	err := conn.call(ctx, sessionID, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &eval)
	return eval.Result.Value, err
}
