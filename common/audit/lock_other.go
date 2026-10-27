//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package audit

import (
	"errors"
	"os"
)

func lockDirectory(string) (*os.File, error) {
	return nil, errors.New("audit disk cache currently requires Unix file locking")
}
func unlockDirectory(f *os.File) error { return f.Close() }
