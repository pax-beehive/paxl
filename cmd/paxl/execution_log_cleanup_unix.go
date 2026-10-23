//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var errExecutionLogCleanupNotDue = errors.New("log cleanup is not due")

func claimExecutionLogCleanup(dir string, now time.Time) (*os.File, error) {
	file, err := os.OpenFile(
		executionLogCleanupLockPath(dir),
		os.O_CREATE|os.O_RDWR,
		0600,
	) // #nosec G304 -- Fixed local lock path.
	if err != nil {
		return nil, fmt.Errorf("open log cleanup lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeFile(file)
		return nil, fmt.Errorf("lock log cleanup: %w", err)
	}
	if !cleanupIsDue(file, now) {
		closeFile(file)
		return nil, errExecutionLogCleanupNotDue
	}
	// Back off after a crash or failed spawn; successful scans postpone for an hour.
	if err := deferExecutionLogCleanup(file, now.Add(time.Minute)); err != nil {
		closeFile(file)
		return nil, err
	}
	return file, nil
}

func startExecutionLogCleanup(dir string) {
	file, err := claimExecutionLogCleanup(dir, time.Now())
	if err != nil || file == nil {
		return
	}
	defer closeFile(file)
	command, err := executionLogCleanupCommand()
	if err != nil {
		return
	}
	// Inherit the same flock through fd 3 so no second process can start a scan.
	command.ExtraFiles = []*os.File{file}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Nil streams use /dev/null, preventing inherited terminal pipes from staying open.
	if err := command.Start(); err != nil {
		return
	}
	go func() { _ = command.Wait() }()
}

func runExecutionLogCleanupWorker() error {
	dir, err := executionLogDir()
	if err != nil {
		return fmt.Errorf("resolve cleanup log directory: %w", err)
	}
	file := os.NewFile(3, "log-cleanup.lock")
	if file == nil {
		return fmt.Errorf("missing inherited cleanup lock")
	}
	defer closeFile(file)
	inherited, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect inherited cleanup lock: %w", err)
	}
	expected, err := os.Stat(executionLogCleanupLockPath(dir))
	if err != nil || !os.SameFile(inherited, expected) {
		return fmt.Errorf("invalid inherited cleanup lock")
	}
	return cleanExecutionLogsWithLock(file, dir)
}
