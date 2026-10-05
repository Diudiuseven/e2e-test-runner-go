//go:build unix

package main

import (
	"syscall"
)

// sendInterrupt delivers SIGINT to the test process itself. The CLI's
// signal.NotifyContext converts it into run cancellation; the test only
// exercises the same path a terminal Ctrl-C would take.
func sendInterrupt() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGINT)
}
