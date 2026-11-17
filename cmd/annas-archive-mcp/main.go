// Command annas-archive-mcp is an MCP server, speaking over stdio, for
// searching and downloading from Anna's Archive.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ball2jh/annas-archive-mcp/internal/browser"
	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/server"
	"github.com/ball2jh/annas-archive-mcp/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at release time with
// -ldflags "-X main.version=v1.2.3". Builds made with `go install …@vX.Y.Z`
// fall back to the module version recorded in the binary.
var version = ""

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildVersion())
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("initialise logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck // stderr sync errors are not actionable

	// Search / details / doi work without download config. Downloads require
	// ANNAS_DOWNLOAD_PATH; ANNAS_SECRET_KEY enables Anna's fast_download tier,
	// but Libgen and Sci-Net fallbacks can still work without it.
	if cfg.SecretKey == "" {
		logger.Warn("ANNAS_SECRET_KEY is not set — Anna's fast_download tier will be skipped")
	}
	if cfg.DownloadPath == "" {
		logger.Warn("ANNAS_DOWNLOAD_PATH is not set — download tools will fail until this is configured")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := httpclient.New(cfg, logger)
	enableBrowserSession(cfg, client, logger)
	srv := server.New(cfg, client, logger, buildVersion())

	// Run returns when the client closes stdin or a signal cancels ctx; both
	// are normal shutdowns.
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("server exited: %w", err)
	}
	return nil
}

// enableBrowserSession lets the client through Anna's Archive's DDoS-Guard
// browser check: it applies the session saved by an earlier run and, when the
// site challenges anyway, renews it by briefly opening Chrome.
func enableBrowserSession(cfg *config.Config, client *httpclient.Client, logger *zap.Logger) {
	if cfg.SessionFile == "" {
		logger.Warn("no user config directory — browser sessions are disabled; set ANNAS_SESSION_FILE to enable them")
		return
	}

	var solve session.Solver
	if cfg.BrowserEnabled {
		solver := &browser.Solver{
			ChromePath: cfg.ChromePath,
			ProfileDir: filepath.Join(filepath.Dir(cfg.SessionFile), "chrome-profile"),
			Logger:     logger,
		}
		solve = solver.Solve
	}
	store := session.NewStore(cfg.SessionFile, cfg.BaseURL, solve, logger)

	apply := func(state *session.State) httpclient.Session {
		return httpclient.Session{UserAgent: state.UserAgent, Cookies: state.HTTPCookies()}
	}
	if state := store.Load(); state != nil {
		client.UseSession(apply(state))
	}
	client.OnChallenge(func(ctx context.Context) (httpclient.Session, error) {
		state, err := store.Renew(ctx)
		if err != nil {
			return httpclient.Session{}, err
		}
		return apply(state), nil
	})
}

// buildVersion returns the release version, falling back to the module
// version embedded by the Go toolchain, then to "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// newLogger creates a zap.Logger configured for stderr at the given level.
// MCP servers communicate over stdio, so all application logs go to stderr.
func newLogger(level string) (*zap.Logger, error) {
	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	// Errors here are handled upstream failures, not crashes; a stack trace
	// on each one only buries the message.
	cfg.DisableStacktrace = true
	cfg.OutputPaths = []string{"stderr"}
	cfg.ErrorOutputPaths = []string{"stderr"}

	return cfg.Build()
}
