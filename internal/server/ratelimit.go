package server

import (
	"sync"
	"time"

	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	start    time.Time
	used     int
	now      func() time.Time
	toolName string
}

func newRateLimiter(toolName string, limitPerMinute int) *rateLimiter {
	return &rateLimiter{
		limit:    limitPerMinute,
		window:   time.Minute,
		now:      time.Now,
		toolName: toolName,
	}
}

func (l *rateLimiter) allow() error {
	if l == nil || l.limit <= 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if l.start.IsZero() || now.Sub(l.start) >= l.window {
		l.start = now
		l.used = 0
	}

	if l.used >= l.limit {
		resetIn := l.window - now.Sub(l.start)
		return usererror.New("LOCAL_RATE_LIMITED",
			"Local rate limit exceeded for "+l.toolName+". Retry after "+resetIn.Round(time.Second).String()+".")
	}

	l.used++
	return nil
}
