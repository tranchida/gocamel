package gocamel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SecurityValidator provides common security validation functions for GoCamel
// to prevent various injection and traversal attacks.

// ValidateRemotePath validates paths for remote protocols (FTP, SFTP, SMB)
// These paths may be Unix-style even on Windows
func ValidateRemotePath(path string) error {
	if path == "" {
		return nil
	}

	// Check for path traversal
	normalized := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(normalized, "/")

	var depth int
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			depth--
			if depth < 0 {
				return fmt.Errorf("path escapes allowed directory: %s", path)
			}
		} else {
			depth++
		}
	}

	// Check for embedded null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte")
	}

	return nil
}

// validateSQLQuery performs a lightweight sanity check on a SQL query string.
//
// The query is treated as TRUSTED configuration (it is never interpolated with
// Exchange data — values are bound via positional '?' placeholders, see
// SQLProducer.Send). The only injection vector worth guarding against at this
// layer is statement stacking: a stray ';' followed by another statement,
// which a naive copy-paste of a user-supplied value into the query could
// introduce. Comment sequences ('--', '/*') and a single trailing ';' are
// accepted: they occur in legitimate queries (e.g. SELECT '-- total' ...) and
// rejecting them produced false positives without adding real protection given
// parameterised binding.
//
// Only ';' characters that fall OUTSIDE string literals are considered, so a
// semicolon inside a quoted value does not trip the check.
func validateSQLQuery(query string) error {
	if countSemicolonsOutsideStrings(query) > 1 {
		return fmt.Errorf("query contains multiple statement terminators: %s", query)
	}
	return nil
}

// countSemicolonsOutsideStrings counts ';' characters that are not inside a
// single- or double-quoted string literal. It is a minimal lexer that does not
// handle escape sequences or identifier quoting — sufficient for the sanity
// check above, where the query is trusted configuration.
func countSemicolonsOutsideStrings(query string) int {
	count := 0
	inSingle := false
	inDouble := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';':
			if !inSingle && !inDouble {
				count++
			}
		}
	}
	return count
}

// sanitizer is a regex-based sanitizer for common patterns
var sanitizer = struct {
	pathTraversal *regexp.Regexp
	nullBytes     *regexp.Regexp
	controlChars  *regexp.Regexp
}{
	pathTraversal: regexp.MustCompile(`\.\.[\/\\]`),
	nullBytes:     regexp.MustCompile(`\x00`),
	controlChars:  regexp.MustCompile(`[\x00-\x1f\x7f]`),
}

// SanitizeInput removes dangerous characters from user input for use in file paths
func SanitizeInput(input string) string {
	// Remove null bytes
	input = sanitizer.nullBytes.ReplaceAllString(input, "")

	// Remove control characters except newline and tab
	input = sanitizer.controlChars.ReplaceAllStringFunc(input, func(s string) string {
		if s == "\n" || s == "\t" || s == "\r" {
			return s
		}
		return ""
	})

	return input
}

// pathDepth computes the effective directory depth of a path by counting
// path components: '.' and empty components are ignored, '..' decrements
// the depth, anything else increments it. A path that drives depth below
// zero escapes its base directory. Unlike a naive strings.Contains(path, "..")
// check, this does not reject legitimate filenames that merely contain two
// adjacent dots (e.g. "report..v2.txt", "foo..bar").
func pathDepth(path string) int {
	normalized := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(normalized, "/")
	depth := 0
	for _, part := range parts {
		switch part {
		case "", ".":
			// no-op
		case "..":
			depth--
		default:
			depth++
		}
	}
	return depth
}

// containsTraversal reports whether path escapes its base via a '..' component
// (depth drops below zero at any point during the walk).
func containsTraversal(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(normalized, "/")
	depth := 0
	for _, part := range parts {
		switch part {
		case "", ".":
			// no-op
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		default:
			depth++
		}
	}
	return false
}

// IsSafePath checks if a path is safe (exported for use in components).
// A path is considered unsafe if any '..' component drives the effective
// depth below zero (directory traversal) or if the path contains a null byte.
// Filenames that merely contain two adjacent dots (e.g. "report..v2.txt")
// are NOT rejected.
func IsSafePath(path string, checkExistence bool) bool {
	if containsTraversal(path) {
		return false
	}
	if strings.Contains(path, "\x00") {
		return false
	}

	if checkExistence {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}

	return true
}

// ValidatePath validates a file path for directory traversal.
// It rejects empty paths, null bytes and any '..' component that escapes the
// base (depth below zero). Filenames containing '..' as part of a longer
// token (e.g. "foo..bar") are accepted. A post-clean traversal check catches
// cases that filepath.Clean alone would not.
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte")
	}

	if containsTraversal(path) {
		return fmt.Errorf("path contains traversal sequence '..': %s", path)
	}

	// Clean the path and re-check: a cleaned path should never contain '..'
	// unless it is a leading one (a relative escape), which containsTraversal
	// already caught.
	cleanPath := filepath.Clean(path)
	if containsTraversal(cleanPath) {
		return fmt.Errorf("path escapes base directory: %s", path)
	}

	return nil
}

// ValidatePathInDir validates that a path is within a specific directory
func ValidatePathInDir(path, baseDir string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}

	// Get absolute paths
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	absBase, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return fmt.Errorf("failed to resolve base directory: %w", err)
	}

	// Ensure path is within base directory
	if !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) && absPath != absBase {
		return fmt.Errorf("path escapes base directory: %s not under %s", absPath, absBase)
	}

	return nil
}
