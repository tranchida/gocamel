package gocamel

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// SecurityValidator provides common security validation functions for GoCamel
// to prevent various injection and traversal attacks.

// ValidateRemotePath validates paths for remote protocols (FTP, SFTP, SMB)
// These paths may be Unix-style even on Windows.
//
// It rejects ASCII control characters (including null bytes and CR/LF) and '..'
// traversal that escapes the base: an absolute path such as "/etc/passwd" is
// accepted. For a file name taken from a message header use ConfineFileName or
// JoinRemotePath, which also reject absolute paths, Windows drive prefixes,
// and any '..' segment.
func ValidateRemotePath(path string) error {
	if path == "" {
		return nil
	}

	// Check for embedded null bytes and ASCII control characters (CR/LF, etc.)
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte")
	}
	if containsControlChar(path) {
		return fmt.Errorf("path contains control character")
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

	return nil
}

// ConfineFileName rejects a payload file name that would escape a directory
// when concatenated or filepath.Joined: empty names, null bytes, ASCII control
// characters, absolute paths, Windows volume/drive prefixes, and any '..'
// segment (even when balanced lexically, because an intermediate symlink could
// traverse outside the confined directory).
func ConfineFileName(name string) error {
	if name == "" {
		return fmt.Errorf("file name cannot be empty")
	}
	if strings.Contains(name, "\x00") {
		return fmt.Errorf("file name contains null byte")
	}
	if containsControlChar(name) {
		return fmt.Errorf("file name contains control character")
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" || hasWindowsDrivePrefix(name) {
		return fmt.Errorf("file name must be a relative path: %s", name)
	}
	normalized := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("file name must be a relative path: %s", name)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return fmt.Errorf("file name contains traversal: %s", name)
		}
	}
	if clean := path.Clean(normalized); clean == "." || clean == "/" || clean == "" {
		return fmt.Errorf("file name must not resolve to current or root directory: %s", name)
	}
	return nil
}

func containsControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func hasWindowsDrivePrefix(s string) bool {
	return len(s) >= 2 && isASCIIAlpha(s[0]) && s[1] == ':'
}

// JoinRemotePath concatenates a remote directory URI path with a payload file
// name after ConfineFileName. dir should be the endpoint path (possibly with a
// trailing slash). The result uses forward slashes.
func JoinRemotePath(dir, name string) (string, error) {
	if err := ValidateRemotePath(dir); err != nil {
		return "", err
	}
	if err := ConfineFileName(name); err != nil {
		return "", err
	}
	dir = strings.ReplaceAll(dir, "\\", "/")
	name = strings.ReplaceAll(name, "\\", "/")
	if dir == "" || dir == "/" {
		return name, nil
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir + name, nil
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
// Only ';' characters that fall OUTSIDE string literals and comments are
// considered, so a semicolon inside a quoted value does not trip the check.
// Because validateSQLQuery has no database-driver context, it scans the query
// under standard/SQLite, PostgreSQL, and MySQL lexical rules (including both
// active and version-skipped MySQL '/*! ... */' executable comments) and
// rejects if ANY dialect reading observes stacked statements. Consequently,
// dialect-specific constructs that another dialect parses as stacked code
// (e.g. \' or \", PostgreSQL $$...$$ containing ';', or MySQL trailing '#'
// comments) are rejected unless written portably (for example, doubling quotes).
//
// IMPORTANT: this is NOT a security boundary. When an endpoint opts in with
// allowHeaderOverride=true, the CamelSqlQuery header replaces the statement
// wholesale and is fully attacker-controlled if the exchange carries untrusted
// headers: a single data-modifying statement such as "DELETE FROM users" passes
// this check unchanged. The real boundary is the opt-in flag, which defaults to
// false — do not enable it where headers can be influenced by untrusted input.
func validateSQLQuery(query string) error {
	for _, dialect := range sqlDialects {
		if hasNonTrailingSemicolonOutsideStrings(query, dialect) {
			return fmt.Errorf("query contains stacked SQL statements: %s", query)
		}
	}
	return nil
}

type sqlDialect struct {
	backslashEscapes bool // \' and \" escape quotes inside '...' and "..." (MySQL)
	dollarQuotes     bool // $$ and $tag$ dollar-quoted string literals (PostgreSQL)
	hashComments     bool // '#' opens a line comment (MySQL)
	execComments     bool // /*! ... */ executes as SQL (MySQL matching server version)
	crEndsComment    bool // '\r' terminates '--' line comments (PostgreSQL)
}

var sqlDialects = []sqlDialect{
	// Standard SQL / SQLite / Oracle / SQL Server
	{backslashEscapes: false, dollarQuotes: false, hashComments: false, execComments: false, crEndsComment: false},
	// PostgreSQL
	{backslashEscapes: false, dollarQuotes: true, hashComments: false, execComments: false, crEndsComment: true},
	// MySQL with active executable comments (/*! ... */)
	{backslashEscapes: true, dollarQuotes: false, hashComments: true, execComments: true, crEndsComment: false},
	// MySQL when /*!version ... */ is skipped as a normal block comment
	{backslashEscapes: true, dollarQuotes: false, hashComments: true, execComments: false, crEndsComment: false},
}

// hasNonTrailingSemicolonOutsideStrings reports whether a statement terminator
// is followed by SQL code under the given dialect's lexical rules. Whitespace
// and benign comments may follow a single trailing semicolon, while
// "SELECT 1; DROP TABLE t" is rejected.
func hasNonTrailingSemicolonOutsideStrings(query string, dialect sqlDialect) bool {
	inSingle := false
	inDouble := false
	inBacktick := false
	inLineComment := false
	inBlockComment := false
	inExecComment := false
	seenTerminator := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if inLineComment {
			if c == '\n' || (dialect.crEndsComment && c == '\r') {
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
			if dialect.backslashEscapes && c == '\\' && i+1 < len(query) {
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
			if dialect.backslashEscapes && c == '\\' && i+1 < len(query) {
				i++
				continue
			}
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
			if seenTerminator {
				return true
			}
			inSingle = true
		case '"':
			if seenTerminator {
				return true
			}
			inDouble = true
		case '`':
			if seenTerminator {
				return true
			}
			inBacktick = true
		case '$':
			if seenTerminator {
				return true
			}
			if dialect.dollarQuotes {
				if tag, ok := matchDollarQuoteTag(query[i:]); ok {
					if endRel := strings.Index(query[i+len(tag):], tag); endRel >= 0 {
						i += len(tag) + endRel + len(tag) - 1
					}
				}
			}
		case '#':
			if dialect.hashComments {
				inLineComment = true
			} else if seenTerminator {
				return true
			}
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				inLineComment = true
				i++
			} else if seenTerminator {
				return true
			}
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				if dialect.execComments && i+2 < len(query) && query[i+2] == '!' {
					// MySQL executable comments (/*! ... */) are executed as SQL.
					if seenTerminator {
						return true
					}
					inExecComment = true
					i += 2
				} else {
					inBlockComment = true
					i++
				}
			} else if seenTerminator {
				return true
			}
		case '*':
			if inExecComment && i+1 < len(query) && query[i+1] == '/' {
				inExecComment = false
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

func isDollarQuoteStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || b >= 0x80
}

func isDollarQuoteCont(b byte) bool {
	return isDollarQuoteStart(b) || (b >= '0' && b <= '9')
}

// matchDollarQuoteTag returns the PostgreSQL dollar-quote delimiter ($tag$) at
// the start of s when tag is empty or matches PostgreSQL's dolq_start/dolq_cont
// rules (ASCII letters, '_', non-ASCII bytes 0x80-0xff, and digits after the
// first byte).
func matchDollarQuoteTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	if s[1] == '$' {
		return "$$", true
	}
	if !isDollarQuoteStart(s[1]) {
		return "", false
	}
	for j := 2; j < len(s); j++ {
		if s[j] == '$' {
			return s[:j+1], true
		}
		if !isDollarQuoteCont(s[j]) {
			return "", false
		}
	}
	return "", false
}

// sanitizer is a regex-based sanitizer for common patterns.
// Traversal detection ('..') is handled by containsTraversal, not regexes:
// a fixed pattern cannot weigh '..' against the actual depth of the path.
var sanitizer = struct {
	nullBytes    *regexp.Regexp
	controlChars *regexp.Regexp
}{
	nullBytes:    regexp.MustCompile(`\x00`),
	controlChars: regexp.MustCompile(`[\x00-\x1f\x7f]`),
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
// Like ValidatePath it does not confine the path: absolute paths are accepted.
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
//
// ValidatePath does NOT confine a path: absolute paths ("/etc/passwd") are
// accepted. For paths derived from message headers use ValidatePathInDir
// (containment in a base directory, symlinks resolved) or ConfineFileName.
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

	// Resolve symlinks left-to-right component-by-component BEFORE folding '..'
	// (filepath.Clean/Abs fold '<symlink>/..' lexically before symlink resolution).
	// Both baseDir and path are resolved through their longest existing prefix so
	// not-yet-created subdirectories remain comparable in the real namespace.
	resolvedBase, err := resolveExistingPrefix(baseDir)
	if err != nil {
		return fmt.Errorf("failed to resolve base directory: %w", err)
	}

	resolvedPath, err := resolveExistingPrefix(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Ensure path is within base directory. A root baseDir ("/" on Unix or
	// "C:\" on Windows) already ends with the separator.
	prefix := resolvedBase
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(resolvedPath, prefix) && resolvedPath != resolvedBase {
		return fmt.Errorf("path escapes base directory: %s not under %s", resolvedPath, resolvedBase)
	}

	return nil
}

// resolveExistingPrefix resolves symlinks left-to-right across the existing
// prefix of path before folding '..' components, rejects dangling symlinks, and
// re-appends any remaining (not yet created) components.
func resolveExistingPrefix(path string) (string, error) {
	rawAbs, err := toRawAbsPath(path)
	if err != nil {
		return "", err
	}

	vol := filepath.VolumeName(rawAbs)
	rest := rawAbs[len(vol):]
	root := vol + string(filepath.Separator)

	current, currentExists, currentIsDir, err := resolveExistingStep(root)
	if err != nil {
		return "", err
	}

	for _, part := range splitPathComponents(rest) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if !currentExists || !currentIsDir {
				return "", fmt.Errorf("cannot traverse '..' through non-existent or non-directory path: %s", current)
			}
			current = filepath.Dir(current)
			continue
		}
		if !currentExists {
			current = filepath.Join(current, part)
			continue
		}
		if !currentIsDir {
			return "", fmt.Errorf("path component is not a directory: %s", current)
		}
		current, currentExists, currentIsDir, err = resolveExistingStep(filepath.Join(current, part))
		if err != nil {
			return "", err
		}
	}

	return current, nil
}

// resolveExistingStep inspects candidate with Lstat. If candidate does not
// exist, exists is false with a nil error. Symlinks are resolved and dangling
// symlinks are rejected.
func resolveExistingStep(candidate string) (resolved string, exists bool, isDir bool, err error) {
	fi, err := os.Lstat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return candidate, false, false, nil
		}
		return "", false, false, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return candidate, true, fi.IsDir(), nil
	}
	target, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, false, fmt.Errorf("dangling symlink: %s", candidate)
		}
		return "", false, false, err
	}
	tfi, err := os.Stat(target)
	if err != nil {
		return "", false, false, err
	}
	return target, true, tfi.IsDir(), nil
}

func toRawAbsPath(path string) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if vol := filepath.VolumeName(path); vol != "" {
		volBase, err := filepath.Abs(vol + ".")
		if err != nil {
			return "", err
		}
		return joinRawPath(volBase, path[len(vol):]), nil
	}
	if len(path) > 0 && os.IsPathSeparator(path[0]) {
		return filepath.VolumeName(wd) + path, nil
	}
	return joinRawPath(wd, path), nil
}

func joinRawPath(base, rel string) string {
	if strings.HasSuffix(base, string(filepath.Separator)) {
		return base + rel
	}
	return base + string(filepath.Separator) + rel
}

func splitPathComponents(p string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(p); i++ {
		if os.IsPathSeparator(p[i]) {
			if i > start {
				parts = append(parts, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		parts = append(parts, p[start:])
	}
	return parts
}
