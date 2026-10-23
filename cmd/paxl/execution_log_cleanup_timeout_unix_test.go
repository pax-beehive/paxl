//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecutionLogCleanupDeadlineReleasesLockAndPreservesProgress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := executionLogDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0700))
	for _, name := range []string{"paxl-20260101T00Z.log", "paxl-20260102T00Z.log"} {
		createExecutionCleanupLog(t, dir, name, time.Now().Add(-73*time.Hour))
	}
	file, err := claimExecutionLogCleanup(dir, time.Now())
	require.NoError(t, err)
	require.NotNil(t, file)
	defer closeFile(file)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestExecutionLogCleanupDeadlineProcess$",
	)
	command.Env = append(os.Environ(), "PAXL_TEST_CLEANUP_DEADLINE=stalled")
	command.ExtraFiles = []*os.File{file}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	require.NoError(t, command.Start())
	closeFile(file)
	err = command.Wait()
	var exited *exec.ExitError
	require.ErrorAs(t, err, &exited)
	require.Equal(
		t,
		124,
		exited.ExitCode(),
		"A stalled cleanup must terminate itself, without the caller killing it. %s",
		stderr.String(),
	)
	require.NoError(t, ctx.Err())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "A completed batch must remain deleted when the worker times out.")
	retry, err := claimExecutionLogCleanup(dir, time.Now().Add(2*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, retry, "The OS must release the dead worker's inherited lock.")
	defer closeFile(retry)
	require.NoError(t, cleanExecutionLogsWithLock(retry, dir))
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "A later invocation must be able to finish the remaining cleanup.")
	require.FileExists(
		t,
		executionLogCleanupLockPath(dir),
		"Recovery must not require deleting the lock file.",
	)
}

func TestExecutionLogCleanupDeadlineProcess(_ *testing.T) {
	if os.Getenv("PAXL_TEST_CLEANUP_DEADLINE") == "" {
		return
	}
	mode := os.Getenv("PAXL_TEST_CLEANUP_DEADLINE")
	err := runExecutionLogCleanupWorker(func(o *executionLogCleanupWorkerOption) {
		o.MaxRuntime = 500 * time.Millisecond
		switch mode {
		case "stalled":
			o.Clean = func(_ *os.File, dir string) error {
				return pruneExecutionLogs(
					dir,
					time.Now().Add(-72*time.Hour),
					func(batch *executionLogCleanupOption) {
						batch.BatchSize = 1
						// Model a filesystem call that never returns within the worker's deadline.
						batch.Sleep = func(time.Duration) { time.Sleep(time.Hour) }
					},
				)
			}
		case "error":
			o.Clean = func(*os.File, string) error { return errors.New("cleanup failed") }
		}
	})
	// Returning from the worker must cancel the deadline even when cleanup failed.
	time.Sleep(time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	os.Exit(0)
}

func TestExecutionLogCleanupUsesBoundedDefaultRuntime(t *testing.T) {
	require.Equal(t, 10*time.Minute, executionLogCleanupMaxRuntime)
	require.Less(t, executionLogCleanupMaxRuntime, time.Hour)
}

func TestExecutionLogCleanupCancelsDeadlineAfterReturning(t *testing.T) {
	for _, scenario := range []struct {
		mode     string
		exitCode int
	}{{"completed", 0}, {"error", 2}} {
		t.Run(scenario.mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			dir, err := executionLogDir()
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(dir, 0700))
			file, err := claimExecutionLogCleanup(dir, time.Now())
			require.NoError(t, err)
			require.NotNil(t, file)
			defer closeFile(file)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(
				ctx,
				os.Args[0],
				"-test.run=^TestExecutionLogCleanupDeadlineProcess$",
			)
			command.Env = append(os.Environ(), "PAXL_TEST_CLEANUP_DEADLINE="+scenario.mode)
			command.ExtraFiles = []*os.File{file}
			if scenario.exitCode == 0 {
				output, err := command.CombinedOutput()
				require.NoError(t, err, string(output))
			} else {
				err := command.Run()
				var exited *exec.ExitError
				require.ErrorAs(t, err, &exited)
				require.Equal(
					t,
					scenario.exitCode,
					exited.ExitCode(),
					"A completed worker must not be terminated by a stale timer.",
				)
			}
			require.NoError(t, ctx.Err())
		})
	}
}

func TestExecutionLogCleanupRejectsUnboundedRuntime(t *testing.T) {
	require.ErrorContains(
		t,
		runExecutionLogCleanupWorker(func(o *executionLogCleanupWorkerOption) { o.MaxRuntime = 0 }),
		"runtime must be positive",
	)
}
