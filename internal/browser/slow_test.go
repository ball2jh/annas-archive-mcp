package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowPage scripts what the fake tab shows.
type slowPage struct {
	firstManual bool   // the file's own page is stuck on the manual check
	pick        string // what the file's page offers as a slow-download path
	manualPolls int    // status checks that show the manual check first
	link        string // the link the partner page finally shows ("" = never)

	mu        sync.Mutex
	navigated string
}

// fakeSlowChrome answers the DevTools commands slowLink issues.
func fakeSlowChrome(t *testing.T, host string, page *slowPage) *cdpConn {
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
				Params struct {
					Expression string `json:"expression"`
					URL        string `json:"url"`
				} `json:"params"`
			}
			if json.Unmarshal(frame[:len(frame)-1], &cmd) != nil {
				return
			}

			str := func(v string) string {
				b, _ := json.Marshal(v)
				return `{"result":{"type":"string","value":` + string(b) + `}}`
			}
			result := `{}`
			switch {
			case cmd.Method == "Target.createTarget":
				result = `{"targetId":"T1"}`
			case cmd.Method == "Target.attachToTarget":
				result = `{"sessionId":"S1"}`
			case cmd.Method == "Page.navigate":
				page.mu.Lock()
				page.navigated = cmd.Params.URL
				page.mu.Unlock()
			case cmd.Params.Expression == statusScript:
				first := pageStatus{Host: host, Title: "A Book - Anna's Archive", Ready: "complete", UserAgent: "BrowserUA/1.0"}
				if page.firstManual {
					first.Title, first.Manual = "DDOS-GUARD", true
				}
				status, _ := json.Marshal(first)
				result = str(string(status))
			case cmd.Params.Expression == pickSlowScript:
				result = str(page.pick)
			case cmd.Params.Expression == slowStatusScript:
				polls++
				status := slowStatus{Path: page.pick, Title: "Download from partner website"}
				if polls <= page.manualPolls {
					status.Title, status.Manual = "DDOS-GUARD", true
				} else {
					status.Link = page.link
				}
				b, _ := json.Marshal(status)
				result = str(string(b))
			}
			_, _ = io.WriteString(replyW, `{"id":`+itoa(cmd.ID)+`,"result":`+result+`}`+"\x00")
		}
	}()

	conn := newCDPConn(replyR, cmdW, func() { _ = cmdR.Close() })
	t.Cleanup(conn.close)
	return conn
}

const (
	slowHost = "annas-archive.example"
	slowHash = "0123456789abcdef0123456789abcdef"
	slowPath = "/slow_download/" + slowHash + "/0/8"
)

func TestSlowLinkWaitsOutTheCheckAndReturnsThePartnerLink(t *testing.T) {
	page := &slowPage{pick: slowPath, manualPolls: 2, link: "https://partner.example/d/file.epub"}
	conn := fakeSlowChrome(t, slowHost, page)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	link, err := slowLink(ctx, conn, slowHost, slowHash)
	if err != nil {
		t.Fatalf("slowLink: %v", err)
	}
	if link != page.link {
		t.Errorf("link = %q, want %q", link, page.link)
	}
	page.mu.Lock()
	defer page.mu.Unlock()
	if want := "https://" + slowHost + slowPath; page.navigated != want {
		t.Errorf("navigated to %q, want %q", page.navigated, want)
	}
}

func TestSlowLinkReportsAManualCheckNobodyCompleted(t *testing.T) {
	conn := fakeSlowChrome(t, slowHost, &slowPage{pick: slowPath, manualPolls: 1 << 30})

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := slowLink(ctx, conn, slowHost, slowHash)
	if !errors.Is(err, ErrNeedsPerson) {
		t.Fatalf("error = %v, want ErrNeedsPerson", err)
	}
}

// The file's own page can demand the manual check too, before the
// slow-download page is ever reached.
func TestSlowLinkReportsAManualCheckOnTheFilePage(t *testing.T) {
	conn := fakeSlowChrome(t, slowHost, &slowPage{firstManual: true, pick: slowPath})

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := slowLink(ctx, conn, slowHost, slowHash)
	if !errors.Is(err, ErrNeedsPerson) {
		t.Fatalf("error = %v, want ErrNeedsPerson", err)
	}
}

func TestSlowLinkWithoutAServer(t *testing.T) {
	conn := fakeSlowChrome(t, slowHost, &slowPage{pick: ""})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := slowLink(ctx, conn, slowHost, slowHash)
	if !errors.Is(err, ErrNoSlowServer) {
		t.Fatalf("error = %v, want ErrNoSlowServer", err)
	}
}

// Only a link that leaves the site for a partner server is a download link.
func TestPartnerLink(t *testing.T) {
	cases := map[string]bool{
		"https://partner.example/d/file.epub": true,
		"http://203.0.113.7:6060/d/file.epub": true,
		"https://" + slowHost + "/md5/abc":    false,
		"javascript:alert(1)":                 false,
		"ftp://partner.example/file.epub":     false,
		"":                                    false,
		"https:///nohost":                     false,
		strings.Repeat("%", 3):                false,
	}
	for raw, want := range cases {
		if _, ok := partnerLink(raw, slowHost); ok != want {
			t.Errorf("partnerLink(%q) = %v, want %v", raw, ok, want)
		}
	}
}
