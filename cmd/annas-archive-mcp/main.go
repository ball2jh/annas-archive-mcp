package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}

	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: failed to initialise logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	client := httpclient.New(cfg, logger)
	srv := server.New(cfg, client, logger)

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		logger.Fatal("server exited with error", zap.Error(err))
	}
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
	cfg.OutputPaths = []string{"stderr"}
	cfg.ErrorOutputPaths = []string{"stderr"}

	return cfg.Build()
}
