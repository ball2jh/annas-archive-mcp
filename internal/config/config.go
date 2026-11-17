// Package config loads and validates server configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
)

const (
	defaultBaseURL        = "annas-archive.gl"
	defaultScinetBaseURL  = "sci-net.xyz"
	defaultLibgenBaseURL  = "libgen.li"
	defaultHTTPTimeout    = 30 * time.Second
	defaultStatsTimeout   = 5 * time.Second
	defaultMaxRetries     = 3
	defaultMaxConcurrency = 10
	defaultToolRateLimit  = 60
	defaultLogLevel       = "warn"
)

// Config holds all runtime configuration for the server. Fields are read-only
// after Load returns. Download-gated fields (SecretKey, DownloadPath) are
// allowed to be empty; the download logic is responsible for rejecting calls
// when those are absent.
type Config struct {
	// SecretKey is the Anna's Archive donation API key. May be empty.
	SecretKey string

	// DownloadPath is the absolute filesystem path where downloads are saved.
	// May be empty. A leading "~" and relative paths are resolved by Load.
	DownloadPath string

	// BaseURL is the primary mirror hostname without a protocol scheme (e.g. "annas-archive.gl").
	BaseURL string

	// ScinetBaseURL is the Sci-Net hostname used as a fallback PDF source for
	// download_by_doi. Without a protocol scheme. Default "sci-net.xyz".
	ScinetBaseURL string

	// LibgenBaseURL is the Libgen mirror hostname used as a fallback for the
	// download tool when Anna's fast_download cannot deliver the file. Without
	// a protocol scheme. Default "libgen.li". Alternatives: libgen.rs, libgen.is.
	LibgenBaseURL string

	// LibgenEnabled controls whether the libgen.li fallback is attempted when
	// Anna's fast_download fails (or when ANNAS_SECRET_KEY is absent).
	// Default true. Set LIBGEN_ENABLED=false to disable.
	LibgenEnabled bool

	// HTTPTimeout is how long a request may go without progress (waiting for
	// response headers, or between chunks of a body) before it is abandoned.
	// It is not a cap on total transfer time, so large downloads are fine.
	HTTPTimeout time.Duration

	// StatsTimeout bounds the supplementary community-stats lookups; results
	// are returned without stats when it elapses.
	StatsTimeout time.Duration

	// MaxRetries is the number of retry attempts for failed HTTP requests.
	MaxRetries int

	// MaxConcurrency caps the number of parallel community-stats requests.
	MaxConcurrency int

	// ToolRateLimitPerMinute caps each tool's local invocation rate. Zero disables the limiter.
	ToolRateLimitPerMinute int

	// LogLevel is a valid zap log-level string (debug, info, warn, error, …).
	LogLevel string

	// BrowserEnabled allows a Chrome window to be opened briefly to pass
	// Anna's Archive's DDoS-Guard browser check when the site demands it.
	// Default true. Set ANNAS_BROWSER=off to disable.
	BrowserEnabled bool

	// ChromePath overrides Chrome/Chromium discovery. May be empty.
	ChromePath string

	// SessionFile is where the browser session (cookies, User-Agent) earned
	// by passing the check is saved. Its directory also holds the dedicated
	// Chrome profile. Empty only when no user config directory can be found.
	SessionFile string
}

// Load reads configuration from environment variables, applies defaults for
// optional variables, and validates values. It returns an error only when a
// supplied value is structurally invalid (e.g. a timeout that cannot be parsed
// as a duration, a non-integer retry count, an unrecognised log level).
// Missing optional variables are silently replaced by defaults; missing
// download-gated variables (ANNAS_SECRET_KEY, ANNAS_DOWNLOAD_PATH) are accepted
// as empty strings.
func Load() (*Config, error) {
	cfg := &Config{
		SecretKey:              strings.TrimSpace(os.Getenv("ANNAS_SECRET_KEY")),
		BaseURL:                defaultBaseURL,
		ScinetBaseURL:          defaultScinetBaseURL,
		LibgenBaseURL:          defaultLibgenBaseURL,
		LibgenEnabled:          true,
		HTTPTimeout:            defaultHTTPTimeout,
		StatsTimeout:           defaultStatsTimeout,
		MaxRetries:             defaultMaxRetries,
		MaxConcurrency:         defaultMaxConcurrency,
		ToolRateLimitPerMinute: defaultToolRateLimit,
		LogLevel:               defaultLogLevel,
		BrowserEnabled:         true,
		ChromePath:             strings.TrimSpace(os.Getenv("ANNAS_CHROME_PATH")),
	}

	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("ANNAS_BROWSER"))); v {
	case "", "auto", "on":
	case "off":
		cfg.BrowserEnabled = false
	default:
		return nil, fmt.Errorf("config: invalid ANNAS_BROWSER %q: must be auto or off", v)
	}

	cfg.SessionFile = strings.TrimSpace(os.Getenv("ANNAS_SESSION_FILE"))
	if cfg.SessionFile == "" {
		// Best effort: without a config directory the session simply is not
		// kept between runs.
		if dir, err := os.UserConfigDir(); err == nil {
			cfg.SessionFile = filepath.Join(dir, "annas-archive-mcp", "session.json")
		}
	}

	var err error
	if cfg.DownloadPath, err = downloadPath(os.Getenv("ANNAS_DOWNLOAD_PATH")); err != nil {
		return nil, err
	}

	for _, h := range []struct {
		name string
		dst  *string
	}{
		{"ANNAS_BASE_URL", &cfg.BaseURL},
		{"SCINET_BASE_URL", &cfg.ScinetBaseURL},
		{"LIBGEN_BASE_URL", &cfg.LibgenBaseURL},
	} {
		if err := loadHost(h.name, h.dst); err != nil {
			return nil, err
		}
	}

	if v := os.Getenv("LIBGEN_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid LIBGEN_ENABLED %q: must be a bool (true/false/1/0)", v)
		}
		cfg.LibgenEnabled = b
	}

	if err := loadPositiveDuration("ANNAS_HTTP_TIMEOUT", &cfg.HTTPTimeout); err != nil {
		return nil, err
	}
	if err := loadPositiveDuration("ANNAS_STATS_TIMEOUT", &cfg.StatsTimeout); err != nil {
		return nil, err
	}

	if err := loadInt("ANNAS_MAX_RETRIES", 1, &cfg.MaxRetries); err != nil {
		return nil, err
	}
	if err := loadInt("ANNAS_MAX_CONCURRENCY", 1, &cfg.MaxConcurrency); err != nil {
		return nil, err
	}
	if err := loadInt("ANNAS_TOOL_RATE_LIMIT_PER_MINUTE", 0, &cfg.ToolRateLimitPerMinute); err != nil {
		return nil, err
	}

	if v := os.Getenv("ANNAS_LOG_LEVEL"); v != "" {
		var lvl zapcore.Level
		if err := lvl.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("config: invalid ANNAS_LOG_LEVEL %q: must be one of debug, info, warn, error, dpanic, panic, fatal", v)
		}
		cfg.LogLevel = v
	}

	return cfg, nil
}

// downloadPath normalises ANNAS_DOWNLOAD_PATH. MCP clients pass env values
// verbatim (no shell), so a leading "~" is expanded here, and a relative path
// is anchored to the working directory once at startup rather than drifting.
func downloadPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: cannot expand ~ in ANNAS_DOWNLOAD_PATH: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("config: invalid ANNAS_DOWNLOAD_PATH %q: %w", raw, err)
	}
	return abs, nil
}

// loadHost overrides *dst with the hostname in the named variable, if set.
// A pasted URL ("https://host/") is reduced to its host; anything with a path
// is rejected because requests are built as https://{host}{path}.
func loadHost(name string, dst *string) error {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	host := strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
	host = strings.TrimRight(host, "/")
	if host == "" || strings.ContainsAny(host, "/?#@ \t") {
		return fmt.Errorf("config: invalid %s %q: must be a bare hostname such as %q", name, v, *dst)
	}
	*dst = host
	return nil
}

// loadPositiveDuration overrides *dst with the duration in the named
// variable, if set.
func loadPositiveDuration(name string, dst *time.Duration) error {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("config: invalid %s %q: %w", name, v, err)
	}
	if d <= 0 {
		return fmt.Errorf("config: %s must be positive, got %q", name, v)
	}
	*dst = d
	return nil
}

// loadInt overrides *dst with the integer in the named variable, if set,
// rejecting values below lowest.
func loadInt(name string, lowest int, dst *int) error {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("config: invalid %s %q: must be an integer", name, v)
	}
	if n < lowest {
		return fmt.Errorf("config: %s must be at least %d, got %d", name, lowest, n)
	}
	*dst = n
	return nil
}
