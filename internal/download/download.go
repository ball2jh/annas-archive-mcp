package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/api"
	"github.com/ball2jh/annas-archive-mcp/internal/config"
	"github.com/ball2jh/annas-archive-mcp/internal/doi"
	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/libgen"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scinet"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// Source names reported in DownloadResult.Source.
const (
	SourceFastDownload = "fast_download"
	SourceLibgen       = "libgen.li"
	SourceSciNet       = "sci-net"
	SourceCache        = "cache"
)

// defaultDOIFormat is the extension used by ByDOI when none is given.
const defaultDOIFormat = "pdf"

// Download fetches a file by MD5 hash and saves it atomically under
// cfg.DownloadPath. It tries two sources in order:
//
//  1. Anna's Archive fast_download API (requires ANNAS_SECRET_KEY).
//  2. Libgen.li (no key required; opt out with LIBGEN_ENABLED=false).
//
// On success, the returned DownloadResult.Source records which tier delivered
// the file (SourceFastDownload, SourceLibgen, or SourceCache when the target
// file already existed and no request was made).
//
// Validation, auth, and upstream-content failures are *usererror.Error;
// transport failures are returned wrapped for the tool boundary to classify.
func Download(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	rawHash, title, format string,
) (*model.DownloadResult, error) {
	if cfg.DownloadPath == "" {
		return nil, errPathRequired()
	}
	hash, ok := model.NormalizeHash(rawHash)
	if !ok {
		return nil, usererror.New(usererror.CodeInvalidHash, "hash must be a 32-character MD5 hex string.")
	}
	if strings.TrimSpace(title) == "" {
		title = hash
	}

	dst, err := newTarget(cfg, title, format)
	if err != nil {
		return nil, err
	}
	if cached := dst.cached(logger); cached != nil {
		return cached, nil
	}
	return fetchByHash(ctx, client, logger, cfg, hash, dst)
}

// ByDOI downloads a paper by DOI. It resolves the DOI to an MD5 hash on
// Anna's Archive and runs the same tiers as Download; when Anna's Archive
// cannot deliver the paper it falls back to Sci-Net, which needs no key.
//
// The filename is derived from title, or from the DOI when title is empty.
// format defaults to "pdf".
func ByDOI(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	rawDOI, title, format string,
) (*model.DownloadResult, error) {
	cleaned, err := doi.Clean(rawDOI)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = cleaned
	}
	if strings.TrimSpace(format) == "" {
		format = defaultDOIFormat
	}

	dst, err := newTarget(cfg, title, format)
	if err != nil {
		return nil, err
	}
	if cached := dst.cached(logger); cached != nil {
		return cached, nil
	}

	result, annaErr := byDOIViaAnna(ctx, client, logger, cfg, cleaned, dst)
	if annaErr == nil {
		return result, nil
	}
	// A cancelled request or a local write failure will not go better on
	// another source.
	if ctx.Err() != nil || usererror.HasCode(annaErr, usererror.CodeIOError) {
		return nil, annaErr
	}
	logger.Info("download: Anna's Archive could not deliver DOI, trying sci-net",
		zap.String("doi", cleaned), zap.Error(annaErr))

	body, _, err := scinet.FetchPDF(ctx, client, cfg.ScinetBaseURL, cleaned)
	if err != nil {
		return nil, err
	}
	return dst.save(body, SourceSciNet, logger)
}

// byDOIViaAnna resolves the DOI to a hash and downloads it by hash.
func byDOIViaAnna(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	cleanedDOI string,
	dst target,
) (*model.DownloadResult, error) {
	meta, err := doi.Resolve(ctx, client, logger, cleanedDOI)
	if err != nil {
		return nil, fmt.Errorf("download: resolve DOI: %w", err)
	}
	hash, ok := model.NormalizeHash(meta.Hash)
	if !ok {
		return nil, usererror.New(usererror.CodeNotFound, "Anna's Archive has no downloadable file for this DOI.")
	}
	return fetchByHash(ctx, client, logger, cfg, hash, dst)
}

// fetchByHash runs the fast_download → Libgen tiers for an already-validated
// hash and saves the result to dst.
func fetchByHash(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	hash string,
	dst target,
) (*model.DownloadResult, error) {
	// --- Tier 1: Anna's Archive fast_download (when key available) ---

	var annaErr error
	if cfg.SecretKey != "" {
		body, err := fetchFromAnna(ctx, client, logger, cfg, hash)
		if err == nil {
			return dst.save(body, SourceFastDownload, logger)
		}
		annaErr = err
		logger.Info("download: fast_download failed, evaluating fallback",
			zap.String("hash", hash), zap.Error(err))
	} else {
		logger.Info("download: ANNAS_SECRET_KEY unset — skipping fast_download")
		annaErr = usererror.New(usererror.CodeAuthRequired,
			"ANNAS_SECRET_KEY is not set, so Anna's Archive fast_download was skipped and no fallback source is enabled.")
	}

	// --- Tier 2: Libgen.li ---

	if !cfg.LibgenEnabled || ctx.Err() != nil {
		return nil, annaErr
	}

	logger.Info("download: trying libgen.li fallback", zap.String("hash", hash))
	body, err := libgen.FetchByHash(ctx, client, cfg.LibgenBaseURL, hash)
	if err != nil {
		return nil, bothFailed(ctx, annaErr, err)
	}
	return dst.save(body, SourceLibgen, logger)
}

// bothFailed picks the error to report when fast_download and Libgen have
// both failed. Reporting only the Libgen error, as a plain fallback chain
// would, hides the cause the caller can act on.
func bothFailed(ctx context.Context, annaErr, libgenErr error) error {
	if ctx.Err() != nil {
		return libgenErr
	}
	// Either may be nil: not every failure carries a user-facing code.
	annaUser, _ := errors.AsType[*usererror.Error](annaErr)
	libgenUser, _ := errors.AsType[*usererror.Error](libgenErr)
	libgenMissing := libgenUser != nil && libgenUser.Code == usererror.CodeNotFoundOnLibgen
	annaCode := ""
	if annaUser != nil {
		annaCode = annaUser.Code
	}

	switch {
	// A rejected key or an exhausted quota is something the user can fix, and
	// fixing it is usually what makes the file downloadable.
	case annaCode == usererror.CodeAuthInvalid, annaCode == usererror.CodeQuotaExhausted:
		return usererror.Wrap(annaCode, annaUser.Message+" The Libgen fallback could not provide this file either.", libgenErr)

	// Neither source carries the file: the fast_download message already
	// points at the page to download it from by hand.
	case libgenMissing && annaCode == usererror.CodeNotFastDownloadable:
		return usererror.Wrap(annaCode, annaUser.Message+" Libgen has no copy either.", libgenErr)

	// No key, and the free source lacks the file: a membership key is what
	// would make it downloadable.
	case libgenMissing && annaCode == usererror.CodeAuthRequired:
		return usererror.Wrap(libgenUser.Code, libgenUser.Message+" Anna's Archive fast downloads were not tried because ANNAS_SECRET_KEY is not set; a member key may make this file downloadable.", annaErr)

	// Libgen lacks the file and fast_download failed for a passing reason
	// (rate limit, outage, timeout): that reason is what a retry can fix.
	case libgenMissing:
		return annaErr
	}
	// Otherwise the Libgen error is the more specific one.
	return libgenErr
}

// fetchFromAnna performs the Anna's fast_download flow and returns an open
// body that the caller must close.
func fetchFromAnna(
	ctx context.Context,
	client *httpclient.Client,
	logger *zap.Logger,
	cfg *config.Config,
	hash string,
) (io.ReadCloser, error) {
	downloadURL, err := api.ResolveDownloadURL(ctx, client, hash, cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("download: resolve URL for %s: %w", hash, err)
	}

	logger.Info("fetching file", zap.String("hash", hash), zap.String("url", downloadURL))

	resp, err := client.Get(ctx, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download: fetch file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, httpclient.StatusErrorFromResponse("download", downloadURL, resp)
	}
	if httpclient.IsHTML(resp) {
		// An error or challenge page, not the file — never save it as a book.
		_ = resp.Body.Close()
		return nil, usererror.New(usererror.CodeUpstreamRejected,
			"Anna's Archive returned a web page instead of the file. Try again later.")
	}
	return resp.Body, nil
}

// target is a validated destination inside the download directory.
type target struct {
	dir      string
	filename string
}

// newTarget validates the configured download directory and derives a safe
// filename from title and format.
func newTarget(cfg *config.Config, title, format string) (target, error) {
	if cfg.DownloadPath == "" {
		return target{}, errPathRequired()
	}
	info, err := os.Stat(cfg.DownloadPath)
	if err != nil {
		return target{}, usererror.Wrap(usererror.CodePathUnavailable,
			"ANNAS_DOWNLOAD_PATH is not accessible. Check that the directory exists and is writable.", err)
	}
	if !info.IsDir() {
		return target{}, usererror.New(usererror.CodePathUnavailable, "ANNAS_DOWNLOAD_PATH must be a directory.")
	}
	return target{dir: cfg.DownloadPath, filename: SanitizeFilename(title, format)}, nil
}

func (t target) path() string {
	return filepath.Join(t.dir, t.filename)
}

// cached returns a result when a non-empty file already sits at the target
// path, so repeat calls cost no upstream requests or quota. Otherwise nil.
func (t target) cached(logger *zap.Logger) *model.DownloadResult {
	info, err := os.Stat(t.path())
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return nil
	}
	logger.Info("download skipped — file already exists",
		zap.String("path", t.path()), zap.Int64("size", info.Size()))
	return &model.DownloadResult{
		FilePath:       t.path(),
		Message:        "File already exists — download skipped.",
		AlreadyExisted: true,
		Source:         SourceCache,
	}
}

// save drains body to the target path atomically and returns a result stamped
// with the source tier. The body is always closed before return.
func (t target) save(body io.ReadCloser, source string, logger *zap.Logger) (*model.DownloadResult, error) {
	defer body.Close()

	filePath, err := AtomicWrite(t.dir, t.filename, body)
	if err != nil {
		// A failure while reading the response is a transport problem, not a
		// disk problem — let the tool boundary classify it (timeout, etc.).
		if readErr, ok := errors.AsType[*bodyReadError](err); ok {
			return nil, fmt.Errorf("download: %s transfer interrupted: %w", source, readErr.err)
		}
		if errors.Is(err, errEmptyBody) {
			return nil, usererror.Wrap(usererror.CodeUpstreamRejected,
				fmt.Sprintf("The %s source returned an empty file. Try again later.", source), err)
		}
		return nil, usererror.Wrap(usererror.CodeIOError,
			"Could not save the downloaded file. Check ANNAS_DOWNLOAD_PATH is writable and has free space.", err)
	}
	logger.Info("download complete",
		zap.String("path", filePath), zap.String("source", source))

	msg := "Downloaded successfully"
	if source != SourceFastDownload {
		msg = "Downloaded successfully via " + source
	}
	return &model.DownloadResult{
		FilePath: filePath,
		Message:  msg,
		Source:   source,
	}, nil
}

func errPathRequired() error {
	return usererror.New(usererror.CodePathRequired,
		"Downloads require a download path. Set ANNAS_DOWNLOAD_PATH in your MCP server configuration.")
}
