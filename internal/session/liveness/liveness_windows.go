//go:build windows

package liveness

import "golang.org/x/sys/windows"

// IsAlive returns true if Windows can open a process with the given pid.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	return true
}
