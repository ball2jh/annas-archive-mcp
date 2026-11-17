// Package download provides file download orchestration for Anna's Archive.
package download

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxFilenameBytes caps the title part of a filename (excluding the
	// extension). Filesystems limit names to 255 bytes, not characters, so
	// the cap is in bytes to keep non-ASCII titles valid too.
	maxFilenameBytes = 200

	// maxExtensionBytes caps the extension part of a filename.
	maxExtensionBytes = 16

	// fallbackName is used when a title sanitises down to nothing.
	fallbackName = "download"
)

// errEmptyBody is returned by AtomicWrite when the source delivers no bytes.
var errEmptyBody = errors.New("download: source returned an empty body")

// bodyReadError marks an AtomicWrite failure that came from reading the
// source rather than from writing to disk.
type bodyReadError struct{ err error }

func (e *bodyReadError) Error() string { return "download: read source: " + e.err.Error() }
func (e *bodyReadError) Unwrap() error { return e.err }

// sanitizeRune replaces characters that are unsafe in filenames across common
// filesystems (path separators, Windows-reserved punctuation, control
// characters) with an underscore.
func sanitizeRune(r rune) rune {
	if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
		return '_'
	}
	return r
}

// truncateBytes shortens s to at most limit bytes without splitting a rune.
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// SanitizeFilename cleans a title for use as a filename and appends the
// format as an extension (e.g. "Python Programming.epub"). The result never
// contains a path separator and is never "." or "..", so joining it to a
// directory cannot escape that directory. An empty title becomes "download".
func SanitizeFilename(title, format string) string {
	name := strings.Map(sanitizeRune, strings.TrimSpace(title))
	name = truncateBytes(name, maxFilenameBytes)
	// Trailing dots and spaces are invalid on Windows; trimming them also
	// collapses "." and ".." to the fallback.
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = fallbackName
	}

	ext := strings.Map(sanitizeRune, strings.Trim(strings.TrimSpace(format), "."))
	ext = strings.TrimRight(truncateBytes(ext, maxExtensionBytes), ". ")
	if ext == "" {
		return name
	}
	return name + "." + ext
}

// AtomicWrite writes all data from r into a new file named filename inside dir.
// It uses a write-to-temp-then-rename pattern so that a partial write never
// leaves a corrupt file at the final path, and refuses to create an empty
// file. It returns the path of the written file.
func AtomicWrite(dir, filename string, r io.Reader) (path string, err error) {
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("download: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// Always clean up the temp file on failure.
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	src := &trackingReader{r: r}
	n, err := io.Copy(tmp, src)
	switch {
	case src.err != nil:
		return "", &bodyReadError{err: src.err}
	case err != nil:
		return "", fmt.Errorf("download: write temp file: %w", err)
	case n == 0:
		return "", errEmptyBody
	}

	// CreateTemp makes the file 0600; give downloads ordinary file permissions.
	if err := tmp.Chmod(0o644); err != nil {
		return "", fmt.Errorf("download: set file mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("download: close temp file: %w", err)
	}

	finalPath := filepath.Join(dir, filename)
	if err := os.Rename(tmpName, finalPath); err != nil {
		return "", fmt.Errorf("download: rename to final path: %w", err)
	}
	return finalPath, nil
}

// trackingReader remembers the first non-EOF read error so AtomicWrite can
// tell a failed transfer apart from a failed disk write.
type trackingReader struct {
	r   io.Reader
	err error
}

func (t *trackingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}
