package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCronCreateSendsPromptAndScheduleToDaemon(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "paxd.sock")
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "unix", socket)
	require.NoError(t, err)
	var received map[string]any
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/cron-jobs", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cron_test","agent":"codex"}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	var stdout, stderr bytes.Buffer
	cmd := newCommandWithDiagnostics(
		strings.NewReader("Review this repository."),
		&stdout,
		&stderr,
		&stderr,
	)
	err = cmd.Run(
		context.Background(),
		[]string{
			"paxl",
			"cron",
			"--socket",
			socket,
			"create",
			"--name",
			"Daily review",
			"--connection",
			"conn_codex",
			"--session-mode",
			"reuse",
			"--schedule",
			"0 9 * * *",
			"--timezone",
			"America/Los_Angeles",
			"--cwd",
			t.TempDir(),
			"--prompt-file",
			"-",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "Review this repository.", received["prompt"])
	require.Equal(t, "conn_codex", received["connection_id"])
	require.Equal(t, "reuse", received["session_mode"])
	require.Equal(t, "0 9 * * *", received["schedule"])
	require.Equal(t, "America/Los_Angeles", received["timezone"])
	require.Equal(t, float64(3600), received["timeout_seconds"])
	require.Contains(t, stdout.String(), "cron_test")
}
