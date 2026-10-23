package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const executionLogHourLayout = "20060102T15Z"

// Only the CLI entry point enables subprocesses; embedded command runs stay local.
var scheduleExecutionLogCleanup func(string)

type executionLogCleanupOption struct {
	BatchSize int
	Pause     time.Duration
	Sleep     func(time.Duration)
}

func (l *executionLogger) rotate(now time.Time) error {
	hour := now.UTC().Format(executionLogHourLayout)
	if l.hour == hour {
		return nil
	}
	path := filepath.Join(l.dir, "paxl-"+hour+".log")
	// O_APPEND keeps each encoded JSON record together across concurrent CLI processes.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("open execution log: %w", err)
	}
	if l.file != nil {
		_ = l.file.Close()
	}
	l.file, l.encoder, l.hour = file, json.NewEncoder(file), hour
	if scheduleExecutionLogCleanup != nil {
		scheduleExecutionLogCleanup(l.dir)
	}
	return nil
}

func pruneExecutionLogs(
	dir string,
	cutoff time.Time,
	opts ...func(*executionLogCleanupOption),
) error {
	option := &executionLogCleanupOption{
		BatchSize: 256,
		Pause:     50 * time.Millisecond,
		Sleep:     time.Sleep,
	}
	for _, opt := range opts {
		opt(option)
	}
	if option.BatchSize < 1 {
		return fmt.Errorf("cleanup batch size must be positive")
	}
	directory, err := os.Open(dir) // #nosec G304 -- Fixed local log directory.
	if err != nil {
		return fmt.Errorf("open execution log directory: %w", err)
	}
	defer closeFile(directory)
	var cleanupErr error
	// Keep a single streaming directory handle rather than sorting millions of names.
	for {
		entries, readErr := directory.ReadDir(option.BatchSize)
		for _, entry := range entries {
			if err := removeExpiredExecutionLog(dir, entry, cutoff); err != nil {
				cleanupErr = err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return cleanupErr
		}
		if readErr != nil {
			return fmt.Errorf("read execution log directory: %w", readErr)
		}
		if len(entries) == option.BatchSize && option.Pause > 0 {
			option.Sleep(option.Pause)
		}
	}
}

func removeExpiredExecutionLog(dir string, entry os.DirEntry, cutoff time.Time) error {
	if !entry.Type().IsRegular() || !isExecutionLogName(entry.Name()) {
		return nil
	}
	info, err := entry.Info()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect execution log: %w", err)
	}
	if !info.ModTime().Before(cutoff) {
		return nil
	}
	if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove expired execution log: %w", err)
	}
	return nil
}

func isExecutionLogName(name string) bool {
	raw := strings.TrimSuffix(strings.TrimPrefix(name, "paxl-"), ".log")
	if name != "paxl-"+raw+".log" {
		return false
	}
	if _, err := time.Parse(executionLogHourLayout, raw); err == nil {
		return true
	}
	// Also retire the former per-invocation timestamp/PID files.
	timestamp, pid, ok := strings.Cut(raw, "-")
	if !ok || pid == "" {
		return false
	}
	for _, char := range pid {
		if char < '0' || char > '9' {
			return false
		}
	}
	_, err := time.Parse("20060102T150405.000000000Z", timestamp)
	return err == nil
}
