//go:build unix

package app

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock, so two processes never hand out the
// same send slot.
func lockFile(file *os.File) (func(), error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}
