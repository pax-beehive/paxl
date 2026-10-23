package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecutionLogsAppendWithinAnHour(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for i := 0; i < 2; i++ {
		logger := openExecutionLogger([]string{"version"})
		require.NotNil(t, logger)
		closeExecutionLogger(logger)
	}
	dir, err := executionLogDir()
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(raw), "command_start")
}

func TestExecutionLogsRotateAndPruneOldFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logger := openExecutionLogger([]string{"version"})
	require.NotNil(t, logger)
	defer closeExecutionLogger(logger)
	now := logger.startedAt
	old := now.Add(-73 * time.Hour)
	for _, name := range []string{"paxl-20260101T00Z.log", "paxl-20260101T000000.000000000Z-123.log", "keep.txt", "paxl-invalid.log"} {
		path := filepath.Join(logger.dir, name)
		require.NoError(t, os.WriteFile(path, []byte("old\n"), 0600))
		require.NoError(t, os.Chtimes(path, old, old))
	}
	recent := filepath.Join(logger.dir, "paxl-20260102T00Z.log")
	require.NoError(t, os.WriteFile(recent, []byte("recent\n"), 0600))
	logger.now = func() time.Time { return now.Add(time.Hour) }
	logger.write("next_hour", nil)
	require.NoError(
		t,
		pruneExecutionLogs(
			logger.dir,
			now.Add(time.Hour-72*time.Hour),
			func(o *executionLogCleanupOption) { o.Pause = 0 },
		),
	)
	require.NoFileExists(t, filepath.Join(logger.dir, "paxl-20260101T00Z.log"))
	require.NoFileExists(t, filepath.Join(logger.dir, "paxl-20260101T000000.000000000Z-123.log"))
	require.FileExists(t, filepath.Join(logger.dir, "keep.txt"))
	require.FileExists(t, filepath.Join(logger.dir, "paxl-invalid.log"))
	require.FileExists(t, recent)
	raw, err := os.ReadFile(logger.file.Name())
	require.NoError(t, err)
	require.Contains(t, string(raw), "next_hour")
	require.NotContains(t, string(raw), "command_start")
}

func TestExecutionLogNamesOnlyMatchOwnedFiles(t *testing.T) {
	for _, name := range []string{"paxl-20260101T00Z.log", "paxl-20260101T000000.000000000Z-123.log"} {
		require.True(t, isExecutionLogName(name), name)
	}
	for _, name := range []string{"other.log", "paxl-invalid.log", "paxl-20260101T000000.000000000Z-.log", "paxl-20260101T000000.000000000Z-abc.log", "paxl-bad-123.log"} {
		require.False(t, isExecutionLogName(name), name)
	}
}

func TestExecutionLogRotationFailurePreservesCurrentFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logger := openExecutionLogger(nil)
	require.NotNil(t, logger)
	defer closeExecutionLogger(logger)
	previous := logger.file
	logger.dir = filepath.Join(logger.dir, "missing")
	require.Error(t, logger.rotate(logger.startedAt.Add(time.Hour)))
	require.Same(t, previous, logger.file)
	require.Error(t, pruneExecutionLogs(logger.dir, time.Now()))
}

func TestExecutionLogsConcurrentWritersProduceCompleteRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger := openExecutionLogger(nil)
			if logger == nil {
				t.Error("Expected execution logger.")
				return
			}
			defer closeExecutionLogger(logger)
			for j := 0; j < 20; j++ {
				logger.write("parallel", nil)
			}
		}()
	}
	wg.Wait()
	dir, err := executionLogDir()
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	records := 0
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			var record map[string]any
			require.NoError(t, json.Unmarshal(line, &record))
			require.NotEmpty(t, record["executionId"])
			records++
		}
	}
	require.Equal(t, 210, records)
}

func TestExecutionLogsIgnoreWritesAfterCloseOrFailedRotation(t *testing.T) {
	var absent *executionLogger
	absent.write("ignored", nil)
	t.Setenv("HOME", t.TempDir())
	logger := openExecutionLogger(nil)
	require.NotNil(t, logger)
	logger.dir = filepath.Join(logger.dir, "missing")
	logger.now = func() time.Time { return logger.startedAt.Add(time.Hour) }
	logger.write("ignored", nil)
	closeExecutionLogger(logger)
	logger.write("ignored", nil)
}

func TestExecutionLogRotationDoesNotWaitForHistoricalCleanup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logger := openExecutionLogger(nil)
	require.NotNil(t, logger)
	defer closeExecutionLogger(logger)
	path := filepath.Join(logger.dir, "paxl-20260101T00Z.log")
	require.NoError(t, os.WriteFile(path, []byte("Old log.\n"), 0600))
	expired := time.Now().Add(-73 * time.Hour)
	require.NoError(t, os.Chtimes(path, expired, expired))
	require.NoError(t, logger.rotate(logger.startedAt.Add(time.Hour)))
	require.FileExists(t, path, "Rotation must not scan or delete historical files synchronously.")
}
