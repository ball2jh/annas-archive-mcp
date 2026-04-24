package config_test

import (
	"testing"
	"time"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
)

// TestDefaults verifies that Load returns expected defaults when no env vars are set.
func TestDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.BaseURL != "annas-archive.gl" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "annas-archive.gl")
	}
	if cfg.ScinetBaseURL != "sci-net.xyz" {
		t.Errorf("ScinetBaseURL = %q, want %q", cfg.ScinetBaseURL, "sci-net.xyz")
	}
	if cfg.LibgenBaseURL != "libgen.li" {
		t.Errorf("LibgenBaseURL = %q, want %q", cfg.LibgenBaseURL, "libgen.li")
	}
	if !cfg.LibgenEnabled {
		t.Error("LibgenEnabled = false, want true")
	}
	if cfg.HTTPTimeout != 30*time.Second {
		t.Errorf("HTTPTimeout = %v, want 30s", cfg.HTTPTimeout)
	}
	if cfg.StatsTimeout != 5*time.Second {
		t.Errorf("StatsTimeout = %v, want 5s", cfg.StatsTimeout)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency = %d, want 10", cfg.MaxConcurrency)
	}
	if cfg.ToolRateLimitPerMinute != 60 {
		t.Errorf("ToolRateLimitPerMinute = %d, want 60", cfg.ToolRateLimitPerMinute)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "warn")
	}
}

// TestEmptySecretKeyAndDownloadPath confirms soft-fail behaviour for download-gated fields.
func TestEmptySecretKeyAndDownloadPath(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() with no key/path returned unexpected error: %v", err)
	}
	if cfg.SecretKey != "" {
		t.Errorf("SecretKey = %q, want empty string", cfg.SecretKey)
	}
	if cfg.DownloadPath != "" {
		t.Errorf("DownloadPath = %q, want empty string", cfg.DownloadPath)
	}
}

// TestCustomValues verifies that valid env-var overrides are applied correctly.
func TestCustomValues(t *testing.T) {
	t.Setenv("ANNAS_SECRET_KEY", "mysecret")
	t.Setenv("ANNAS_DOWNLOAD_PATH", "/tmp/books")
	t.Setenv("ANNAS_BASE_URL", "mirror.example.com")
	t.Setenv("SCINET_BASE_URL", "scinet.example.com")
	t.Setenv("LIBGEN_BASE_URL", "libgen.example.com")
	t.Setenv("LIBGEN_ENABLED", "false")
	t.Setenv("ANNAS_HTTP_TIMEOUT", "60s")
	t.Setenv("ANNAS_STATS_TIMEOUT", "10s")
	t.Setenv("ANNAS_MAX_RETRIES", "5")
	t.Setenv("ANNAS_MAX_CONCURRENCY", "20")
	t.Setenv("ANNAS_TOOL_RATE_LIMIT_PER_MINUTE", "120")
	t.Setenv("ANNAS_LOG_LEVEL", "debug")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.SecretKey != "mysecret" {
		t.Errorf("SecretKey = %q, want %q", cfg.SecretKey, "mysecret")
	}
	if cfg.DownloadPath != "/tmp/books" {
		t.Errorf("DownloadPath = %q, want %q", cfg.DownloadPath, "/tmp/books")
	}
	if cfg.BaseURL != "mirror.example.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "mirror.example.com")
	}
	if cfg.ScinetBaseURL != "scinet.example.com" {
		t.Errorf("ScinetBaseURL = %q, want %q", cfg.ScinetBaseURL, "scinet.example.com")
	}
	if cfg.LibgenBaseURL != "libgen.example.com" {
		t.Errorf("LibgenBaseURL = %q, want %q", cfg.LibgenBaseURL, "libgen.example.com")
	}
	if cfg.LibgenEnabled {
		t.Error("LibgenEnabled = true, want false")
	}
	if cfg.HTTPTimeout != 60*time.Second {
		t.Errorf("HTTPTimeout = %v, want 60s", cfg.HTTPTimeout)
	}
	if cfg.StatsTimeout != 10*time.Second {
		t.Errorf("StatsTimeout = %v, want 10s", cfg.StatsTimeout)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}
	if cfg.MaxConcurrency != 20 {
		t.Errorf("MaxConcurrency = %d, want 20", cfg.MaxConcurrency)
	}
	if cfg.ToolRateLimitPerMinute != 120 {
		t.Errorf("ToolRateLimitPerMinute = %d, want 120", cfg.ToolRateLimitPerMinute)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

// TestInvalidHTTPTimeout ensures a non-parseable timeout returns an error.
func TestInvalidHTTPTimeout(t *testing.T) {
	t.Setenv("ANNAS_HTTP_TIMEOUT", "notaduration")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for invalid ANNAS_HTTP_TIMEOUT, got nil")
	}
}

// TestNegativeHTTPTimeout ensures a non-positive timeout returns an error.
func TestNegativeHTTPTimeout(t *testing.T) {
	t.Setenv("ANNAS_HTTP_TIMEOUT", "-5s")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for negative ANNAS_HTTP_TIMEOUT, got nil")
	}
}

// TestZeroHTTPTimeout ensures a zero timeout returns an error.
func TestZeroHTTPTimeout(t *testing.T) {
	t.Setenv("ANNAS_HTTP_TIMEOUT", "0s")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for zero ANNAS_HTTP_TIMEOUT, got nil")
	}
}

// TestInvalidStatsTimeout ensures a non-parseable stats timeout returns an error.
func TestInvalidStatsTimeout(t *testing.T) {
	t.Setenv("ANNAS_STATS_TIMEOUT", "banana")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for invalid ANNAS_STATS_TIMEOUT, got nil")
	}
}

// TestNegativeStatsTimeout ensures a non-positive stats timeout returns an error.
func TestNegativeStatsTimeout(t *testing.T) {
	t.Setenv("ANNAS_STATS_TIMEOUT", "-1s")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for negative ANNAS_STATS_TIMEOUT, got nil")
	}
}

// TestInvalidMaxRetries ensures a non-integer retry count returns an error.
func TestInvalidMaxRetries(t *testing.T) {
	t.Setenv("ANNAS_MAX_RETRIES", "abc")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for non-integer ANNAS_MAX_RETRIES, got nil")
	}
}

// TestZeroMaxRetries ensures a zero retry count returns an error.
func TestZeroMaxRetries(t *testing.T) {
	t.Setenv("ANNAS_MAX_RETRIES", "0")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for zero ANNAS_MAX_RETRIES, got nil")
	}
}

// TestNegativeMaxRetries ensures a negative retry count returns an error.
func TestNegativeMaxRetries(t *testing.T) {
	t.Setenv("ANNAS_MAX_RETRIES", "-1")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for negative ANNAS_MAX_RETRIES, got nil")
	}
}

// TestInvalidMaxConcurrency ensures a non-integer concurrency cap returns an error.
func TestInvalidMaxConcurrency(t *testing.T) {
	t.Setenv("ANNAS_MAX_CONCURRENCY", "notanumber")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for non-integer ANNAS_MAX_CONCURRENCY, got nil")
	}
}

// TestZeroMaxConcurrency ensures a zero concurrency cap returns an error.
func TestZeroMaxConcurrency(t *testing.T) {
	t.Setenv("ANNAS_MAX_CONCURRENCY", "0")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for zero ANNAS_MAX_CONCURRENCY, got nil")
	}
}

// TestNegativeMaxConcurrency ensures a negative concurrency cap returns an error.
func TestNegativeMaxConcurrency(t *testing.T) {
	t.Setenv("ANNAS_MAX_CONCURRENCY", "-3")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for negative ANNAS_MAX_CONCURRENCY, got nil")
	}
}

func TestInvalidToolRateLimit(t *testing.T) {
	t.Setenv("ANNAS_TOOL_RATE_LIMIT_PER_MINUTE", "notanumber")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for non-integer ANNAS_TOOL_RATE_LIMIT_PER_MINUTE, got nil")
	}
}

func TestNegativeToolRateLimit(t *testing.T) {
	t.Setenv("ANNAS_TOOL_RATE_LIMIT_PER_MINUTE", "-1")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for negative ANNAS_TOOL_RATE_LIMIT_PER_MINUTE, got nil")
	}
}

func TestZeroToolRateLimitDisablesLimiter(t *testing.T) {
	t.Setenv("ANNAS_TOOL_RATE_LIMIT_PER_MINUTE", "0")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.ToolRateLimitPerMinute != 0 {
		t.Errorf("ToolRateLimitPerMinute = %d, want 0", cfg.ToolRateLimitPerMinute)
	}
}

func TestInvalidLibgenEnabled(t *testing.T) {
	t.Setenv("LIBGEN_ENABLED", "sometimes")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for invalid LIBGEN_ENABLED, got nil")
	}
}

// TestInvalidLogLevel ensures an unrecognised log level returns an error.
func TestInvalidLogLevel(t *testing.T) {
	t.Setenv("ANNAS_LOG_LEVEL", "verbose")
	_, err := config.Load()
	if err == nil {
		t.Error("Load() expected error for invalid ANNAS_LOG_LEVEL, got nil")
	}
}

// TestValidLogLevels ensures all supported zap log levels are accepted.
func TestValidLogLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error", "dpanic", "panic", "fatal"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			t.Setenv("ANNAS_LOG_LEVEL", level)
			cfg, err := config.Load()
			if err != nil {
				t.Errorf("Load() with log level %q returned unexpected error: %v", level, err)
			}
			if cfg != nil && cfg.LogLevel != level {
				t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, level)
			}
		})
	}
}

// TestDurationFormats verifies that various valid duration formats are accepted.
func TestDurationFormats(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"1m30s", 90 * time.Second},
		{"2m", 2 * time.Minute},
		{"500ms", 500 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("ANNAS_HTTP_TIMEOUT", tc.env)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load() returned unexpected error for duration %q: %v", tc.env, err)
			}
			if cfg.HTTPTimeout != tc.want {
				t.Errorf("HTTPTimeout = %v, want %v", cfg.HTTPTimeout, tc.want)
			}
		})
	}
}
