// Package doi resolves a DOI to paper metadata via the Anna's Archive /scidb page.
package doi

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// Resolve looks up a DOI on Anna's Archive and returns paper metadata. The
// DOI may carry a "doi:" or doi.org URL prefix; see Clean.
func Resolve(ctx context.Context, client *httpclient.Client, logger *zap.Logger, doi string) (*model.DOIResult, error) {
	cleaned, err := Clean(doi)
	if err != nil {
		return nil, err
	}

	doc, err := client.GetHTML(ctx, "/scidb/"+EscapePath(cleaned))
	if err != nil {
		return nil, fmt.Errorf("doi: fetch scidb page for %q: %w", cleaned, err)
	}

	result, err := scraper.ParseSciDBPage(doc, logger)
	if err != nil {
		return nil, fmt.Errorf("doi: parse scidb page for %q: %w", cleaned, err)
	}

	if result.DOI == "" {
		result.DOI = cleaned
	}

	return result, nil
}

// doiPrefixes are the URL/scheme forms people paste DOIs in, lower-cased.
var doiPrefixes = []string{
	"https://doi.org/",
	"http://doi.org/",
	"https://dx.doi.org/",
	"http://dx.doi.org/",
	"doi.org/",
	"doi:",
}

// Clean validates and normalises a DOI string, stripping surrounding
// whitespace and any well-known prefix ("https://doi.org/", "doi:", …).
//
// A valid DOI must start with "10." and contain at least one "/".
func Clean(raw string) (string, error) {
	doi := strings.TrimSpace(raw)

	lower := strings.ToLower(doi)
	for _, prefix := range doiPrefixes {
		if strings.HasPrefix(lower, prefix) {
			doi = strings.TrimSpace(doi[len(prefix):])
			break
		}
	}

	if doi == "" {
		return "", usererror.New(usererror.CodeInvalidDOI, "DOI is required.")
	}
	if !strings.HasPrefix(doi, "10.") {
		return "", usererror.New(usererror.CodeInvalidDOI, "DOI must start with \"10.\".")
	}
	if !strings.Contains(doi, "/") {
		return "", usererror.New(usererror.CodeInvalidDOI, "DOI must contain a slash, for example 10.1038/nature12373.")
	}

	return doi, nil
}

// EscapePath percent-encodes a DOI for use as a URL path, keeping "/" as a
// separator. DOIs may legally contain characters such as "#", "?", "<" and
// ">" that would otherwise be read as URL syntax.
func EscapePath(doi string) string {
	return (&url.URL{Path: doi}).EscapedPath()
}
