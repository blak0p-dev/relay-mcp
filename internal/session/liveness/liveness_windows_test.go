//go:build windows

package liveness

import (
	"os"
	"testing"
)

func TestIsAlive_WindowsOwnPID(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Fatalf("IsAlive(self) = false, want true (pid=%d)", os.Getpid())
	}
}
