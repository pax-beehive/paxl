package adaptor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestReadConfigurationReturnsPassiveEmptyInventory(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "must-not-read"))
	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent: model.AgentNamePi, CWD: cwd, Kind: model.AgentConfigurationKindAll,
		Home: home, Environment: map[string]string{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.SchemaVersion)
	require.False(t, result.RuntimeVerified)
	require.NotNil(t, result.Skills)
	require.NotNil(t, result.MCPServers)
	require.NotNil(t, result.Sources)
	require.NotNil(t, result.Conflicts)
	require.NotNil(t, result.Diagnostics)
	require.Equal(t, "partial", result.Coverage[model.AgentConfigurationKindSkills].Status)
	require.Equal(t, "partial", result.Coverage[model.AgentConfigurationKindMCP].Status)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(data), "must-not-read")
	_, err = os.Stat(filepath.Join(home, ".pi"))
	require.True(t, os.IsNotExist(err), "passive reading must not create native directories")
}

func validateConfigurationSnapshot(t *testing.T, snapshot *model.AgentConfiguration) {
	t.Helper()
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(model.AgentConfigurationSchema(), &schema))
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var value any
	require.NoError(t, json.Unmarshal(encoded, &value))
	require.NoError(
		t,
		resolved.Validate(value),
		"serialized inventory must satisfy its published JSON Schema",
	)
}

func TestReadConfigurationDefaultAndSensitiveOutputRespectTheSchema(t *testing.T) {
	for _, agent := range []model.AgentName{model.AgentNameCodex, model.AgentNameClaude, model.AgentNamePi,
		model.AgentNameKiro, model.AgentNameOpenCode, model.AgentNameKimi, model.AgentNameHermes,
		model.AgentNameOpenClaw, model.AgentNameDSH, model.AgentNameDSCode} {
		t.Run(string(agent), func(t *testing.T) {
			home, cwd := mcpFixtureDirectories(t)
			for _, sensitive := range []bool{false, true} {
				result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
					Agent:            agent,
					CWD:              cwd,
					Home:             home,
					Environment:      map[string]string{},
					IncludeSensitive: sensitive,
				})
				require.NoError(t, err)
				validateConfigurationSnapshot(t, result)
			}
		})
	}
}

func TestReadConfigurationRejectsInvalidContextsBeforeReading(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	for _, req := range []*ReadConfigurationRequest{
		nil,
		{Agent: model.AgentNameUnknown, CWD: cwd, Home: home},
		{Agent: model.AgentNameGemini, CWD: cwd, Home: home},
		{Agent: model.AgentNamePi, CWD: cwd, Environment: map[string]string{}},
		{Agent: model.AgentNamePi, CWD: filepath.Join(cwd, "missing"), Home: home},
		{Agent: model.AgentNamePi, CWD: cwd, Home: home, Kind: "unsupported"},
	} {
		_, err := ReadConfiguration(context.Background(), req)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadConfiguration(
		ctx,
		&ReadConfigurationRequest{Agent: model.AgentNamePi, CWD: cwd, Home: home},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestReadConfigurationRedactsOpenCodeCommandArrayAndKeepsReferences(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		`{"mcp":{"fixture":{"type":"local","command":["node","private-argument"],"environment":{"REFERENCE":"${TOKEN}","FALLBACK":"${TOKEN:-private-fallback}"}}}}`,
	)
	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent:       model.AgentNameOpenCode,
		CWD:         cwd,
		Home:        home,
		Environment: map[string]string{},
		Kind:        model.AgentConfigurationKindMCP,
	})
	require.NoError(t, err)
	require.Len(t, result.MCPServers, 1)
	require.Equal(t, "${TOKEN}", result.MCPServers[0].Environment["REFERENCE"])
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
	validateConfigurationSnapshot(t, result)
}

func TestReadConfigurationFileLimitsAndMalformedContentProduceSafeDiagnostics(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	path := filepath.Join(home, ".pi", "agent", "mcp.json")
	mcpFixtureFile(t, path, `{"mcpServers":{"secret":"must-not-appear-in-diagnostics"`)
	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent:       model.AgentNamePi,
		CWD:         cwd,
		Home:        home,
		Environment: map[string]string{},
		Kind:        model.AgentConfigurationKindMCP,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Diagnostics)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "must-not-appear-in-diagnostics")
	large := make([]byte, configurationFileLimit+1)
	require.NoError(t, os.WriteFile(path, large, 0o600))
	result, err = ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent:       model.AgentNamePi,
		CWD:         cwd,
		Home:        home,
		Environment: map[string]string{},
		Kind:        model.AgentConfigurationKindMCP,
	})
	require.NoError(t, err)
	require.Empty(t, result.MCPServers)
	require.Contains(t, mcpFixtureDiagnosticCodes(result), "configuration_read_error")
	validateConfigurationSnapshot(t, result)
}

func TestReadConfigurationReadsOpenClawNativeJSON5(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(t, filepath.Join(home, ".openclaw", "openclaw.json"), `{
  // Native OpenClaw supports JSON5 rather than strict JSON.
  mcp: {servers: {fixture: {transport: 'streamable-http', url: 'https://example.test/mcp', timeout: 0x20,},},},
}`)
	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent: model.AgentNameOpenClaw, CWD: cwd, Home: home, Environment: map[string]string{},
		Kind: model.AgentConfigurationKindMCP, IncludeSensitive: true,
	})
	require.NoError(t, err)
	require.Len(t, result.MCPServers, 1)
	require.Equal(t, "fixture", result.MCPServers[0].Name)
	require.Equal(t, "http", result.MCPServers[0].Transport)
	validateConfigurationSnapshot(t, result)
}

func TestReadConfigurationReportsNativeValuesThatCannotBeReturnedAsJSON(t *testing.T) {
	for _, test := range []struct {
		agent   model.AgentName
		path    string
		content string
	}{
		{model.AgentNameHermes, ".hermes/config.yaml", "mcp_servers: {broken: {timeout: .nan}}"},
		{model.AgentNameOpenClaw, ".openclaw/openclaw.json", "{mcp:{servers:{broken:{timeout:Infinity}}}}"},
	} {
		t.Run(string(test.agent), func(t *testing.T) {
			home, cwd := mcpFixtureDirectories(t)
			mcpFixtureFile(t, filepath.Join(home, filepath.FromSlash(test.path)), test.content)
			result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
				Agent:       test.agent,
				CWD:         cwd,
				Home:        home,
				Environment: map[string]string{},
				Kind:        model.AgentConfigurationKindMCP,
			})
			require.NoError(t, err)
			require.Empty(t, result.MCPServers)
			require.Contains(t, mcpFixtureDiagnosticCodes(result), "configuration_parse_error")
			validateConfigurationSnapshot(t, result)
		})
	}
}

func TestReadConfigurationRedactsSecretsFromEveryMCPRepresentation(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".pi", "agent", "mcp.json"),
		`{"mcpServers":{"issues":{"url":"https://secret-user:secret-password@example.test/mcp?token=secret-query&public=value#secret-fragment","command":"node","args":["--token","secret-argument"],"env":{"API_TOKEN":"secret-env"},"headers":{"Authorization":"secret-header"},"extension":{"credential":"secret-native","numeric_token":123456789}}}}`,
	)
	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent: model.AgentNamePi, CWD: cwd, Kind: model.AgentConfigurationKindMCP,
		Home: home, Environment: map[string]string{},
	})
	require.NoError(t, err)
	require.Len(t, result.MCPServers, 1)
	server := result.MCPServers[0]
	require.True(t, server.Redacted)
	require.Equal(t, "node", server.Command)
	require.Equal(t, []string{"[REDACTED]", "[REDACTED]"}, server.Args)
	require.Equal(t, "[REDACTED]", server.Environment["API_TOKEN"])
	require.Equal(t, "[REDACTED]", server.Headers["Authorization"])
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-")
	require.NotContains(t, string(data), "123456789")
	require.Contains(t, server.URL, "example.test/mcp")
}
