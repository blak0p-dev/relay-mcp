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
	return syscall.SIGTERM
}

func forceKillSignal() syscall.Signal {
	return syscall.SIGKILL
}

func isMissingProcessError(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
