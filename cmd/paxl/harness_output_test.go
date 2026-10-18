package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"

	"github.com/stretchr/testify/require"
)

func TestHarnessInstallDefaultsToReadableText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"harness", "install", "claude", "--dry-run"},
			strings.NewReader(""),
			&stdout,
			&stderr,
		),
	)
	require.Contains(t, stdout.String(), "Install plan: claude (cli)")
	require.Contains(t, stdout.String(), "Installer: https://claude.ai/install.sh")
	require.NotContains(t, stdout.String(), `"state"`)
}

func TestHarnessResultTextAndJSONKeepDistinctSemantics(t *testing.T) {
	installation := &facade.HarnessInstallation{
		Harness:      "claude",
		Component:    facade.HarnessComponentACP,
		Version:      "1.2.3",
		Path:         "/bin/adapter",
		ResolvedPath: "/packages/adapter.js",
		Package:      "@agentclientprotocol/claude-agent-acp",
		Source:       "npm",
	}
	cases := []struct {
		action   string
		result   any
		expected string
	}{
		{
			"install",
			&facade.HarnessInstallResult{
				Harness:   "claude",
				Component: facade.HarnessComponentCLI,
				Via:       facade.HarnessInstallViaOfficial,
				Source:    "script",
				State:     "completed",
				Version:   "latest",
			},
			"Official installer completed: claude (cli)",
		},
		{
			"install",
			&facade.HarnessInstallResult{
				Harness:   "claude",
				Component: facade.HarnessComponentACP,
				Via:       facade.HarnessInstallViaOfficial,
				Source:    "npm",
				State:     "installed",
				Version:   "latest",
				Package:   installation.Package,
			},
			"Package installation completed: claude (acp)",
		},
		{"inspect", installation, "Version: 1.2.3"},
		{"rollback", installation, "Rollback completed: claude (acp)"},
		{
			"upgrade",
			&facade.HarnessUpgradeResponse{
				Installation:  installation,
				Phase:         "planned",
				TargetVersion: "1.2.4",
			},
			"Target version: 1.2.4",
		},
		{
			"upgrade",
			&facade.HarnessUpgradeResponse{
				Installation:  installation,
				Phase:         "installed",
				TargetVersion: "1.2.3",
				RollbackID:    "saved-1",
			},
			"Rollback ID: saved-1",
		},
	}
	for _, tc := range cases {
		var textOutput, jsonOutput bytes.Buffer
		require.NoError(t, renderHarnessOutput(&textOutput, tc.result, "text", tc.action))
		require.Contains(t, textOutput.String(), tc.expected)
		require.NoError(t, renderHarnessOutput(&jsonOutput, tc.result, "json", tc.action))
		expected, err := json.Marshal(tc.result)
		require.NoError(t, err)
		require.JSONEq(t, string(expected), jsonOutput.String())
	}
	var output bytes.Buffer
	require.NoError(t, renderHarnessOutput(&output, cases[0].result, "text", "install"))
	require.NotContains(t, output.String(), "Version: latest")
	require.NotContains(t, output.String(), "Path:")
	require.Contains(t, output.String(), "not been independently verified")
}

func TestHarnessInstallJSONIsExplicitAndInvalidFormatsFailBeforeInstallation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"harness", "install", "pi", "--dry-run", "--format", "json"},
			strings.NewReader(""),
			&stdout,
			&stderr,
		),
	)
	require.True(t, json.Valid(stdout.Bytes()))
	require.Contains(t, stdout.String(), `"state":"planned"`)
	for _, action := range []string{"install", "inspect", "upgrade", "rollback"} {
		args := []string{"harness", action, "claude", "--format", "xml"}
		if action == "rollback" {
			args = append(args, "--rollback-id", "unused")
		}
		stdout.Reset()
		err := runWithInput(t.Context(), args, strings.NewReader(""), &stdout, &stderr)
		require.ErrorContains(t, err, "supported: text, json")
		require.Empty(t, stdout.String())
	}
}

func TestHarnessInstallKeepsNativeProgressOutOfJSON(t *testing.T) {
	bin := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(bin, "npm"),
			[]byte("#!/bin/sh\necho 'Fixture installation progress'\n"),
			0700,
		),
	)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, format := range []string{"text", "json"} {
		var stdout, stderr bytes.Buffer
		require.NoError(
			t,
			runWithInput(
				t.Context(),
				[]string{"harness", "install", "claude", "--component", "acp", "--format", format},
				strings.NewReader(""),
				&stdout,
				&stderr,
			),
		)
		require.Contains(t, stderr.String(), "Fixture installation progress")
		require.NotContains(t, stdout.String(), "Fixture installation progress")
		if format == "json" {
			require.True(t, json.Valid(stdout.Bytes()))
		} else {
			require.Contains(t, stdout.String(), "Package installation completed: claude (acp)")
		}
	}
}

func TestAntigravityInstallGuidanceNamesActualCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"harness", "install", "antigravity", "--dry-run"},
			strings.NewReader(""),
			&stdout,
			&stderr,
		),
	)
	require.Contains(t, stdout.String(), "Installer: https://antigravity.google/cli/install.sh")
	stdout.Reset()
	require.NoError(
		t,
		renderHarnessOutput(
			&stdout,
			&facade.HarnessInstallResult{
				Harness:   "antigravity",
				Component: facade.HarnessComponentCLI,
				Via:       facade.HarnessInstallViaOfficial,
				Source:    "script",
				State:     "completed",
				Version:   "latest",
			},
			"text",
			"install",
		),
	)
	require.Contains(t, stdout.String(), "Official installer completed: antigravity (cli)")
	require.Contains(t, stdout.String(), "Check: agy --version")
	stdout.Reset()
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"harness", "install", "-h"},
			strings.NewReader(""),
			&stdout,
			&stderr,
		),
	)
	require.Contains(t, stdout.String(), "Antigravity CLI official installer (command: agy)")
}
