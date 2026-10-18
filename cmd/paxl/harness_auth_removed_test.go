package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessAuthRejectsRemovedRemoteOptions(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "status", "--harness", "codex", "--socket", "/tmp/unused.sock"},
		{"harness", "auth", "login", "--harness", "claude", "--code-stdin"},
		{"auth", "cancel", "--harness", "claude"},
	} {
		var stdout, stderr bytes.Buffer
		err := runWithInput(t.Context(), args, strings.NewReader(""), &stdout, &stderr)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "dial unix")
	}
}

func TestHarnessAuthHelpOnlyAdvertisesLocalOperations(t *testing.T) {
	for _, args := range [][]string{{"auth", "--help"}, {"harness", "auth", "login", "--help"}} {
		var output bytes.Buffer
		require.NoError(t, runWithInput(t.Context(), args, strings.NewReader(""), &output, &output))
		for _, removed := range []string{"--socket", "--code-stdin", "legacy", "cancel"} {
			require.NotContains(t, output.String(), removed)
		}
	}
}

func TestHarnessAuthRejectsInvalidLocalInputBeforeRunningNativeCommand(t *testing.T) {
	command := filepath.Join(t.TempDir(), "native")
	marker := filepath.Join(t.TempDir(), "called")
	require.NoError(
		t,
		os.WriteFile(command, []byte("#!/bin/sh\necho called > '"+marker+"'\n"), 0700),
	)
	t.Setenv("CODEX_PATH", command)
	t.Setenv("CLAUDE_CODE_EXECUTABLE", command)
	for _, args := range [][]string{
		{"auth", "status"},
		{"auth", "status", "--harness", "claude", "extra"},
		{"auth", "status", "--harness", "unknown"},
		{"auth", "status", "--harness", "codex", "--format", "xml"},
		{"auth", "login", "--harness", "codex", "--method", "unknown"},
		{"auth", "login", "--harness", "codex", "--method", "api-key"},
		{"auth", "login", "--harness", "codex", "--method", "console"},
		{"auth", "login", "--harness", "claude", "--secret-stdin"},
	} {
		var output bytes.Buffer
		require.Error(t, runWithInput(t.Context(), args, strings.NewReader(""), &output, &output))
	}
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}

func TestHarnessAuthInvalidMethodListsSupportedChoices(t *testing.T) {
	t.Setenv("CODEX_PATH", filepath.Join(t.TempDir(), "not-executed"))
	t.Setenv("CLAUDE_CODE_EXECUTABLE", filepath.Join(t.TempDir(), "not-executed"))
	for _, tc := range []struct{ harness, method, choices string }{
		{"claude", "device", "subscription, console (default: subscription)"},
		{"claude", "api-key", "subscription, console (default: subscription)"},
		{"claude", "typo", "subscription, console (default: subscription)"},
		{"codex", "console", "device, browser, api-key, access-token (default: device)"},
		{"kimi", "api-key", "device (default: device)"},
	} {
		var output bytes.Buffer
		err := runWithInput(
			t.Context(),
			[]string{"harness", "auth", "login", "--harness", tc.harness, "--method", tc.method},
			strings.NewReader(""),
			&output,
			&output,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "supported methods for "+tc.harness+": "+tc.choices)
	}
}

func TestPiInstallCLIOnlyPlansOfficialPackages(t *testing.T) {
	for _, args := range [][]string{
		{"harness", "install", "pi", "--dry-run", "--format", "json"},
		{"harness", "install", "pi", "--component", "acp", "--dry-run", "--format", "json"},
	} {
		var output bytes.Buffer
		require.NoError(t, runWithInput(t.Context(), args, strings.NewReader(""), &output, &output))
		require.Contains(t, output.String(), `"state":"planned"`)
		require.Contains(t, output.String(), `"version":"latest"`)
	}
}

func TestHarnessInstallGuidesUnsupportedNamesAndHelp(t *testing.T) {
	var output bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{"harness", "install", "pi-agent"},
		strings.NewReader(""),
		&output,
		&output,
	)
	require.ErrorContains(t, err, `unsupported install harness "pi-agent"`)
	require.ErrorContains(t, err, "supported harnesses: claude, codex, pi")
	require.ErrorContains(t, err, "paxl harness install pi --component acp")
	output.Reset()
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"harness", "install", "-h"},
			strings.NewReader(""),
			&output,
			&output,
		),
	)
	require.Contains(t, output.String(), "SUPPORTED HARNESSES (official route):")
	require.Contains(t, output.String(), "Pi managed installer")
	require.Contains(t, output.String(), "paxl harness install pi --component acp")
}
