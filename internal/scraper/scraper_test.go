package scraper

import (
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"
)

func testLogger(t *testing.T) *zap.Logger {
	t.Helper()
	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("creating logger: %v", err)
	}
	return logger
}

func loadDoc(t *testing.T, path string) *goquery.Document {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	doc, err := goquery.NewDocumentFromReader(f)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc
}

// ---------------------------------------------------------------------------
// ParseSearchResults
// ---------------------------------------------------------------------------

func TestParseSearchResults(t *testing.T) {
	doc := loadDoc(t, "../../testdata/search_results.html")
	logger := testLogger(t)

	results := ParseSearchResults(doc, logger)
	if len(results) < 3 {
		t.Fatalf("expected at least 3 search results, got %d", len(results))
	}

	// Verify first result.
	r0 := results[0]
	if r0.Hash != "f87448722f0072549206b63999ec39e1" {
		t.Errorf("result[0] hash = %q, want f87448722f0072549206b63999ec39e1", r0.Hash)
	}
	if r0.Title == "" {
		t.Error("result[0] title is empty")
	}
	if !strings.Contains(r0.Title, "Python Programming for Beginners") {
		t.Errorf("result[0] title = %q, expected it to contain 'Python Programming for Beginners'", r0.Title)
	}
	if len(r0.Authors) == 0 {
		t.Error("result[0] has no authors")
	} else if r0.Authors[0] != "Publishing, AMZ" {
		t.Errorf("result[0] author = %q, want 'Publishing, AMZ'", r0.Authors[0])
	}
	if r0.Format != "EPUB" {
		t.Errorf("result[0] format = %q, want EPUB", r0.Format)
	}
	if r0.Size != "12.0MB" {
		t.Errorf("result[0] size = %q, want 12.0MB", r0.Size)
	}
	if r0.Language != "English" {
		t.Errorf("result[0] language = %q, want English", r0.Language)
	}

	// Verify second result.
	r1 := results[1]
	if r1.Hash != "075d26750a7fb8af9464074a11f75765" {
		t.Errorf("result[1] hash = %q, want 075d26750a7fb8af9464074a11f75765", r1.Hash)
	}
	if len(r1.Authors) == 0 {
		t.Error("result[1] has no authors")
	} else if r1.Authors[0] != "Adam Stewart" {
		t.Errorf("result[1] author = %q, want 'Adam Stewart'", r1.Authors[0])
	}

	// Verify third result.
	r2 := results[2]
	if r2.Hash != "fd1812d07b1c48f87e69d420e5f5872e" {
		t.Errorf("result[2] hash = %q, want fd1812d07b1c48f87e69d420e5f5872e", r2.Hash)
	}
	if r2.Format != "PDF" {
		t.Errorf("result[2] format = %q, want PDF", r2.Format)
	}
	if r2.Size != "4.4MB" {
		t.Errorf("result[2] size = %q, want 4.4MB", r2.Size)
	}

	// Stats should be zero-valued.
	if r0.Stats.Downloads != 0 || r0.Stats.Lists != 0 {
		t.Error("result[0] stats should be zero-valued")
	}
}

// ---------------------------------------------------------------------------
// ParseDetailPage
// ---------------------------------------------------------------------------

func TestParseDetailPage(t *testing.T) {
	doc := loadDoc(t, "../../testdata/detail_page.html")
	logger := testLogger(t)

	bd, err := ParseDetailPage(doc, logger)
	if err != nil {
		t.Fatalf("ParseDetailPage: %v", err)
	}

	if !strings.Contains(bd.Title, "Python Programming for Beginners") {
		t.Errorf("title = %q, expected to contain 'Python Programming for Beginners'", bd.Title)
	}
	if bd.Hash == "" {
		t.Error("hash is empty")
	}
	if bd.Hash != "f87448722f0072549206b63999ec39e1" {
		t.Errorf("hash = %q, want f87448722f0072549206b63999ec39e1", bd.Hash)
	}
	if len(bd.Authors) == 0 {
		t.Error("no authors")
	} else if bd.Authors[0] != "Publishing, AMZ" {
		t.Errorf("author = %q, want 'Publishing, AMZ'", bd.Authors[0])
	}
	if bd.Year != "2021" {
		t.Errorf("year = %q, want 2021", bd.Year)
	}
	if bd.Language != "English" {
		t.Errorf("language = %q, want English", bd.Language)
	}
	if bd.Format != "EPUB" {
		t.Errorf("format = %q, want EPUB", bd.Format)
	}
	if bd.Size != "12.0MB" {
		t.Errorf("size = %q, want 12.0MB", bd.Size)
	}
	if bd.Description == "" {
		t.Error("description is empty")
	}
}

// ---------------------------------------------------------------------------
// ParseSciDBPage
// ---------------------------------------------------------------------------

func TestParseSciDBPage(t *testing.T) {
	doc := loadDoc(t, "../../testdata/scidb_page.html")
	logger := testLogger(t)

	dr, err := ParseSciDBPage(doc, logger)
	if err != nil {
		t.Fatalf("ParseSciDBPage: %v", err)
	}

	if dr.Title != "Nanometre-scale thermometry in a living cell" {
		t.Errorf("title = %q, want 'Nanometre-scale thermometry in a living cell'", dr.Title)
	}
	if dr.DOI != "10.1038/nature12373" {
		t.Errorf("doi = %q, want '10.1038/nature12373'", dr.DOI)
	}
	if len(dr.Authors) == 0 {
		t.Fatal("no authors")
	}
	// First author should be "Kucsko, G."
	if dr.Authors[0] != "Kucsko, G." {
		t.Errorf("authors[0] = %q, want 'Kucsko, G.'", dr.Authors[0])
	}
	if len(dr.Authors) != 8 {
		t.Errorf("len(authors) = %d, want 8", len(dr.Authors))
	}
	if dr.Journal != "Nature" {
		t.Errorf("journal = %q, want 'Nature'", dr.Journal)
	}
	if dr.Year != "2013" {
		t.Errorf("year = %q, want '2013'", dr.Year)
	}
	if dr.Hash != "d89c394b00116f093b5d9d6a6611f975" {
		t.Errorf("hash = %q, want 'd89c394b00116f093b5d9d6a6611f975'", dr.Hash)
	}
}

// ---------------------------------------------------------------------------
// ParseMetadataLine
// ---------------------------------------------------------------------------

func TestParseMetadataLine(t *testing.T) {
	tests := []struct {
		input    string
		language string
		format   string
		size     string
	}{
		{
			input:    "English [en] \u00b7 EPUB \u00b7 12.0MB \u00b7 2021 \u00b7 \U0001f4d8 Book (non-fiction)",
			language: "English",
			format:   "EPUB",
			size:     "12.0MB",
		},
		{
			input:    "French [fr] \u00b7 PDF \u00b7 3.2MB",
			language: "French",
			format:   "PDF",
			size:     "3.2MB",
		},
		{
			input:    "",
			language: "",
			format:   "",
			size:     "",
		},
	}

	for _, tc := range tests {
		lang, fmt, sz := ParseMetadataLine(tc.input)
		if lang != tc.language {
			t.Errorf("ParseMetadataLine(%q): language = %q, want %q", tc.input, lang, tc.language)
		}
		if fmt != tc.format {
			t.Errorf("ParseMetadataLine(%q): format = %q, want %q", tc.input, fmt, tc.format)
		}
		if sz != tc.size {
			t.Errorf("ParseMetadataLine(%q): size = %q, want %q", tc.input, sz, tc.size)
		}
	}
}
