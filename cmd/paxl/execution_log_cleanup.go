package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const executionLogCleanupMaxRuntime = 10 * time.Minute

type executionLogCleanupWorkerOption struct {
	MaxRuntime time.Duration
	Clean      func(*os.File, string) error
}

const executionLogCleanupWorkerFlag = "__paxl-cleanup-logs" // #nosec G101 -- Internal command switch, not a credential.

var executionLogCleanupCommand = func() (*exec.Cmd, error) {
	executable, err := executablePath()
	if err != nil {
		return nil, fmt.Errorf("find cleanup executable: %w", err)
	}
	// The detached worker must outlive a short CLI invocation.
	return exec.Command(
		executable,
		executionLogCleanupWorkerFlag,
	), nil // #nosec G204 -- Current executable and fixed argument.
}

func executionLogCleanupLockPath(dir string) string {
	return filepath.Join(filepath.Dir(dir), "log-cleanup.lock")
}

func cleanupIsDue(file *os.File, now time.Time) bool {
	var stamp [32]byte
	n, _ := file.ReadAt(stamp[:], 0)
	next, err := strconv.ParseInt(strings.TrimSpace(string(stamp[:n])), 10, 64)
	return err != nil || !now.Before(time.Unix(0, next))
}

func deferExecutionLogCleanup(file *os.File, until time.Time) error {
	stamp := []byte(fmt.Sprintf("%020d\n", until.UnixNano()))
	if _, err := file.WriteAt(stamp, 0); err != nil {
		return fmt.Errorf("write log cleanup schedule: %w", err)
	}
	return nil
}

func cleanExecutionLogsWithLock(file *os.File, dir string) error {
	err := pruneExecutionLogs(dir, time.Now().Add(-72*time.Hour))
	delay := time.Hour
	if err != nil {
		delay = time.Minute
	}
	if stampErr := deferExecutionLogCleanup(file, time.Now().Add(delay)); stampErr != nil {
		return stampErr
	}
	return err
}
