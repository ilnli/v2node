//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockDirectory(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("audit spool already in use: %w", err)
	}
	return f, nil
}
func unlockDirectory(f *os.File) error { return f.Close() }
