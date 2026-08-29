//go:build !unix

package file

import "os"

func confinedOpen(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}
