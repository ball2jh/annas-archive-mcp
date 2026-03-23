package httpclient

import (
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logEntry records a single logged message.
type logEntry struct {
	Level   zapcore.Level
	Message string
}

// logObserver accumulates log entries for test assertions.
type logObserver struct {
	mu      sync.Mutex
	entries []logEntry
}

func (o *logObserver) add(lvl zapcore.Level, msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = append(o.entries, logEntry{Level: lvl, Message: msg})
}

// All returns a snapshot of all recorded entries.
func (o *logObserver) All() []logEntry {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]logEntry, len(o.entries))
	copy(out, o.entries)
	return out
}

// observerCore is a minimal zapcore.Core that feeds into logObserver.
type observerCore struct {
	obs   *logObserver
	level zapcore.Level
}

func (c *observerCore) Enabled(lvl zapcore.Level) bool {
	return lvl >= c.level
}

func (c *observerCore) With(_ []zapcore.Field) zapcore.Core {
	return c // stateless — fields are ignored for test purposes
}

func (c *observerCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *observerCore) Write(entry zapcore.Entry, _ []zapcore.Field) error {
	c.obs.add(entry.Level, entry.Message)
	return nil
}

func (c *observerCore) Sync() error { return nil }

// Ensure observerCore satisfies zapcore.Core at compile time.
var _ zapcore.Core = (*observerCore)(nil)

// Ensure zap import is used.
var _ = zap.DebugLevel
