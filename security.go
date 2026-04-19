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

// validateRemotePath validates paths for remote protocols (FTP, SFTP, SMB)
// These paths may be Unix-style even on Windows
func validateRemotePath(path string) error {
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

// validateSQLQuery checks if a query contains suspicious patterns
func validateSQLQuery(query string) error {
	// Only validate if query contains dangerous patterns in non-string contexts
	// This is a basic check - real SQL injection prevention should use parameterized queries

	// Check for comment sequences that could break queries
	if strings.Contains(query, "--") || strings.Contains(query, "/*") || strings.Contains(query, "*/") {
		return fmt.Errorf("query contains SQL comment sequence: %s", query)
	}

	// Check for multiple semicolons (statement terminator abuse)
	if strings.Count(query, ";") > 1 {
		return fmt.Errorf("query contains multiple statement terminators: %s", query)
	}

	return nil
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

// IsSafePath checks if a path is safe (exported for use in components)
func IsSafePath(path string, checkExistence bool) bool {
	// Check for basic path traversal
	if strings.Contains(path, "..") {
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

// ValidatePath validates a file path for directory traversal
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Check for explicit path traversal pattern
	if strings.Contains(path, "..") {
		return fmt.Errorf("path contains traversal sequence '..': %s", path)
	}

	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte")
	}

	// Clean the path
	cleanPath := filepath.Clean(path)

	// After cleaning, check if path still contains traversal
	if strings.Contains(cleanPath, "..") {
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
