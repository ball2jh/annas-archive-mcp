package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/libgen"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

var hashRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// Download fetches a file by MD5 hash and saves it atomically under
// cfg.DownloadPath. It tries two sources in order:
//
//  1. Anna's Archive fast_download API (requires ANNAS_SECRET_KEY).
//  2. Libgen.li (no key required; opt out with LIBGEN_ENABLED=false).
//
// On success, the returned DownloadResult.Source records which tier delivered
// the file ("fast_download", "libgen.li", or "cache" if skip-if-exists fired).
//
// Errors are always *usererror.Error (directly or via Unwrap) so the MCP
// tool-handler boundary can render structured, actionable messages.
func Download(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	hash, title, format string,
) (*model.DownloadResult, error) {
	// --- Input validation (bubble up; no fallback can help) ---

	if cfg.DownloadPath == "" {
		return nil, usererror.New("PATH_REQUIRED",
			"Downloads require a download path. Set ANNAS_DOWNLOAD_PATH in your MCP server configuration.")
	}
	if !hashRE.MatchString(hash) {
		return nil, usererror.New("INVALID_HASH", "hash must be a 32-character MD5 hex string.")
	}
	info, err := os.Stat(cfg.DownloadPath)
	if err != nil {
		return nil, usererror.Wrap("PATH_UNAVAILABLE",
			"ANNAS_DOWNLOAD_PATH is not accessible. Check that the directory exists and Codex can write to it.", err)
	}
	if !info.IsDir() {
		return nil, usererror.New("PATH_UNAVAILABLE", "ANNAS_DOWNLOAD_PATH must be a directory.")
	}

	filename := SanitizeFilename(title, format)
	if title == "" {
		filename = SanitizeFilename(hash, format)
	}
	finalPath := filepath.Join(cfg.DownloadPath, filename)

	// --- Skip-if-exists: preserves API quota on every tier ---

	if fi, err := os.Stat(finalPath); err == nil && !fi.IsDir() && fi.Size() > 0 {
		logger.Info("download skipped — file already exists",
			zap.String("path", finalPath), zap.Int64("size", fi.Size()))
		return &model.DownloadResult{
			FilePath:       finalPath,
			Message:        "File already exists — download skipped.",
			AlreadyExisted: true,
			Source:         "cache",
		}, nil
	}

	// --- Tier 1: Anna's Archive fast_download (when key available) ---

	var annaErr error
	if cfg.SecretKey != "" {
		body, err := fetchFromAnna(ctx, client, logger, cfg, hash)
		if err == nil {
			return writeAndResult(cfg.DownloadPath, filename, body, "fast_download", logger)
		}
		annaErr = err
		logger.Info("download: fast_download failed, evaluating fallback",
			zap.String("hash", hash), zap.Error(err))
	} else {
		logger.Info("download: ANNAS_SECRET_KEY unset — skipping fast_download")
		annaErr = usererror.New("AUTH_REQUIRED",
			"ANNAS_SECRET_KEY is not set — skipped the fast_download tier.")
	}

	// --- Tier 2: Libgen.li ---

	if !cfg.LibgenEnabled {
		// Fallback is disabled; surface the Anna's error (or the "auth
		// required" stub when we didn't even try).
		return nil, annaErr
	}

	logger.Info("download: trying libgen.li fallback", zap.String("hash", hash))
	body, err := libgen.FetchByHash(ctx, client, cfg.LibgenBaseURL, hash)
	if err != nil {
		// Return the libgen error directly. When Anna's also failed we lose
		// the original cause here, but the libgen error is more actionable
		// for end-users (it names the exact hash and mirror).
		return nil, err
	}
	return writeAndResult(cfg.DownloadPath, filename, body, "libgen.li", logger)
}

// fetchFromAnna performs the Anna's fast_download flow and returns an open
// body that the caller must close (via writeAndResult).
func fetchFromAnna(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	hash string,
) (io.ReadCloser, error) {
	logger.Info("resolving download URL", zap.String("hash", hash))

	downloadURL, err := api.ResolveDownloadURL(ctx, client, hash, cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("download: resolve URL for %s: %w", hash, err)
	}

	logger.Info("fetching file", zap.String("url", downloadURL))

	resp, err := client.Get(ctx, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download: fetch file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, httpclient.StatusErrorFromResponse("download", downloadURL, resp)
	}
	return resp.Body, nil
}

// writeAndResult drains body to disk atomically and returns a DownloadResult
// stamped with the source tier. The body is always closed before return.
func writeAndResult(
	downloadPath, filename string,
	body io.ReadCloser,
	source string,
	logger *zap.Logger,
) (*model.DownloadResult, error) {
	defer body.Close()

	filePath, err := AtomicWrite(downloadPath, filename, body)
	if err != nil {
		return nil, usererror.Wrap("IO_ERROR",
			"Could not save the downloaded file. Check ANNAS_DOWNLOAD_PATH is writable and has free space.", err)
	}
	logger.Info("download complete",
		zap.String("path", filePath), zap.String("source", source))

	msg := "Downloaded successfully"
	if source == "libgen.li" {
		msg = "Downloaded successfully via libgen.li"
	}
	return &model.DownloadResult{
		FilePath: filePath,
		Message:  msg,
		Source:   source,
	}, nil
}

// IsConfigError reports whether err represents an operator/config problem
// that fallback tiers cannot fix. Exported so tool handlers can decide
// whether to bubble up or let the fallback cascade continue.
func IsConfigError(err error) bool {
	var ue *usererror.Error
	if !errors.As(err, &ue) {
		return false
	}
	switch ue.Code {
	case "PATH_REQUIRED", "PATH_UNAVAILABLE", "INVALID_HASH", "INVALID_DOI":
		return true
	}
	return false
}
