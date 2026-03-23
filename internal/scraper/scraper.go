package scraper

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"

	"github.com/ball2jh/annas-archive-mcp/internal/model"
)

// md5RE extracts a 32-hex-char MD5 hash from a /md5/{hash} path.
var md5RE = regexp.MustCompile(`/md5/([0-9a-fA-F]{32})`)

// yearRE extracts a four-digit year.
var yearRE = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)

// ---------------------------------------------------------------------------
// ParseSearchResults
// ---------------------------------------------------------------------------

// ParseSearchResults extracts search result entries from an Anna's Archive
// search page. Stats are left as zero-values because they come from a
// separate API call.
func ParseSearchResults(doc *goquery.Document, logger *zap.Logger) []model.SearchResult {
	root := doc.Selection

	containers := SearchResultContainer.Find(root, logger)
	if containers.Length() == 0 {
		logger.Warn("no search result containers found")
		return nil
	}

	var results []model.SearchResult

	containers.Each(func(_ int, container *goquery.Selection) {
		var sr model.SearchResult

		// --- Hash + Title ---
		titleEl := SearchResultTitle.FindOne(container, logger)
		if titleEl.Length() == 0 {
			logger.Debug("skipping result: no title element found")
			return
		}
		sr.Title = strings.TrimSpace(titleEl.Text())
		if href, ok := titleEl.Attr("href"); ok {
			sr.Hash = extractMD5(href)
		}
		if sr.Hash == "" {
			logger.Debug("skipping result: no MD5 hash in title href")
			return
		}

		// --- Author ---
		authorIcon := SearchResultAuthor.Find(container, logger)
		if authorIcon.Length() > 0 {
			// The icon span is inside the anchor; get the parent anchor text.
			authorAnchor := authorIcon.First().Parent()
			sr.Authors = parseAuthors(textWithoutChildren(authorAnchor, "span"))
		}

		// --- Metadata line ---
		metaEl := SearchResultMeta.FindOne(container, logger)
		if metaEl.Length() > 0 {
			// We only want the direct text content before any child elements
			// like <a> (Save link) and <span> (stats). Get the raw text of the
			// node and trim at the Save link boundary.
			metaText := metaEl.Text()
			sr.Language, sr.Format, sr.Size = ParseMetadataLine(metaText)
		}

		results = append(results, sr)
	})

	return results
}

// ---------------------------------------------------------------------------
// ParseDetailPage
// ---------------------------------------------------------------------------

// ParseDetailPage extracts full book metadata from an Anna's Archive /md5/
// detail page.
func ParseDetailPage(doc *goquery.Document, logger *zap.Logger) (*model.BookDetails, error) {
	root := doc.Selection
	bd := &model.BookDetails{}

	// --- Title ---
	titleEl := DetailTitle.FindOne(root, logger)
	if titleEl.Length() == 0 {
		return nil, fmt.Errorf("scraper: detail page title not found")
	}
	// The title div may contain a <span class="select-none"> with a search
	// icon — strip children.
	bd.Title = textWithoutChildren(titleEl, "span")

	// --- Hash from URL in page ---
	// Grab it from any /md5/ link on the page, or from the code tabs.
	hashLink := root.Find(`a[href^="/md5/"].js-vim-focus`)
	if hashLink.Length() == 0 {
		// Fallback: look in code tabs for "AA Record ID" with md5: prefix.
		DetailCodeTab.Find(root, logger).Each(func(_ int, tab *goquery.Selection) {
			spans := tab.Find("span")
			if spans.Length() >= 2 {
				label := strings.TrimSpace(spans.First().Text())
				value := strings.TrimSpace(spans.Last().Text())
				if label == "AA Record ID" && strings.HasPrefix(value, "md5:") {
					bd.Hash = value[4:]
				}
			}
		})
	} else {
		if href, ok := hashLink.Attr("href"); ok {
			bd.Hash = extractMD5(href)
		}
	}
	// Another fallback: look for any /md5/ link in the page.
	if bd.Hash == "" {
		root.Find(`a[href^="/md5/"]`).Each(func(_ int, s *goquery.Selection) {
			if bd.Hash != "" {
				return
			}
			if href, ok := s.Attr("href"); ok {
				bd.Hash = extractMD5(href)
			}
		})
	}

	// --- Author ---
	authorIcon := DetailAuthor.Find(root, logger)
	if authorIcon.Length() > 0 {
		authorAnchor := authorIcon.First().Parent()
		bd.Authors = parseAuthors(textWithoutChildren(authorAnchor, "span"))
	}

	// --- Publisher / Year ---
	pubIcon := DetailPublisher.Find(root, logger)
	if pubIcon.Length() > 0 {
		pubAnchor := pubIcon.First().Parent()
		pubText := strings.TrimSpace(textWithoutChildren(pubAnchor, "span"))
		bd.Publisher, bd.Year = parsePublisherYear(pubText)
	}

	// --- Metadata line ---
	metaEl := DetailMeta.FindOne(root, logger)
	if metaEl.Length() > 0 {
		bd.Language, bd.Format, bd.Size = ParseMetadataLine(metaEl.Text())
	}

	// --- Description ---
	descEl := DetailDescription.FindOne(root, logger)
	if descEl.Length() > 0 {
		bd.Description = parseDescription(descEl)
	}

	// --- Code tabs (ISBN, DOI, ISSN, etc.) ---
	DetailCodeTab.Find(root, logger).Each(func(_ int, tab *goquery.Selection) {
		spans := tab.Find("span")
		if spans.Length() < 2 {
			return
		}
		label := strings.TrimSpace(spans.First().Text())
		value := strings.TrimSpace(spans.Last().Text())

		switch {
		case strings.EqualFold(label, "ISBN 13") || strings.EqualFold(label, "ISBN 10"):
			if bd.ISBN == "" {
				bd.ISBN = value
			}
		case strings.EqualFold(label, "DOI"):
			if bd.DOI == "" {
				bd.DOI = value
			}
		case strings.EqualFold(label, "ISSN"):
			if bd.ISSN == "" {
				bd.ISSN = value
			}
		}
	})

	// Fill year from metadata line if publisher line didn't have one.
	if bd.Year == "" && metaEl.Length() > 0 {
		parts := splitMetadata(metaEl.Text())
		if len(parts) >= 4 {
			y := strings.TrimSpace(parts[3])
			if yearRE.MatchString(y) {
				bd.Year = yearRE.FindString(y)
			}
		}
	}

	return bd, nil
}

// ---------------------------------------------------------------------------
// ParseSciDBPage
// ---------------------------------------------------------------------------

// ParseSciDBPage extracts article metadata from an Anna's Archive /scidb/ page.
func ParseSciDBPage(doc *goquery.Document, logger *zap.Logger) (*model.DOIResult, error) {
	root := doc.Selection
	dr := &model.DOIResult{}

	// Scope to the left-side menu.
	menu := SciDBLeftMenu.FindOne(root, logger)
	if menu.Length() == 0 {
		return nil, fmt.Errorf("scraper: scidb left menu not found")
	}

	// --- DOI ---
	// The DOI line looks like: "DOI: 10.1038/nature12373 🔍"
	menu.Find(`div[class*="text-sm"]`).Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if strings.HasPrefix(text, "DOI:") {
			doi := strings.TrimPrefix(text, "DOI:")
			doi = strings.TrimSpace(doi)
			// Strip trailing search icon or any non-DOI chars.
			// DOIs typically look like 10.xxxx/yyy — cut at first space or emoji.
			if idx := strings.IndexByte(doi, ' '); idx > 0 {
				doi = doi[:idx]
			}
			dr.DOI = strings.TrimSpace(doi)
		}
	})

	// --- Info block (title, publisher, authors) ---
	infoBlock := SciDBInfoBlock.FindOne(menu, logger)
	if infoBlock.Length() == 0 {
		return nil, fmt.Errorf("scraper: scidb info block not found")
	}

	// Title
	titleEl := SciDBTitle.FindOne(infoBlock, logger)
	if titleEl.Length() > 0 {
		dr.Title = textWithoutChildren(titleEl, "a")
	}

	// Authors (semicolon-separated in italic div)
	authEl := SciDBAuthors.FindOne(infoBlock, logger)
	if authEl.Length() > 0 {
		authText := textWithoutChildren(authEl, "a")
		parts := strings.Split(authText, ";")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				dr.Authors = append(dr.Authors, p)
			}
		}
	}

	// Publisher/Journal line — it's the plain div between font-bold and italic.
	// We walk the direct children of infoBlock looking for a div that is
	// neither font-bold nor italic and isn't the metadata summary line.
	infoBlock.Children().Each(func(_ int, child *goquery.Selection) {
		if dr.Journal != "" {
			return
		}
		cls, _ := child.Attr("class")
		if goquery.NodeName(child) != "div" {
			return
		}
		if strings.Contains(cls, "font-bold") || strings.Contains(cls, "italic") || strings.Contains(cls, "text-gray-500") {
			return
		}
		text := strings.TrimSpace(child.Text())
		if text == "" {
			return
		}
		dr.Journal, dr.Year = parseSciDBPublisherLine(text)
	})

	// --- MD5 hash ---
	md5Link := SciDBMD5Link.FindOne(menu, logger)
	if md5Link.Length() > 0 {
		if href, ok := md5Link.Attr("href"); ok {
			dr.Hash = extractMD5(href)
		}
	}

	if dr.Title == "" {
		return nil, fmt.Errorf("scraper: scidb page has no title")
	}

	return dr, nil
}

// ---------------------------------------------------------------------------
// ParseMetadataLine
// ---------------------------------------------------------------------------

// ParseMetadataLine extracts language, format, and size from the metadata
// string that looks like:
//
//	"English [en] · EPUB · 12.0MB · 2021 · 📘 Book (non-fiction) · 🚀/sources · Save..."
//
// It splits on " · " and picks the first three fields.
func ParseMetadataLine(text string) (language, format, size string) {
	parts := splitMetadata(text)
	if len(parts) >= 1 {
		lang := strings.TrimSpace(parts[0])
		// Strip language code like " [en]".
		if idx := strings.Index(lang, " ["); idx > 0 {
			lang = lang[:idx]
		}
		language = lang
	}
	if len(parts) >= 2 {
		format = strings.TrimSpace(parts[1])
	}
	if len(parts) >= 3 {
		size = strings.TrimSpace(parts[2])
	}
	return
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// splitMetadata splits a metadata line by the " · " separator (middle dot
// surrounded by spaces). This handles the Unicode middle-dot U+00B7 used on
// Anna's Archive pages.
func splitMetadata(text string) []string {
	return strings.Split(text, " \u00b7 ")
}

// extractMD5 pulls an MD5 hash from a /md5/{hash} path.
func extractMD5(href string) string {
	m := md5RE.FindStringSubmatch(href)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// textWithoutChildren returns the text content of sel with all text from
// matching child elements removed. This is useful for stripping icon spans
// or search-link anchors from a parent element's text.
func textWithoutChildren(sel *goquery.Selection, childSelector string) string {
	// Clone so we don't mutate the original document.
	clone := sel.Clone()
	clone.Find(childSelector).Remove()
	return strings.TrimSpace(clone.Text())
}

// parseAuthors splits an author string into individual names. Authors may be
// separated by semicolons or be a single name.
func parseAuthors(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Some pages use semicolons, some commas-as-separators-with-last-name-first.
	// If semicolons are present, split on those.
	if strings.Contains(raw, ";") {
		parts := strings.Split(raw, ";")
		var out []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	return []string{raw}
}

// parsePublisherYear splits "Publisher, 2021" into publisher and year.
// If only a year is present (e.g., "2021"), publisher is empty.
func parsePublisherYear(text string) (publisher, year string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	m := yearRE.FindString(text)
	if m != "" {
		year = m
		// Everything before the year (minus trailing comma/space) is publisher.
		idx := strings.LastIndex(text, m)
		pub := strings.TrimSpace(text[:idx])
		pub = strings.TrimRight(pub, ", ")
		publisher = pub
	} else {
		// No year found — treat the whole thing as publisher.
		publisher = text
	}
	return
}

// parseDescription extracts a human-readable description from the detail page
// description div. It skips the grey label divs ("Alternative filename",
// "metadata comments", etc.) and joins the value divs.
func parseDescription(sel *goquery.Selection) string {
	var parts []string
	sel.Children().Each(func(_ int, child *goquery.Selection) {
		cls, _ := child.Attr("class")
		// Skip label divs (uppercase grey text).
		if strings.Contains(cls, "text-gray-500") && strings.Contains(cls, "uppercase") {
			return
		}
		text := strings.TrimSpace(child.Text())
		if text != "" {
			parts = append(parts, text)
		}
	})
	return strings.Join(parts, "\n")
}

// parseSciDBPublisherLine extracts journal name and year from a line like:
//
//	"Nature Publishing Group; ... (ISSN 0028-0836), Nature, #7460, 500, pages 54-58, 2013 jul 31"
//
// The journal name is the part right after the ISSN parenthetical (or the
// first segment if no ISSN). The year is the four-digit number at the end.
func parseSciDBPublisherLine(text string) (journal, year string) {
	// Extract year (last 4-digit number).
	if m := yearRE.FindAllString(text, -1); len(m) > 0 {
		year = m[len(m)-1]
	}

	// Try to extract journal name from after "(ISSN ...)" pattern.
	if idx := strings.Index(text, "),"); idx > 0 {
		rest := text[idx+2:]
		parts := strings.SplitN(rest, ",", 2)
		journal = strings.TrimSpace(parts[0])
		return
	}

	// Fallback: use text before first comma.
	if idx := strings.IndexByte(text, ','); idx > 0 {
		journal = strings.TrimSpace(text[:idx])
	} else {
		journal = text
	}
	return
}
