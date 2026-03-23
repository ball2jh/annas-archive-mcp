// Package config loads and validates server configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"go.uber.org/zap/zapcore"
)

const (
	defaultBaseURL         = "annas-archive.gl"
	defaultHTTPTimeout     = 30 * time.Second
	defaultStatsTimeout    = 5 * time.Second
	defaultMaxRetries      = 3
	defaultMaxConcurrency  = 10
	defaultLogLevel        = "warn"
)

// Config holds all runtime configuration for the server. Fields are read-only
// after Load returns. Download-gated fields (SecretKey, DownloadPath) are
// allowed to be empty; the download logic is responsible for rejecting calls
// when those are absent.
type Config struct {
	// SecretKey is the Anna's Archive donation API key. May be empty.
	SecretKey string

	// DownloadPath is the absolute filesystem path where downloads are saved. May be empty.
	DownloadPath string

	// BaseURL is the primary mirror hostname without a protocol scheme (e.g. "annas-archive.gl").
	BaseURL string

	// HTTPTimeout is the timeout for general HTTP requests.
	HTTPTimeout time.Duration

	// StatsTimeout is the timeout used when fetching community stats.
	StatsTimeout time.Duration

	// MaxRetries is the number of retry attempts for failed HTTP requests.
	MaxRetries int

	// MaxConcurrency caps the number of parallel HTTP requests in flight.
	MaxConcurrency int

	// LogLevel is a valid zap log-level string (debug, info, warn, error, …).
	LogLevel string
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
		SecretKey:      os.Getenv("ANNAS_SECRET_KEY"),
		DownloadPath:   os.Getenv("ANNAS_DOWNLOAD_PATH"),
		BaseURL:        defaultBaseURL,
		HTTPTimeout:    defaultHTTPTimeout,
		StatsTimeout:   defaultStatsTimeout,
		MaxRetries:     defaultMaxRetries,
		MaxConcurrency: defaultMaxConcurrency,
		LogLevel:       defaultLogLevel,
	}

	if v := os.Getenv("ANNAS_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}

	if v := os.Getenv("ANNAS_HTTP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ANNAS_HTTP_TIMEOUT %q: %w", v, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("config: ANNAS_HTTP_TIMEOUT must be positive, got %q", v)
		}
		cfg.HTTPTimeout = d
	}

	if v := os.Getenv("ANNAS_STATS_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ANNAS_STATS_TIMEOUT %q: %w", v, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("config: ANNAS_STATS_TIMEOUT must be positive, got %q", v)
		}
		cfg.StatsTimeout = d
	}

	if v := os.Getenv("ANNAS_MAX_RETRIES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ANNAS_MAX_RETRIES %q: must be an integer", v)
		}
		if n <= 0 {
			return nil, fmt.Errorf("config: ANNAS_MAX_RETRIES must be a positive integer, got %d", n)
		}
		cfg.MaxRetries = n
	}

	if v := os.Getenv("ANNAS_MAX_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ANNAS_MAX_CONCURRENCY %q: must be an integer", v)
		}
		if n <= 0 {
			return nil, fmt.Errorf("config: ANNAS_MAX_CONCURRENCY must be a positive integer, got %d", n)
		}
		cfg.MaxConcurrency = n
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
