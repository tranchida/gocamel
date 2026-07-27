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
	if hasNonTrailingSemicolonOutsideStrings(query) {
		return fmt.Errorf("query contains stacked SQL statements: %s", query)
	}
	return nil
}

// hasNonTrailingSemicolonOutsideStrings reports whether a statement terminator
// is followed by SQL code. Whitespace and comments may follow a single trailing
// semicolon, while "SELECT 1; DROP TABLE t" is rejected.
func hasNonTrailingSemicolonOutsideStrings(query string) bool {
	inSingle := false
	inDouble := false
	inBacktick := false
	inLineComment := false
	inBlockComment := false
	seenTerminator := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if inLineComment {
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if c == '*' && i+1 < len(query) && query[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inSingle {
			if c == '\\' && i+1 < len(query) {
				i++
				continue
			}
			if c == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					i++
				} else {
					inDouble = false
				}
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				if i+1 < len(query) && query[i+1] == '`' {
					i++
				} else {
					inBacktick = false
				}
			}
			continue
		}

		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				inLineComment = true
				i++
			} else if seenTerminator {
				return true
			}
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				inBlockComment = true
				i++
			} else if seenTerminator {
				return true
			}
		case ';':
			if seenTerminator {
				return true
			}
			seenTerminator = true
		case ' ', '\t', '\r', '\n', '\f':
			// Whitespace is valid after the final terminator.
		default:
			if seenTerminator {
				return true
			}
		}
	}
	return false
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

// ValidatePathInDir validates that a path is within a specific directory.
//
// Symlinks are resolved before the containment check: a lexical comparison
// alone accepts a symlink that lives under baseDir but points outside it,
// which defeats the confinement this function exists to provide. When the path
// does not exist yet, the closest existing ancestor is resolved instead so
// that a not-yet-created file under a symlinked directory is still checked.
func ValidatePathInDir(path, baseDir string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}

	absBase, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return fmt.Errorf("failed to resolve base directory: %w", err)
	}
	// The base itself may be reached through a symlink; resolve it so both
	// sides of the comparison are in the same (real) namespace.
	if resolvedBase, err := filepath.EvalSymlinks(absBase); err == nil {
		absBase = resolvedBase
	}

	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}
	resolvedPath, err := resolveExistingPrefix(absPath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Ensure path is within base directory
	if !strings.HasPrefix(resolvedPath, absBase+string(filepath.Separator)) && resolvedPath != absBase {
		return fmt.Errorf("path escapes base directory: %s not under %s", resolvedPath, absBase)
	}

	return nil
}

// resolveExistingPrefix resolves symlinks in the longest existing prefix of
// path and re-appends the remaining (not yet created) components. This lets
// containment be enforced for files that do not exist yet without failing on
// them outright.
func resolveExistingPrefix(path string) (string, error) {
	remaining := ""
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remaining == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remaining), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the root without finding an existing component.
			return path, nil
		}
		remaining = filepath.Join(filepath.Base(current), remaining)
		current = parent
	}
}
