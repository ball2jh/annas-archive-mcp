// Package download provides file download orchestration for Anna's Archive.
package download

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxFilenameLen is the maximum length for the title part of a filename
	// (excluding the extension).
	maxFilenameLen = 200
)

// invalidChars are filename characters that are unsafe across common filesystems.
var invalidChars = strings.NewReplacer(
	"/", "_",
	`\`, "_",
	":", "_",
	"*", "_",
	"?", "_",
	`"`, "_",
	"<", "_",
	">", "_",
	"|", "_",
)

// SanitizeFilename cleans a title for use as a filename and appends the
// format as an extension (e.g. "Python_Programming.epub").
// If title is empty, hash is used as a fallback.
func SanitizeFilename(title, format string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = "download"
	}

	name = invalidChars.Replace(name)

	// Trim to maxFilenameLen runes to stay well under filesystem limits.
	runes := []rune(name)
	if len(runes) > maxFilenameLen {
		runes = runes[:maxFilenameLen]
	}
	name = strings.TrimSpace(string(runes))

	if format != "" {
		// Sanitize the format extension too (remove path separators, etc.).
		format = invalidChars.Replace(strings.TrimSpace(format))
		return name + "." + format
	}
	return name
}

// AtomicWrite writes all data from r into a new file named filename inside dir.
// It uses a write-to-temp-then-rename pattern so that a partial write never
// leaves a corrupt file at the final path.
// It returns the absolute path of the written file.
func AtomicWrite(dir, filename string, r io.Reader) (string, error) {
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("download: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// Always clean up the temp file on failure.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("download: write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("download: close temp file: %w", err)
	}

	finalPath := filepath.Join(dir, filename)
	if err := os.Rename(tmpName, finalPath); err != nil {
		return "", fmt.Errorf("download: rename to final path: %w", err)
	}

	success = true
	return finalPath, nil
}
