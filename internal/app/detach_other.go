//go:build !unix

package app

import "syscall"

// ponytail: no session detachment outside unix; the daemon still runs, it just
// shares the parent's process group.
func detachAttrs() *syscall.SysProcAttr { return nil }
