// Package scinet provides a fallback PDF-fetching backend that resolves DOIs
// via Sci-Net (sci-net.xyz). Unlike Anna's fast_download API, Sci-Net serves
// paywalled journal articles directly and does not require a membership key.
//
// The flow is simple: Sci-Net's per-DOI page (/<doi>) embeds the PDF in an
// <iframe src="/storage/.../<slug>.pdf">. We GET the page, extract that URL,
// then GET the PDF. No auth is required for guest fetches, but Sci-Net does
// gate heavy users — callers should treat repeated 403/429s as "out of free
// quota, ask the user to upload to earn credit."
package scinet

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// pdfSrcRE matches an iframe src pointing at a sci-net storage PDF. The URL
// fragment (#view=FitH&navpanes=0 etc.) is allowed and stripped by the caller.
//
// The Sci-Net HTML looks like:
//
//	<iframe src = "/storage/6021860/2ae58...d7174c3a1bbaa/Some-Title.pdf#view=FitH&navpanes=0">
//
// We accept either quote style and any amount of whitespace around `=`.
var pdfSrcRE = regexp.MustCompile(`(?i)<iframe[^>]*\bsrc\s*=\s*["'](/storage/[^"']+\.pdf)(?:#[^"']*)?["']`)

// FetchPDF resolves a DOI on Sci-Net and returns an open body for the PDF, the
// suggested filename derived from the storage URL, and any error.
//
// Errors are *usererror.Error with one of:
//   - NOT_FOUND_ON_SCINET — Sci-Net's page loaded but no PDF is hosted
//   - UPSTREAM_REJECTED — Sci-Net returned an unexpected status for the page
//     or the PDF fetch
//
// The caller is responsible for closing the returned body.
func FetchPDF(ctx context.Context, client *httpclient.Client, baseURL, doi string) (io.ReadCloser, string, error) {
	if strings.TrimSpace(doi) == "" {
		return nil, "", usererror.New("INVALID_DOI", "DOI is required.")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, "", usererror.New("CONFIG", "SCINET_BASE_URL is empty.")
	}

	pageURL := "https://" + baseURL + "/" + strings.TrimPrefix(doi, "/")

	resp, err := client.Get(ctx, pageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("scinet: GET page %s: %w", pageURL, err)
	}
	// Read the body right away — it's small (~4KB) and we don't need streaming.
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, "", fmt.Errorf("scinet: read page body: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", usererror.Wrap("UPSTREAM_REJECTED",
			fmt.Sprintf("Sci-Net returned HTTP %d for DOI %q.", resp.StatusCode, doi),
			fmt.Errorf("HTTP %d", resp.StatusCode))
	}

	match := pdfSrcRE.FindSubmatch(body)
	if match == nil {
		return nil, "", usererror.New("NOT_FOUND_ON_SCINET",
			fmt.Sprintf("Sci-Net has no PDF for DOI %q. The article may need to be requested from members.", doi))
	}
	pdfPath := string(match[1])
	pdfURL := "https://" + baseURL + pdfPath

	// Derive a human-readable filename from the storage path's last segment.
	// Sci-Net slugs are already dash-separated and safe.
	filename := path.Base(pdfPath)

	pdfResp, err := client.Get(ctx, pdfURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("scinet: GET pdf %s: %w", pdfURL, err)
	}
	if pdfResp.StatusCode != http.StatusOK {
		_ = pdfResp.Body.Close()
		return nil, "", usererror.Wrap("UPSTREAM_REJECTED",
			fmt.Sprintf("Sci-Net storage returned HTTP %d for PDF of DOI %q.", pdfResp.StatusCode, doi),
			fmt.Errorf("HTTP %d", pdfResp.StatusCode))
	}

	return pdfResp.Body, filename, nil
}
