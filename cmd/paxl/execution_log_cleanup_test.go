package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestExecutionLogCleanupStreamsAndPacesBatches(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	for i := 0; i < 513; i++ {
		name := fmt.Sprintf("paxl-20260101T000000.000000000Z-%d.log", i)
		createExecutionCleanupLog(t, dir, name, cutoff.Add(-time.Hour))
	}
	for _, name := range []string{"paxl-20260102T00Z.log", "paxl-20260103T00Z.log", "keep.txt", "paxl-invalid.log"} {
		createExecutionCleanupLog(t, dir, name, cutoff)
	}
	createExecutionCleanupLog(t, dir, "paxl-20260104T00Z.log", time.Now())
	require.NoError(t, os.Mkdir(filepath.Join(dir, "paxl-20260105T00Z.log"), 0700))
	require.NoError(
		t,
		os.Symlink(filepath.Join(dir, "keep.txt"), filepath.Join(dir, "paxl-20260106T00Z.log")),
	)
	pauses := 0
	require.NoError(t, pruneExecutionLogs(dir, cutoff, func(o *executionLogCleanupOption) {
		o.Sleep = func(delay time.Duration) { require.Equal(t, 50*time.Millisecond, delay); pauses++ }
	}))
	require.Equal(t, 2, pauses, "The worker must yield after each full batch of 256 entries.")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 7)
	require.NoError(
		t,
		pruneExecutionLogs(dir, cutoff, func(o *executionLogCleanupOption) { o.Pause = 0 }),
	)
}

func TestExecutionLogCleanupReportsInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	require.Error(
		t,
		pruneExecutionLogs(dir, time.Now(), func(o *executionLogCleanupOption) { o.BatchSize = 0 }),
	)
	require.Error(t, pruneExecutionLogs(filepath.Join(dir, "missing"), time.Now()))
	path := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	require.Error(t, pruneExecutionLogs(path, time.Now()))
}

func TestExecutionLogCleanupRecordsCompletionOrRetry(t *testing.T) {
	dir := t.TempDir()
	file, err := os.CreateTemp(t.TempDir(), "cleanup-lock")
	require.NoError(t, err)
	defer closeFile(file)
	require.True(t, cleanupIsDue(file, time.Now()))
	require.NoError(t, cleanExecutionLogsWithLock(file, dir))
	require.False(t, cleanupIsDue(file, time.Now().Add(59*time.Minute)))
	require.True(t, cleanupIsDue(file, time.Now().Add(61*time.Minute)))
	require.Error(t, cleanExecutionLogsWithLock(file, filepath.Join(dir, "missing")))
	require.False(t, cleanupIsDue(file, time.Now().Add(30*time.Second)))
	require.True(t, cleanupIsDue(file, time.Now().Add(2*time.Minute)))
	closeFile(file)
	require.Error(t, deferExecutionLogCleanup(file, time.Now()))
	require.Error(t, cleanExecutionLogsWithLock(file, dir))
}

func TestExecutionLogRotationSchedulesCleanupWithoutDeletingInline(t *testing.T) {
	previous := scheduleExecutionLogCleanup
	t.Cleanup(func() { scheduleExecutionLogCleanup = previous })
	var dirs []string
	scheduleExecutionLogCleanup = func(dir string) { dirs = append(dirs, dir) }
	t.Setenv("HOME", t.TempDir())
	logger := openExecutionLogger(nil)
	require.NotNil(t, logger)
	defer closeExecutionLogger(logger)
	require.Equal(t, []string{logger.dir}, dirs)
	logger.write("same_hour", nil)
	require.Len(t, dirs, 1)
	require.NoError(t, logger.rotate(logger.startedAt.Add(time.Hour)))
	require.Len(t, dirs, 2)
}

func createExecutionCleanupLog(t *testing.T, dir, name string, modified time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("Historical log.\n"), 0600))
	require.NoError(t, os.Chtimes(path, modified, modified))
}

func TestExecutionLogCleanupToleratesFilesRemovedByAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	name := "paxl-20260101T00Z.log"
	createExecutionCleanupLog(t, dir, name, time.Now().Add(-73*time.Hour))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, name)))
	require.NoError(t, removeExpiredExecutionLog(dir, entries[0], time.Now()))
}

func TestExecutionLogsRemainOptionalWhenHomeOrLogFileIsUnavailable(t *testing.T) {
	t.Run("missing_home", func(t *testing.T) {
		t.Setenv("HOME", "")
		require.Nil(t, openExecutionLogger(nil))
	})
	t.Run("unwritable_log_file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		dir, err := executionLogDir()
		require.NoError(t, err)
		// A directory at the expected file path must not prevent the command running.
		now := time.Now()
		for _, now := range []time.Time{now, now.Add(time.Hour)} {
			require.NoError(
				t,
				os.MkdirAll(
					filepath.Join(dir, "paxl-"+now.UTC().Format(executionLogHourLayout)+".log"),
					0700,
				),
			)
		}
		require.Nil(t, openExecutionLogger(nil))
		var stdout, stderr bytes.Buffer
		require.NoError(t, run(t.Context(), []string{"version"}, &stdout, &stderr))
		require.Contains(t, stdout.String(), "paxl")
	})
}

func TestExecutionLogsFinishOnlyOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logger := openExecutionLogger(nil)
	require.NotNil(t, logger)
	defer closeExecutionLogger(logger)
	finishExecutionLog(logger, nil)
	finishExecutionLog(logger, errors.New("duplicate finish"))
	raw, err := os.ReadFile(logger.file.Name())
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(raw, []byte(`"event":"command_finish"`)))
	require.NotContains(t, string(raw), "duplicate finish")
}

func TestExecutionLogCallerAttributionRespectsInvocationBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		args  []string
		agent string
	}{
		{"inline_flag", []string{"--caller-agent=codex"}, "codex"},
		{"missing_flag_value", []string{"--caller-agent"}, ""},
		{"nested_command_flag", []string{"--", "--caller-agent=codex"}, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PAXL_CALLER_AGENT", "")
			t.Setenv("PAXL_AGENT", "")
			logger := openExecutionLogger(scenario.args)
			require.NotNil(t, logger)
			defer closeExecutionLogger(logger)
			raw, err := os.ReadFile(logger.file.Name())
			require.NoError(t, err)
			var record map[string]any
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(raw), &record))
			if scenario.agent == "" {
				require.NotContains(t, record, "callerAgent")
			} else {
				require.Equal(t, scenario.agent, record["callerAgent"])
			}
		})
	}
}

func TestExecutionLogsFailureDoesNotSuppressVerboseStderr(t *testing.T) {
	var stderr bytes.Buffer
	command := &cli.Command{
		Flags: []cli.Flag{&cli.BoolFlag{Name: "verbose"}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			require.Same(t, &stderr, verboseWriter(cmd, &stderr, nil))
			return nil
		},
	}
	require.NoError(t, command.Run(t.Context(), []string{"paxl", "--verbose"}))
}
