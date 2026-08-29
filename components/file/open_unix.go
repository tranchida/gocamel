//go:build unix

package file

import (
	"os"
	"syscall"
)

// confinedOpen opens path without following a final symlink (O_NOFOLLOW).
// Callers must already confine path with ValidatePathInDir; gosec G304 is
// the variable-path open that this helper exists to perform.
func confinedOpen(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm) // #nosec G304
}
