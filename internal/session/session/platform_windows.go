//go:build windows

package session

import (
	"os"
	"syscall"
)

func signalProcessGroup(pid int, _ syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer process.Release()
	return process.Kill()
}

func isPlatformTerminalReadEnd(error) bool {
	return false
}

func terminateSignal() syscall.Signal {
	return syscall.Signal(15)
}

func forceKillSignal() syscall.Signal {
	return syscall.Signal(9)
}

func isMissingProcessError(error) bool {
	return false
}
