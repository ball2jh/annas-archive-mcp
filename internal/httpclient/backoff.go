package httpclient

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// backoffOverride is a test hook. When non-zero, backoffDuration returns this
// value instead of the normal exponential schedule. It is only mutated by
// tests (overrideBackoffForTest).
var backoffOverride time.Duration

// backoffDuration returns the wait time before retry attempt n (zero-indexed).
// n=0 → ~1 s, n=1 → ~2 s, n=2 → ~4 s, … plus up to jitterMax random jitter.
func backoffDuration(n int) time.Duration {
	if backoffOverride != 0 {
		return backoffOverride
	}
	base := baseBackoff << n // 1s * 2^n
	jitter := time.Duration(rand.Int64N(int64(jitterMax)))
	return base + jitter
}

// parseRetryAfter parses a Retry-After header per RFC 7231 §7.1.3. It accepts
// either a non-negative integer number of seconds or an HTTP-date. Returns 0
// for empty input, negative values, already-past dates, or anything
// unparseable. The `now` argument is used when the header is a date; pass
// time.Now() in production (a parameter makes the function testable).
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	// Integer seconds — strict parse, no trailing garbage.
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// HTTP-date — try the three formats allowed by RFC 7231.
	for _, layout := range []string{http.TimeFormat, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, header); err == nil {
			if d := t.Sub(now); d > 0 {
				return d
			}
			return 0
		}
	}
	return 0
}

// retryWait combines an exponential-backoff duration with an optional server
// Retry-After hint. Returns the larger of the two (we must wait at least as
// long as the server asked, but never less than the backoff).
func retryWait(backoff, retryAfter time.Duration) time.Duration {
	if retryAfter > backoff {
		return retryAfter
	}
	return backoff
}
