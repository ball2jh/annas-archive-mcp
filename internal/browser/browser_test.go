package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeChrome answers DevTools commands over in-memory pipes. The tab reports
// the challenge page for the first challengePolls status checks.
func fakeChrome(t *testing.T, host string, challengePolls int, cookies string) *cdpConn {
	t.Helper()
	cmdR, cmdW := io.Pipe()
	replyR, replyW := io.Pipe()

	go func() {
		defer replyW.Close()
		reader := bufio.NewReader(cmdR)
		polls := 0
		for {
			frame, err := reader.ReadBytes(0)
			if err != nil {
				return
			}
			var cmd struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(frame[:len(frame)-1], &cmd) != nil {
				return
			}

			result := `{}`
			switch cmd.Method {
			case "Target.createTarget":
				result = `{"targetId":"T1"}`
			case "Target.attachToTarget":
				result = `{"sessionId":"S1"}`
			case "Runtime.evaluate":
				polls++
				title := "DDoS-Guard"
				if polls > challengePolls {
					title = "a - Search - Anna's Archive"
				}
				status, _ := json.Marshal(pageStatus{Host: host, Title: title, Ready: "complete", UserAgent: "BrowserUA/1.0"})
				value, _ := json.Marshal(string(status))
				result = `{"result":{"type":"string","value":` + string(value) + `}}`
			case "Storage.getCookies":
				result = `{"cookies":` + cookies + `}`
			}
			// An event first: the reader must skip messages without an id.
			_, _ = io.WriteString(replyW, `{"method":"Target.targetInfoChanged","params":{}}`+"\x00")
			_, _ = io.WriteString(replyW, `{"id":`+itoa(cmd.ID)+`,"result":`+result+`}`+"\x00")
		}
	}()

	conn := newCDPConn(replyR, cmdW, func() { _ = cmdR.Close() })
	t.Cleanup(conn.close)
	return conn
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestSolveWaitsForCheckAndKeepsOnlySiteCookies(t *testing.T) {
	const host = "annas-archive.example"
	cookies := `[
		{"name":"aa_ddg_check","value":"v1","domain":".annas-archive.example","path":"/","expires":1900000000,"secure":true,"httpOnly":true},
		{"name":"hostonly","value":"v2","domain":"annas-archive.example","path":"/","expires":-1},
		{"name":"__ddg2","value":"v3","domain":".check.ddos-guard.net","path":"/","expires":-1},
		{"name":"lookalike","value":"v4","domain":".evil-annas-archive.example","path":"/","expires":-1}
	]`
	conn := fakeChrome(t, host, 2, cookies)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := solve(ctx, conn, host)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if state.UserAgent != "BrowserUA/1.0" {
		t.Errorf("UserAgent = %q, want the browser's", state.UserAgent)
	}
	var names []string
	for _, c := range state.Cookies {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "aa_ddg_check,hostonly" {
		t.Errorf("cookies = %s, want only the site's own", got)
	}
	if c := state.Cookies[0]; c.Value != "v1" || !c.Secure || !c.HTTPOnly || c.Expires != 1900000000 {
		t.Errorf("cookie = %+v, want every attribute carried over", c)
	}
}

func TestSolveGivesUpWhenCheckNeverPasses(t *testing.T) {
	conn := fakeChrome(t, "annas-archive.example", 1<<30, `[]`)

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	_, err := solve(ctx, conn, "annas-archive.example")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline error", err)
	}
	if !strings.Contains(err.Error(), "DDoS-Guard") {
		t.Errorf("error %q should name the page it was stuck on", err)
	}
}

func TestCallFailsOnceBrowserIsGone(t *testing.T) {
	replyR, replyW := io.Pipe()
	_, cmdW := io.Pipe()
	conn := newCDPConn(replyR, cmdW, func() {})
	_ = replyW.Close() // Chrome exits

	// The read loop notices asynchronously; wait for it so the test checks
	// the settled state.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn.mu.Lock()
		closed := conn.closed
		conn.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := conn.call(ctx, "", "Browser.getVersion", nil, nil); !errors.Is(err, errBrowserClosed) {
		t.Fatalf("error = %v, want errBrowserClosed", err)
	}
}

func TestFindChromeOverrideMustExist(t *testing.T) {
	if _, err := FindChrome("/nonexistent/chrome"); err == nil {
		t.Error("FindChrome accepted a path that does not exist")
	}
	self := t.TempDir()
	if got, err := FindChrome(self); err != nil || got != self {
		t.Errorf("FindChrome(%q) = %q, %v; want the override back", self, got, err)
	}
}
