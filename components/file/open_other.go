//go:build !unix

package file

import "os"

// confinedOpen opens path. Unix builds also set O_NOFOLLOW; callers must
// already confine path with ValidatePathInDir (gosec G304).
func confinedOpen(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm) // #nosec G304
}
