package httpclient

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"whitespace only", "   ", 0},
		{"zero", "0", 0},
		{"positive integer", "30", 30 * time.Second},
		{"hour", "3600", time.Hour},
		{"negative", "-10", 0},
		{"trailing garbage", "30sec", 0},
		{"leading garbage", "abc30", 0},
		{"http-date future", now.Add(90 * time.Second).UTC().Format(http.TimeFormat), 90 * time.Second},
		{"http-date past", now.Add(-60 * time.Second).UTC().Format(http.TimeFormat), 0},
		{"rfc850 future", now.Add(120 * time.Second).UTC().Format(time.RFC850), 120 * time.Second},
		{"garbage", "not a date", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.header, now)
			// HTTP-date parsing loses sub-second precision; allow 1s slack.
			diff := got - tc.want
			if diff < 0 {
				diff = -diff
			}
			if diff > time.Second {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestRetryWait_UsesLarger(t *testing.T) {
	cases := []struct {
		name       string
		backoff    time.Duration
		retryAfter time.Duration
		want       time.Duration
	}{
		{"no hint", 1 * time.Second, 0, 1 * time.Second},
		{"hint smaller than backoff", 5 * time.Second, 2 * time.Second, 5 * time.Second},
		{"hint larger than backoff", 1 * time.Second, 10 * time.Second, 10 * time.Second},
		{"equal", 3 * time.Second, 3 * time.Second, 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryWait(tc.backoff, tc.retryAfter); got != tc.want {
				t.Errorf("retryWait(%v, %v) = %v, want %v", tc.backoff, tc.retryAfter, got, tc.want)
			}
		})
	}
}
