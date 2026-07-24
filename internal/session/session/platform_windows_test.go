//go:build windows

package session

import (
	"errors"
	"testing"
)

func TestWindowsPlatformHelpers_UseSignalsAndDoNotTreatArbitraryErrorsAsPTYEnd(t *testing.T) {
	if terminateSignal() == 0 || forceKillSignal() == 0 {
		t.Fatal("Windows termination signals must be non-zero")
	}
	if isPlatformTerminalReadEnd(errors.New("read failed")) {
		t.Fatal("arbitrary read error reported as a terminal end")
	}
}
