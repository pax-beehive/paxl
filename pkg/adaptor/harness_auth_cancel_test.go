//go:build !windows

package adaptor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeAuthCancellationStopsLoginProcess(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	command := filepath.Join(dir, "codex")
	require.NoError(
		t,
		os.WriteFile(
			command,
			[]byte("#!/bin/sh\necho ready > '"+ready+"'\nsleep 30 &\nwait\n"),
			0700,
		),
	)
	t.Setenv("CODEX_PATH", command)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewNativeHarnessAuth().Run(ctx, &NativeAuthRequest{Harness: "codex", Operation: "login"})
	}()
	require.Eventually(
		t,
		func() bool { _, err := os.Stat(ready); return err == nil },
		3*time.Second,
		10*time.Millisecond,
	)
	cancel()
	select {
	case err := <-done:
		require.True(t, errors.Is(err, context.Canceled))
	case <-time.After(3 * time.Second):
		t.Fatal("Canceled login did not exit")
	}
}
