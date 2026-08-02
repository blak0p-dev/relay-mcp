//go:build !windows

package session

import (
	"errors"
	"syscall"
)

func signalProcessGroup(pgid int, signal syscall.Signal) error {
	return syscall.Kill(-pgid, signal)
}

func isPlatformTerminalReadEnd(err error) bool {
	return errors.Is(err, syscall.EIO)
}

func terminateSignal() syscall.Signal {
	// Closing a terminal sends SIGHUP. Bash handles it reliably even while
	// still initializing its interactive signal handlers.
	return syscall.SIGHUP
}

func forceKillSignal() syscall.Signal {
	return syscall.SIGKILL
}

func isMissingProcessError(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
