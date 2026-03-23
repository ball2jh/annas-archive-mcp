// Package scraper parses Anna's Archive HTML pages into structured data using
// CSS selector chains with ordered fallbacks.
package scraper

import (
	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"
)

// SelectorChain is an ordered list of CSS selectors. The first selector that
// matches at least one element wins; later entries serve as fallbacks when the
// page structure changes.
type SelectorChain []string

// Find tries each selector in the chain against doc, returning the first
// non-empty selection. If a fallback is used, a warning is logged so we know
// the primary selector needs updating. Returns an empty selection (length 0) if
// nothing matches.
func (sc SelectorChain) Find(doc *goquery.Selection, logger *zap.Logger) *goquery.Selection {
	for i, sel := range sc {
		s := doc.Find(sel)
		if s.Length() > 0 {
			if i > 0 {
				logger.Warn("selector chain fell back",
					zap.Int("index", i),
					zap.String("matched", sel),
					zap.String("primary", sc[0]),
				)
			}
			return s
		}
	}
	return doc.Find("__never_match__") // empty selection
}

// FindOne is like Find but returns only the first matched element.
func (sc SelectorChain) FindOne(doc *goquery.Selection, logger *zap.Logger) *goquery.Selection {
	return sc.Find(doc, logger).First()
}

// ---------------------------------------------------------------------------
// Selector chain definitions
// ---------------------------------------------------------------------------

// Search results page selectors.
var (
	// SearchResultContainer matches each search result row.
	SearchResultContainer = SelectorChain{
		`div[class*="border-b"][class*="flex"][class*="pt-3"][class*="pb-3"]`,
	}

	// SearchResultTitle matches the title link inside a result.
	SearchResultTitle = SelectorChain{
		`a[href^="/md5/"].js-vim-focus`,
		`a[href^="/md5/"][class*="font-semibold"][class*="text-lg"]`,
	}

	// SearchResultAuthor matches the author link (contains user-edit icon).
	SearchResultAuthor = SelectorChain{
		`a[href^="/search"] span[class*="user-edit"]`,
	}

	// SearchResultPublisher matches the publisher/year link (contains company icon).
	SearchResultPublisher = SelectorChain{
		`a[href^="/search"] span[class*="company"]`,
	}

	// SearchResultMeta matches the metadata line (language, format, size, ...).
	SearchResultMeta = SelectorChain{
		`div[class*="text-gray-800"][class*="font-semibold"][class*="text-sm"][class*="mt-2"]`,
	}
)

// Detail page selectors.
var (
	// DetailTitle matches the large title div on a /md5/ page.
	DetailTitle = SelectorChain{
		`div[class*="font-semibold"][class*="text-2xl"]`,
	}

	// DetailAuthor matches the author link on a detail page.
	DetailAuthor = SelectorChain{
		`a[class*="text-base"] span[class*="user-edit"]`,
	}

	// DetailPublisher matches the publisher/year link on a detail page.
	DetailPublisher = SelectorChain{
		`a[class*="text-base"] span[class*="company"]`,
	}

	// DetailMeta matches the metadata line on a detail page (mt-4 variant).
	DetailMeta = SelectorChain{
		`div[class*="text-gray-800"][class*="font-semibold"][class*="text-sm"][class*="mt-4"]`,
	}

	// DetailDescription matches the collapsible description box.
	DetailDescription = SelectorChain{
		`div.js-md5-top-box-description`,
		`div[class*="js-md5-top-box-description"]`,
	}

	// DetailCodeTab matches individual code/identifier tabs.
	DetailCodeTab = SelectorChain{
		`a.js-md5-codes-tabs-tab`,
		`a[class*="js-md5-codes-tabs-tab"]`,
	}
)

// SciDB page selectors.
var (
	// SciDBLeftMenu matches the left-side panel that holds all metadata.
	SciDBLeftMenu = SelectorChain{
		`div#left-side-menu`,
		`div[id="left-side-menu"]`,
	}

	// SciDBInfoBlock matches the container that holds title, publisher, authors.
	SciDBInfoBlock = SelectorChain{
		`div[class*="text-xs"][class*="sm:text-sm"]`,
	}

	// SciDBTitle matches the bold title within the info block.
	SciDBTitle = SelectorChain{
		`div.font-bold`,
	}

	// SciDBAuthors matches the italic authors line within the info block.
	SciDBAuthors = SelectorChain{
		`div.italic`,
	}

	// SciDBPublisherLine is the plain div between title and authors
	// (publisher, ISSN, journal, pages, date).
	// We select it positionally in the parser rather than via a unique selector.

	// SciDBMD5Link matches the /md5/{hash} link.
	SciDBMD5Link = SelectorChain{
		`a[href^="/md5/"]`,
	}
)
