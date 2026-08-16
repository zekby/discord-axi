//go:build unix

package app

import "syscall"

// detachAttrs puts the daemon in its own session, so it survives the shell or
// agent that started it and never receives that terminal's signals.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
