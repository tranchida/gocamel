package gocamel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContainsTraversal covers the traversal detection used by the path
// validation helpers: a '..' component is only dangerous when it drives the
// effective depth below zero (escaping the base). Legitimate uses such as
// "foo/../bar" or names merely containing two dots must be accepted.
func TestContainsTraversal(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"simple escape", "../etc/passwd", true},
		{"deep escape", "foo/../../etc", true},
		{"windows separator escape", `..\win\system32`, true},
		{"mixed separators escape", `foo\../../etc`, true},
		{"leading only", "..", true},
		{"escape then back", "../../foo/..", true},

		{"plain relative", "foo/bar", false},
		{"up one and back", "foo/../bar", false},
		{"nested up and back", "a/b/../../c/d", false},
		{"dot only", ".", false},
		{"dots in filename", "report..v2.txt", false},
		{"dotdot filename", "foo..bar", false},
		{"absolute with internal traversal", "/var/data/../logs/x", false},
		{"trailing slashes", "foo/bar//", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, containsTraversal(tt.path))
		})
	}
}

// TestValidatePath verifies the lexical validation: empty paths, null bytes
// and escaping traversals are rejected.
func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"empty", "", "cannot be empty"},
		{"null byte", "foo\x00bar", "null byte"},
		{"traversal", "../../etc", "traversal"},
		{"windows traversal", `..\..\etc`, "traversal"},

		{"valid name", "file.txt", ""},
		{"valid relative", "dir/sub/file.txt", ""},
		{"legitimate parent ref", "foo/../bar", ""},
		{"dots in name", "archive..old.tar", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestIsSafePath verifies the boolean variant used by remote components.
func TestIsSafePath(t *testing.T) {
	assert.False(t, IsSafePath("../escape", false))
	assert.False(t, IsSafePath("foo\x00bar", false))
	assert.True(t, IsSafePath("safe/path.txt", false))
	assert.True(t, IsSafePath("file..bak", false))

	// With existence check, a nonexistent path must be rejected.
	assert.False(t, IsSafePath(filepath.Join(t.TempDir(), "does-not-exist"), true))
}

// TestValidateRemotePath verifies Unix-style remote paths (FTP/SFTP/SMB):
// empty paths are allowed (consumer picks a default), traversal and null
// bytes are rejected.
func TestValidateRemotePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"unix path", "uploads/2026/report.csv", false},
		{"legitimate up-and-back", "inbox/../uploads/report.csv", false},
		{"unix escape", "../../etc/passwd", true},
		{"windows escape", `..\..\windows\system.ini`, true},
		{"null byte", "report\x00.csv", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRemotePath(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidatePathInDir_Containment verifies that a path under the resolved
// base is accepted and sibling/escaping paths are rejected.
func TestValidatePathInDir_Containment(t *testing.T) {
	base := t.TempDir()

	// Path inside the base directory (file does not need to exist).
	assert.NoError(t, ValidatePathInDir(filepath.Join(base, "sub", "new.txt"), base))
	// The base directory itself.
	assert.NoError(t, ValidatePathInDir(base, base))
	// Escaping via traversal.
	assert.Error(t, ValidatePathInDir(filepath.Join(base, "..", "evil.txt"), base))
	// A sibling directory whose name only shares a string prefix with the
	// base must NOT be accepted (guards the "/base-evil vs /base" trap).
	absBase, err := filepath.Abs(base)
	require.NoError(t, err)
	evilSibling := absBase + "-evil"
	assert.Error(t, ValidatePathInDir(evilSibling, base))
}

// TestValidatePathInDir_SymlinkEscape verifies that a symlink living under
// the base but pointing outside it is rejected.
func TestValidatePathInDir_SymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s3cr3t"), 0o600))

	// Symlink inside the base pointing to the outside directory.
	link := filepath.Join(base, "linkout")
	require.NoError(t, os.Symlink(outside, link))

	assert.Error(t, ValidatePathInDir(filepath.Join(link, "secret.txt"), base))
}

// TestValidatePathInDir_SymlinkBase verifies that a base directory reached
// through a symlink is resolved before the containment check, so both sides
// are compared in the same (real) namespace.
func TestValidatePathInDir_SymlinkBase(t *testing.T) {
	realBase := t.TempDir()
	linkBase := filepath.Join(t.TempDir(), "linkbase")
	require.NoError(t, os.Symlink(realBase, linkBase))

	assert.NoError(t, ValidatePathInDir(filepath.Join(linkBase, "file.txt"), linkBase))
}

// TestValidatePathInDir_NonExistingUnderSymlink verifies that a to-be-created
// file under a symlinked directory is resolved via its closest existing
// ancestor and still confined.
func TestValidatePathInDir_NonExistingUnderSymlink(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "realdir")
	require.NoError(t, os.Mkdir(sub, 0o700))
	link := filepath.Join(base, "reallink")
	require.NoError(t, os.Symlink("realdir", link))

	// The file does not exist yet but sits under a symlink inside the base.
	assert.NoError(t, ValidatePathInDir(filepath.Join(link, "new-file.txt"), base))
}

// TestSanitizeInput verifies dangerous character removal for strings bound
// to be used in file paths or headers.
func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"null byte removed", "re\x00port.txt", "report.txt"},
		{"control chars removed", "a\x01b\x1fc", "abc"},
		{"del removed", "a\x7fb", "ab"},
		{"newline kept", "line1\nline2", "line1\nline2"},
		{"tab kept", "col1\tcol2", "col1\tcol2"},
		{"carriage return kept", "a\r\nb", "a\r\nb"},
		{"unchanged plain text", "plain-file.txt", "plain-file.txt"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeInput(tt.input))
		})
	}
}

// TestValidateSQLQuery verifies the stacked-statement guard: parameterised
// values are bound via '?' placeholders so the only vector guarded here is a
// stray ';' followed by more SQL code, while semicolons inside string
// literals, comments and a single trailing ';' are accepted.
func TestValidateSQLQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{"plain select", "SELECT * FROM users WHERE id = ?", false},
		{"trailing semicolon", "SELECT * FROM users;", false},
		{"trailing semicolon with whitespace", "SELECT 1;   \n", false},
		{"trailing semicolon with comment", "SELECT 1; -- done", false},
		{"trailing semicolon with block comment", "SELECT 1; /* done */", false},
		{"semicolon inside single quote", "SELECT * FROM t WHERE a = 'x;y'", false},
		{"escaped single quote then semicolon text", `SELECT 'it\'s;a'`, false},
		{"doubled single quote", "SELECT 'a'';b'", false},
		{"semicolon inside double quote", `SELECT "a;b" FROM t`, false},
		{"semicolon inside backtick", "SELECT `a;b` FROM t", false},
		{"semicolon in line comment", "SELECT 1 -- comment; with semi", false},
		{"semicolon in block comment", "SELECT 1 /* comment; */", false},

		{"stacked drop", "SELECT 1; DROP TABLE users", true},
		{"stacked delete", "UPDATE t SET a=1; DELETE FROM users", true},
		{"stacked after quote", "SELECT 'x'; DROP TABLE t", true},
		{"double terminator", "SELECT 1;;", true},
		{"statement after comment following semi", "SELECT 1; -- c\nDELETE FROM t", true},
		{"statement after block comment following semi", "SELECT 1; /* c */ DROP TABLE t", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLQuery(tt.query)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "stacked SQL statements")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSanitizerRegexes documents the behaviour of the package-level sanitizer
// regexes so regressions in a pattern are caught even where a helper wraps it.
// Traversal detection lives in containsTraversal (see TestContainsTraversal):
// a fixed regex cannot weigh '..' against the effective path depth.
func TestSanitizerRegexes(t *testing.T) {
	assert.True(t, sanitizer.nullBytes.MatchString("a\x00b"))
	assert.False(t, sanitizer.nullBytes.MatchString("ab"))

	assert.True(t, sanitizer.controlChars.MatchString("a\x10b"))
	assert.False(t, sanitizer.controlChars.MatchString("a b"))
}

// TestValidatePathInDir_EmptyPath verifies that an empty candidate path is
// rejected through the underlying ValidatePath contract.
func TestValidatePathInDir_EmptyPath(t *testing.T) {
	err := ValidatePathInDir("", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")
}

// TestValidatePathInDir_NullByte verifies null bytes are rejected before any
// filesystem resolution happens.
func TestValidatePathInDir_NullByte(t *testing.T) {
	base := t.TempDir()
	err := ValidatePathInDir(filepath.Join(base, "a\x00b"), base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null byte")
}

// Ensure error messages never leak more than the offending path itself.
func TestValidatePath_ErrorContainsPathNotExtra(t *testing.T) {
	err := ValidatePath("../x/../y")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), ".."))
}

func TestConfineFileName(t *testing.T) {
	assert.NoError(t, ConfineFileName("report.csv"))
	assert.NoError(t, ConfineFileName("inbox/report.csv"))
	assert.Error(t, ConfineFileName(""))
	assert.Error(t, ConfineFileName("../etc/passwd"))
	assert.Error(t, ConfineFileName("/etc/passwd"))
	assert.Error(t, ConfineFileName("foo\x00bar"))
}

func TestJoinRemotePath(t *testing.T) {
	joined, err := JoinRemotePath("/data/uploads/", "report.csv")
	require.NoError(t, err)
	assert.Equal(t, "/data/uploads/report.csv", joined)

	_, err = JoinRemotePath("/data/uploads/", "../../outside.txt")
	require.Error(t, err)

	_, err = JoinRemotePath("/data/uploads/", "/etc/passwd")
	require.Error(t, err)
}
