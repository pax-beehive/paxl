//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecutionLogCleanupLocksAndRetriesAfterInterruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	require.NoError(t, os.Mkdir(dir, 0700))
	now := time.Now()
	first, err := claimExecutionLogCleanup(dir, now)
	require.NoError(t, err)
	require.NotNil(t, first)
	defer closeFile(first)
	second, err := claimExecutionLogCleanup(dir, now.Add(time.Hour))
	require.Error(t, err)
	require.Nil(
		t,
		second,
		"Only one process may hold the cleanup lock, even after the retry interval.",
	)
	closeFile(first)
	second, err = claimExecutionLogCleanup(dir, now)
	require.ErrorIs(t, err, errExecutionLogCleanupNotDue)
	require.Nil(t, second, "A failed or interrupted launch must not be retried by every command.")
	second, err = claimExecutionLogCleanup(dir, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, second, "Closing the lock must recover without removing stale lock files.")
	closeFile(second)
	_, err = claimExecutionLogCleanup(filepath.Join(dir, "missing", "logs"), now)
	require.Error(t, err)
}

func TestExecutionLogCleanupStartsDetachedAndKeepsInheritedLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := executionLogDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0700))
	old := time.Now().Add(-73 * time.Hour)
	name := "paxl-20260101T000000.000000000Z-123.log"
	createExecutionCleanupLog(t, dir, name, old)
	gate := filepath.Join(t.TempDir(), "gate")
	done := filepath.Join(t.TempDir(), "done")
	previous := executionLogCleanupCommand
	t.Cleanup(func() { executionLogCleanupCommand = previous })
	executionLogCleanupCommand = func() (*exec.Cmd, error) {
		command := exec.CommandContext(
			t.Context(),
			os.Args[0],
			"-test.run=^TestExecutionLogCleanupWorkerProcess$",
		)
		command.Env = append(
			os.Environ(),
			"PAXL_TEST_CLEANUP_HELPER=1",
			"PAXL_TEST_CLEANUP_GATE="+gate,
			"PAXL_TEST_CLEANUP_DONE="+done,
		)
		return command, nil
	}
	startExecutionLogCleanup(dir)
	// The worker deliberately waits on a gate: the scheduling call must return anyway.
	require.FileExists(t, filepath.Join(dir, name))
	competing, err := claimExecutionLogCleanup(dir, time.Now().Add(2*time.Hour))
	require.Error(t, err)
	if competing != nil {
		closeFile(competing)
		t.Fatal("Worker did not inherit the process lock.")
	}
	require.NoError(t, os.WriteFile(gate, nil, 0600))
	require.Eventually(
		t,
		func() bool { _, err := os.Stat(done); return err == nil },
		10*time.Second,
		10*time.Millisecond,
	)
	require.NoFileExists(t, filepath.Join(dir, name))
	competing, err = claimExecutionLogCleanup(dir, time.Now().Add(30*time.Minute))
	require.ErrorIs(t, err, errExecutionLogCleanupNotDue)
	require.Nil(t, competing, "A completed scan must defer the next scan for one hour.")
}

func TestExecutionLogCleanupWorkerProcess(_ *testing.T) {
	if os.Getenv("PAXL_TEST_CLEANUP_HELPER") != "1" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("PAXL_TEST_CLEANUP_GATE")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(2)
		}
		time.Sleep(time.Millisecond)
	}
	if err := runExecutionLogCleanupWorker(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("PAXL_TEST_CLEANUP_DONE"), nil, 0600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestExecutionLogCleanupLaunchFailuresDoNotBlockCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := executionLogDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0700))
	previous := executionLogCleanupCommand
	t.Cleanup(func() { executionLogCleanupCommand = previous })
	original, err := previous()
	require.NoError(t, err)
	require.Equal(t, []string{original.Path, executionLogCleanupWorkerFlag}, original.Args)
	executionLogCleanupCommand = func() (*exec.Cmd, error) { return nil, errors.New("unavailable executable") }
	startExecutionLogCleanup(dir)
	require.NoError(t, os.Remove(executionLogCleanupLockPath(dir)))
	executionLogCleanupCommand = func() (*exec.Cmd, error) {
		return exec.CommandContext(t.Context(), filepath.Join(dir, "missing-binary")), nil
	}
	startExecutionLogCleanup(dir)
	startExecutionLogCleanup(dir) // Backoff avoids repeatedly trying to launch a broken worker.
	startExecutionLogCleanup(filepath.Join(dir, "missing", "logs"))
}
