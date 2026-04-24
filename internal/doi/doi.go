// Package doi resolves a DOI to paper metadata via the Anna's Archive /scidb page.
package doi

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/httpclient"
	"github.com/ball2jh/annas-archive-mcp/internal/model"
	"github.com/ball2jh/annas-archive-mcp/internal/scraper"
	"github.com/ball2jh/annas-archive-mcp/internal/usererror"
)

// Resolve looks up a DOI on Anna's Archive and returns paper metadata.
//
// Flow:
//  1. Validate DOI format (must start with "10." and contain a "/").
//  2. Clean the DOI: strip any "https://doi.org/" or "doi:" prefix.
//  3. Fetch the SciDB page: client.GetHTML(ctx, "/scidb/"+doi).
//  4. Parse with scraper.ParseSciDBPage(doc, logger).
//  5. If DOI wasn't set by the scraper, set it from the cleaned input.
func Resolve(ctx context.Context, client *httpclient.Client, logger *zap.Logger, doi string) (*model.DOIResult, error) {
	cleaned, err := cleanDOI(doi)
	if err != nil {
		return nil, err
	}

	doc, err := client.GetHTML(ctx, "/scidb/"+cleaned)
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

// cleanDOI validates and normalises a DOI string.
//
// It strips the following well-known prefixes before validation:
//   - "https://doi.org/"
//   - "http://doi.org/"
//   - "doi:"
//
// A valid DOI must start with "10." and contain at least one "/".
func cleanDOI(raw string) (string, error) {
	doi := raw

	// Strip known URL/prefix forms.
	for _, prefix := range []string{
		"https://doi.org/",
		"http://doi.org/",
		"doi:",
	} {
		if strings.HasPrefix(strings.ToLower(doi), prefix) {
			doi = doi[len(prefix):]
			break
		}
	}

	doi = strings.TrimSpace(doi)

	if !strings.HasPrefix(doi, "10.") {
		return "", usererror.New("INVALID_DOI", "DOI must start with \"10.\".")
	}
	if !strings.Contains(doi, "/") {
		return "", usererror.New("INVALID_DOI", "DOI must contain a slash, for example 10.1038/nature12373.")
	}

	return doi, nil
}
