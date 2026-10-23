package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const executionLogHourLayout = "20060102T15Z"

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
	pruneExecutionLogs(l.dir, now.Add(-72*time.Hour))
	return nil
}

func pruneExecutionLogs(dir string, cutoff time.Time) {
	directory, err := os.Open(dir) // #nosec G304
	if err != nil {
		return
	}
	defer closeFile(directory)
	// Read in batches because older installations can contain millions of small files.
	for {
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !isExecutionLogName(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
		if readErr != nil {
			return
		}
	}
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
