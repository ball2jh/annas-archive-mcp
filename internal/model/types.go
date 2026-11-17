// Package model defines all shared data types for the Anna's Archive MCP server.
package model

import (
	"regexp"
	"strings"
)

// ContentType represents the type of content to search for on Anna's Archive.
type ContentType string

const (
	ContentTypeBookAny           ContentType = "book_any"
	ContentTypeBookFiction       ContentType = "book_fiction"
	ContentTypeBookNonfiction    ContentType = "book_nonfiction"
	ContentTypeBookComic         ContentType = "book_comic"
	ContentTypeJournal           ContentType = "journal"
	ContentTypeMagazine          ContentType = "magazine"
	ContentTypeStandardsDocument ContentType = "standards_document"
)

// ContentTypes lists every supported content type in display order.
var ContentTypes = []ContentType{
	ContentTypeBookAny,
	ContentTypeBookFiction,
	ContentTypeBookNonfiction,
	ContentTypeBookComic,
	ContentTypeJournal,
	ContentTypeMagazine,
	ContentTypeStandardsDocument,
}

// ContentTypePath maps a ContentType to the URL path segment used on Anna's Archive.
// An empty string means no filter is applied (the default "book_any" case).
var ContentTypePath = map[ContentType]string{
	ContentTypeBookAny:           "",
	ContentTypeBookFiction:       "book_fiction",
	ContentTypeBookNonfiction:    "book_nonfiction",
	ContentTypeBookComic:         "book_comic",
	ContentTypeJournal:           "journal_article",
	ContentTypeMagazine:          "magazine",
	ContentTypeStandardsDocument: "standards_document",
}

// ValidContentType reports whether ct is a recognised content type.
func ValidContentType(ct ContentType) bool {
	_, ok := ContentTypePath[ct]
	return ok
}

var md5RE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NormalizeHash trims and lower-cases an MD5 hash, reporting whether the
// result is a valid 32-character hex string. Anna's Archive and Libgen both
// key on the lowercase form.
func NormalizeHash(raw string) (string, bool) {
	hash := strings.ToLower(strings.TrimSpace(raw))
	return hash, md5RE.MatchString(hash)
}

// CommunityStats holds community engagement data for an item.
type CommunityStats struct {
	Downloads int    `json:"downloads"`
	Lists     int    `json:"lists"`
	Quality   string `json:"quality"`
	Comments  int    `json:"comments"`
	Reports   int    `json:"reports"`
}

// SearchResult is a single entry returned by the search tool.
type SearchResult struct {
	Title    string         `json:"title"`
	Authors  []string       `json:"authors"`
	Format   string         `json:"format"`
	Size     string         `json:"size"`
	Language string         `json:"language"`
	Hash     string         `json:"hash"` // MD5
	Stats    CommunityStats `json:"stats"`
}

// BookDetails holds the extended metadata returned by the get_details tool.
type BookDetails struct {
	Title       string         `json:"title"`
	Authors     []string       `json:"authors"`
	Publisher   string         `json:"publisher"`
	Year        string         `json:"year"`
	ISBN        string         `json:"isbn"`
	ISSN        string         `json:"issn"`
	DOI         string         `json:"doi"`
	Language    string         `json:"language"`
	Format      string         `json:"format"`
	Size        string         `json:"size"`
	Description string         `json:"description"`
	Hash        string         `json:"hash"` // MD5
	Stats       CommunityStats `json:"stats"`
}

// DOIResult is the metadata returned by the lookup_doi tool.
type DOIResult struct {
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Journal string   `json:"journal"`
	Year    string   `json:"year"`
	DOI     string   `json:"doi"`
	Hash    string   `json:"hash"` // MD5, ready for download
}

// DownloadResult is returned by the download tool.
type DownloadResult struct {
	FilePath string `json:"file_path"`
	Message  string `json:"message"`

	// AlreadyExisted is true when the target file was already present on disk
	// and the download was skipped. When true, no API quota was consumed.
	AlreadyExisted bool `json:"already_existed,omitempty"`

	// Source names the tier that delivered the file: "fast_download",
	// "libgen.li", or "cache" for skip-if-exists hits. Empty when unknown.
	Source string `json:"source,omitempty"`
}
