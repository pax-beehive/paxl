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

func runExecutionLogCleanupWorker(opts ...func(*executionLogCleanupWorkerOption)) error {
	option := &executionLogCleanupWorkerOption{
		MaxRuntime: executionLogCleanupMaxRuntime,
		Clean:      cleanExecutionLogsWithLock,
	}
	for _, opt := range opts {
		opt(option)
	}
	if option.MaxRuntime <= 0 {
		return fmt.Errorf("cleanup runtime must be positive")
	}
	// A context cannot interrupt a stalled filesystem operation. Exiting the worker
	// bounds its lifetime and lets the OS release the inherited lock and file handles.
	deadline := time.AfterFunc(option.MaxRuntime, func() { os.Exit(124) })
	defer deadline.Stop()

	dir, err := executionLogDir()
	if err != nil {
		return fmt.Errorf("resolve cleanup log directory: %w", err)
	}
	// Validate before wrapping fd 3: a malformed invocation may have a Go runtime
	// descriptor there instead of an inherited lock, which must not be closed here.
	var inherited, expected unix.Stat_t
	if err := unix.Fstat(3, &inherited); err != nil {
		return fmt.Errorf("inspect inherited cleanup lock: %w", err)
	}
	if err := unix.Stat(executionLogCleanupLockPath(dir), &expected); err != nil ||
		inherited.Dev != expected.Dev || inherited.Ino != expected.Ino {
		return fmt.Errorf("invalid inherited cleanup lock")
	}
	file := os.NewFile(3, "log-cleanup.lock")
	if file == nil {
		return fmt.Errorf("missing inherited cleanup lock")
	}
	defer closeFile(file)
	return option.Clean(file, dir)
}
