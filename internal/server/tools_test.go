package server

import (
	"strings"
	"testing"

	"github.com/ball2jh/annas-archive-mcp/internal/model"
)

func TestApplySearchDefaults(t *testing.T) {
	tests := []struct {
		name            string
		input           SearchInput
		wantContentType string
		wantLimit       int
	}{
		{
			name:            "empty defaults",
			input:           SearchInput{Query: "go"},
			wantContentType: "book_any",
			wantLimit:       5,
		},
		{
			name:            "explicit values preserved",
			input:           SearchInput{Query: "go", ContentType: "journal", Limit: 10},
			wantContentType: "journal",
			wantLimit:       10,
		},
		{
			name:            "limit clamped to max",
			input:           SearchInput{Query: "go", Limit: 50},
			wantContentType: "book_any",
			wantLimit:       20,
		},
		{
			name:            "negative limit gets default",
			input:           SearchInput{Query: "go", Limit: -1},
			wantContentType: "book_any",
			wantLimit:       5,
		},
		{
			name:            "zero limit gets default",
			input:           SearchInput{Query: "go", Limit: 0},
			wantContentType: "book_any",
			wantLimit:       5,
		},
		{
			name:            "limit at max boundary",
			input:           SearchInput{Query: "go", Limit: 20},
			wantContentType: "book_any",
			wantLimit:       20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applySearchDefaults(&tt.input)
			if tt.input.ContentType != tt.wantContentType {
				t.Errorf("ContentType = %q, want %q", tt.input.ContentType, tt.wantContentType)
			}
			if tt.input.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", tt.input.Limit, tt.wantLimit)
			}
		})
	}
}

func TestFormatSearchResults_Empty(t *testing.T) {
	got := formatSearchResults(nil)
	if got != "No results found." {
		t.Errorf("got %q, want %q", got, "No results found.")
	}
}

func TestFormatSearchResults(t *testing.T) {
	results := []model.SearchResult{
		{
			Title:    "Python Programming for Beginners",
			Authors:  []string{"Publishing", "AMZ"},
			Format:   "EPUB",
			Size:     "12.0MB",
			Language: "English",
			Hash:     "f87448722f0072549206b63999ec39e1",
			Stats:    model.CommunityStats{Downloads: 3486, Lists: 3, Reports: 1},
		},
		{
			Title:    "Python Programming",
			Authors:  []string{"Adam Stewart"},
			Format:   "PDF",
			Size:     "5.2MB",
			Language: "English",
			Hash:     "abc123def456abc123def456abc12345",
			Stats:    model.CommunityStats{Downloads: 100, Lists: 0, Reports: 0},
		},
	}

	got := formatSearchResults(results)

	// Verify structure.
	if !strings.Contains(got, "Found 2 result(s):") {
		t.Errorf("missing header, got:\n%s", got)
	}
	if !strings.Contains(got, "1. Python Programming for Beginners") {
		t.Errorf("missing first result title, got:\n%s", got)
	}
	if !strings.Contains(got, "2. Python Programming") {
		t.Errorf("missing second result title, got:\n%s", got)
	}
	if !strings.Contains(got, "Authors: Publishing, AMZ") {
		t.Errorf("missing authors, got:\n%s", got)
	}
	if !strings.Contains(got, "EPUB · 12.0MB") {
		t.Errorf("missing format info, got:\n%s", got)
	}
	if !strings.Contains(got, "3,486") {
		t.Errorf("missing formatted download count, got:\n%s", got)
	}
}

func TestFormatDetails(t *testing.T) {
	d := &model.BookDetails{
		Title:       "Test Book",
		Authors:     []string{"Author A", "Author B"},
		Publisher:   "Test Publisher",
		Year:        "2023",
		Format:      "PDF",
		Size:        "10MB",
		Language:    "English",
		Hash:        "abc123",
		ISBN:        "978-1234567890",
		DOI:         "10.1234/test",
		ISSN:        "",
		Description: "A test book.",
		Stats:       model.CommunityStats{Downloads: 1000, Lists: 5},
	}

	got := formatDetails(d)

	checks := []string{
		"Title: Test Book",
		"Authors: Author A, Author B",
		"Publisher: Test Publisher",
		"Year: 2023",
		"Format: PDF · 10MB",
		"ISBN: 978-1234567890",
		"DOI: 10.1234/test",
		"ISSN: —",
		"Description: A test book.",
		"1,000 · Lists: 5",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestFormatDOIResult(t *testing.T) {
	r := &model.DOIResult{
		Title:   "A Great Paper",
		Authors: []string{"Smith", "Jones"},
		Journal: "Nature",
		Year:    "2020",
		DOI:     "10.1038/nature12373",
		Hash:    "abc123def456",
	}

	got := formatDOIResult(r)

	checks := []string{
		"Title: A Great Paper",
		"Authors: Smith, Jones",
		"Journal: Nature",
		"Year: 2020",
		"DOI: 10.1038/nature12373",
		"Hash: abc123def456",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1,000"},
		{3486, "3,486"},
		{1000000, "1,000,000"},
		{12345678, "12,345,678"},
	}
	for _, tt := range tests {
		got := formatCount(tt.n)
		if got != tt.want {
			t.Errorf("formatCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatFileInfo(t *testing.T) {
	tests := []struct {
		format, size, want string
	}{
		{"PDF", "10MB", "PDF · 10MB"},
		{"EPUB", "", "EPUB"},
		{"", "5MB", "5MB"},
		{"", "", "—"},
	}
	for _, tt := range tests {
		got := formatFileInfo(tt.format, tt.size)
		if got != tt.want {
			t.Errorf("formatFileInfo(%q, %q) = %q, want %q", tt.format, tt.size, got, tt.want)
		}
	}
}

func TestValueOr(t *testing.T) {
	if got := valueOr("hello", "—"); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if got := valueOr("", "—"); got != "—" {
		t.Errorf("got %q, want %q", got, "—")
	}
}
