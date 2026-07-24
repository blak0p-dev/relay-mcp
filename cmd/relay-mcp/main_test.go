package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestWriteError_UsesPublicRelayPrefix(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	writeError(&stderr, errors.New("serve stdio: broken pipe"))

	if got, want := stderr.String(), "relay: serve stdio: broken pipe\n"; got != want {
		t.Fatalf("writeError output = %q, want %q", got, want)
	}
}
