package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestHarnessConfigurationSchemaOutputsOneJSONDocument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := newCommand(
		&stdout,
		&stderr,
	).Run(context.Background(), []string{"paxl", "harness", "config", "schema"})
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &schema))
	require.Equal(t, "urn:paxl:agent-configuration:1", schema["$id"])
	require.Empty(t, stderr.String())
}

func TestHarnessConfigurationReadProducesRedactedNativeSnapshot(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".pi", "agent", "mcp.json"), []byte(`{
  "mcpServers": {"fixture": {"command":"fixture-server", "args":["--token","literal-secret"],"env":{"ACCESS_TOKEN":"literal-secret"}}}
}`), 0o600))
	var stdout, stderr bytes.Buffer
	err := newCommand(&stdout, &stderr).Run(context.Background(), []string{
		"paxl",
		"harness",
		"config",
		"read",
		"pi",
		"--cwd",
		project,
		"--kind",
		"mcp",
		"--format",
		"json",
		"--verbose",
	})
	require.NoError(t, err)
	var snapshot model.AgentConfiguration
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &snapshot))
	require.Equal(t, 1, snapshot.SchemaVersion)
	require.Equal(t, model.AgentNamePi, snapshot.Agent)
	require.Equal(t, project, snapshot.CWD)
	require.False(t, snapshot.RuntimeVerified)
	require.Empty(t, snapshot.Skills)
	require.Len(t, snapshot.MCPServers, 1)
	require.Equal(t, "fixture", snapshot.MCPServers[0].Name)
	require.Equal(t, "fixture-server", snapshot.MCPServers[0].Command)
	require.NotContains(t, stdout.String(), "literal-secret")
	require.NotEmpty(t, stderr.String())
}

func TestHarnessConfigurationRejectsInvalidArgumentsBeforeReading(t *testing.T) {
	for _, args := range [][]string{
		{"read"},
		{"read", "codex", "claude"},
		{"read", "not-an-agent"},
		{"read", "codex", "--kind", "prompts"},
		{"read", "codex", "--format", "xml"},
		{"schema", "unexpected"},
	} {
		t.Run(args[0]+"_"+args[len(args)-1], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCommand(
				&stdout,
				&stderr,
			).Run(context.Background(), append([]string{"paxl", "harness", "config"}, args...))
			require.Error(t, err)
			require.Empty(t, stdout.String())
		})
	}
}
