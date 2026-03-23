package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
)

// Download downloads a file by MD5 hash using the fast_download API.
// It resolves the download URL, fetches the file, and saves it atomically
// under cfg.DownloadPath. It returns a DownloadResult with the file path on
// success.
func Download(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	hash, title, format string,
) (*model.DownloadResult, error) {
	if cfg.SecretKey == "" {
		return nil, errors.New("Downloads require an API key. Set ANNAS_SECRET_KEY in your MCP server configuration.")
	}

	if cfg.DownloadPath == "" {
		return nil, errors.New("Downloads require a download path. Set ANNAS_DOWNLOAD_PATH in your MCP server configuration.")
	}

	if _, err := os.Stat(cfg.DownloadPath); err != nil {
		return nil, fmt.Errorf("download: download path %q is not accessible: %w", cfg.DownloadPath, err)
	}

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: unexpected status %d from %s", resp.StatusCode, downloadURL)
	}

	filename := SanitizeFilename(title, format)
	if title == "" {
		filename = SanitizeFilename(hash, format)
	}

	filePath, err := AtomicWrite(cfg.DownloadPath, filename, resp.Body)
	if err != nil {
		return nil, err
	}

	logger.Info("download complete", zap.String("path", filePath))

	return &model.DownloadResult{
		FilePath: filePath,
		Message:  "Downloaded successfully",
	}, nil
}
