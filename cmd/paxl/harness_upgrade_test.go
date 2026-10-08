package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessCommandsKeepMachineOutputSeparateFromVerboseProgress(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "lib", "node_modules", "@agentclientprotocol", "codex-acp")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(
				`{"name":"@agentclientprotocol/codex-acp","version":"1.2.3","bin":{"codex-acp":"bin/codex.js"}}`,
			),
			0644,
		),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "bin", "codex.js"),
			[]byte("#!/bin/sh\necho 'codex-cli 1.2.3'\n"),
			0755,
		),
	)
	launcher := filepath.Join(root, "bin", "codex-acp")
	require.NoError(t, os.Symlink(filepath.Join(pkg, "bin", "codex.js"), launcher))
	for _, action := range []string{"inspect", "upgrade"} {
		args := []string{
			"daemon",
			"harness",
			action,
			"codex",
			"--path",
			launcher,
			"--component",
			"acp",
			"--verbose",
		}
		if action == "upgrade" {
			args = append(args, "--version", "1.2.4", "--dry-run")
		}
		var stdout, stderr bytes.Buffer
		require.NoError(t, run(t.Context(), args, &stdout, &stderr))
		var result map[string]any
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
		require.Contains(t, stderr.String(), "Inspected @agentclientprotocol/codex-acp")
		if action == "upgrade" {
			require.Equal(t, "planned", result["phase"])
		}
	}
	require.NoDirExists(t, filepath.Join(root, "bin", ".paxl-harness-versions"))
}

func TestHarnessCommandsRejectInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"inspect"}, {"inspect", "codex", "--component", "unknown"},
		{"inspect", "codex", "--format", "text"}, {"upgrade", "codex", "--version", "invalid"},
		{"rollback", "codex"},
	} {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), append([]string{"daemon", "harness"}, args...), &stdout, &stderr)
		require.Error(t, err)
	}
}

func TestHarnessUpgradeDefaultsToNativeCLIAndLatest(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "lib", "node_modules", "@openai", "codex")
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(pkg, 0755))
	require.NoError(t, os.MkdirAll(bin, 0755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(`{"name":"@openai/codex","version":"1.2.3","bin":{"codex":"entry.js"}}`),
			0600,
		),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "entry.js"),
			[]byte("#!/bin/sh\necho 'codex-cli 1.2.3'\n"),
			0755,
		),
	)
	require.NoError(t, os.Symlink(filepath.Join(pkg, "entry.js"), filepath.Join(bin, "codex")))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(bin, "npm"),
			[]byte(
				"#!/bin/sh\n[ \"$1\" = view ] || exit 20\n[ \"$2\" = '@openai/codex@latest' ] || exit 21\nprintf '\"1.2.4\"\\n'\n",
			),
			0755,
		),
	)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	require.NoError(
		t,
		run(
			t.Context(),
			[]string{"daemon", "harness", "upgrade", "codex", "--dry-run"},
			&stdout,
			&stderr,
		),
	)
	var result struct {
		TargetVersion string `json:"target_version"`
		Installation  struct {
			Component string `json:"component"`
			Package   string `json:"package"`
		} `json:"installation"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	require.Equal(t, "1.2.4", result.TargetVersion)
	require.Equal(t, "cli", result.Installation.Component)
	require.Equal(t, "@openai/codex", result.Installation.Package)
	require.NoDirExists(t, filepath.Join(bin, ".paxl-harness-versions"))
}
