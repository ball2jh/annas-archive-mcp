package httpclient

import (
	"math/rand/v2"
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
