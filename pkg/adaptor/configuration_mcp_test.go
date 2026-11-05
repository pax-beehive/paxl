package adaptor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestReadConfigurationMCPReadsKnownNativeFormats(t *testing.T) {
	tests := []struct {
		name      string
		agent     model.AgentName
		path      string
		content   string
		transport string
		command   string
		args      []string
	}{
		{
			"codex",
			model.AgentNameCodex,
			".codex/config.toml",
			"[mcp_servers.fixture]\ncommand = 'test-mcp'\nargs = ['--stdio']\nenabled = false\n",
			"stdio",
			"test-mcp",
			[]string{"--stdio"},
		},
		{
			"claude",
			model.AgentNameClaude,
			".claude.json",
			`{"mcpServers":{"fixture":{"type":"ws","url":"wss://example.invalid/mcp","disabled":true}}}`,
			"ws",
			"",
			[]string{},
		},
		{
			"pi",
			model.AgentNamePi,
			".pi/agent/mcp.json",
			`{"mcpServers":{"fixture":{"url":"https://example.invalid/mcp","enabled":false}}}`,
			"http",
			"",
			[]string{},
		},
		{
			"kiro",
			model.AgentNameKiro,
			".kiro/settings/mcp.json",
			`{"mcpServers":{"fixture":{"command":"test-mcp","args":[],"disabled":true}}}`,
			"stdio",
			"test-mcp",
			[]string{},
		},
		{
			"opencode",
			model.AgentNameOpenCode,
			".config/opencode/opencode.jsonc",
			"{ // preserved source format\n\"mcp\":{\"fixture\":{\"type\":\"local\",\"command\":[\"test-mcp\",\"--stdio\"],\"enabled\":false,}},}",
			"stdio",
			"test-mcp",
			[]string{"--stdio"},
		},
		{
			"kimi",
			model.AgentNameKimi,
			".kimi-code/mcp.json",
			`{"mcpServers":{"fixture":{"url":"https://example.invalid/sse","transport":"sse","enabled":false}}}`,
			"sse",
			"",
			[]string{},
		},
		{
			"hermes",
			model.AgentNameHermes,
			".hermes/config.yaml",
			"mcp_servers:\n  fixture:\n    command: test-mcp\n    enabled: false\n",
			"stdio",
			"test-mcp",
			[]string{},
		},
		{
			"openclaw",
			model.AgentNameOpenClaw,
			".openclaw/openclaw.json",
			`{"mcp":{"servers":{"fixture":{"url":"https://example.invalid/sse","enabled":false}}}}`,
			"sse",
			"",
			[]string{},
		},
		{
			"dsh",
			model.AgentNameDSH,
			".dsh/cordis.patch.yml",
			"- insert:\n    - id: mcp-fixture\n      name: '@deepseek-ai/dsh-mcp-client'\n      disabled: true\n      config:\n        serverName: fixture\n        transport: stdio\n        command: test-mcp\n",
			"stdio",
			"test-mcp",
			[]string{},
		},
		{
			"dscode",
			model.AgentNameDSCode,
			".local/share/dscode-state/config/mcp.local.yml",
			"- insert:\n    - id: mcp-fixture\n      name: '@deepseek-ai/dsh-mcp-client'\n      disabled: true\n      config:\n        serverName: fixture\n        transport: streamable-http\n        url: https://example.invalid/mcp\n",
			"http",
			"",
			[]string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, cwd := mcpFixtureDirectories(t)
			path := filepath.Join(home, filepath.FromSlash(test.path))
			mcpFixtureFile(t, path, test.content)
			result := mcpFixtureRead(t, test.agent, home, cwd, map[string]string{})
			require.Len(t, result.MCPServers, 1)
			server := result.MCPServers[0]
			require.Equal(t, "fixture", server.Name)
			require.Equal(t, test.transport, server.Transport)
			require.Equal(t, test.command, server.Command)
			require.Equal(t, test.args, server.Args)
			require.NotNil(t, server.Enabled)
			require.False(t, *server.Enabled)
			require.Equal(t, path, server.Source.Path)
			require.NotEmpty(t, server.Source.SHA256)
			require.Equal(t, "unknown", server.Origin.Kind)
			require.False(t, result.RuntimeVerified)
			require.Equal(t, "partial", result.Coverage[model.AgentConfigurationKindMCP].Status)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, test.content, string(after))
		})
	}
}

func TestReadConfigurationMCPKeepsCodexCandidatesAndHeaderReferences(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".codex", "config.toml"),
		"[mcp_servers.shared]\nurl='https://example.invalid/mcp'\nenv_vars=['PASSTHROUGH']\nhttp_headers={X-Client='fixture'}\nenv_http_headers={Authorization='MCP_TOKEN'}\n[profiles.review.mcp_servers.shared]\ncommand='review-mcp'\n",
	)
	mcpFixtureFile(
		t,
		filepath.Join(cwd, ".codex", "config.toml"),
		"[mcp_servers.shared]\ncommand='project-mcp'\n",
	)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".codex", "review.config.toml"),
		"[mcp_servers.review]\ncommand='native-profile-mcp'\n",
	)
	result := mcpFixtureRead(t, model.AgentNameCodex, home, cwd, map[string]string{})
	require.Len(t, result.MCPServers, 4)
	identities := map[string]bool{}
	for _, server := range result.MCPServers {
		require.False(t, identities[server.ID])
		identities[server.ID] = true
		if server.URL != "" {
			require.Equal(t, "${PASSTHROUGH}", server.Environment["PASSTHROUGH"])
			require.Equal(t, "${MCP_TOKEN}", server.Headers["Authorization"])
			require.Equal(t, "fixture", server.Headers["X-Client"])
		}
	}
}

func TestReadConfigurationMCPReadsCordisLiteralsWithoutEvaluatingCode(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	path := filepath.Join(home, ".dsh", "profiles", "review", "cordis.patch.yml")
	mcpFixtureFile(t, path, `- insert:
    - id: mcp-static
      name: '@deepseek-ai/dsh-mcp-client'
      disabled: !!js process.exit(88)
      config:
        serverName: static
        transport: stdio
        command: nonexistent-mcp
        args: [!!js process.exit(89)]
        enabled: true
    - id: managed-mcp
      name: '@deepseek-ai/dsh-mcp-manager'
      config:
        servers:
          remote:
            transport: streamable-http
            url: https://example.invalid/mcp
            enabled: false
    - id: named-later
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: !!js process.env.DYNAMIC_NAME
        transport: stdio
        command: not-launched
- id: mcp-static
  disabled: true
`)
	result := mcpFixtureRead(t, model.AgentNameDSH, home, cwd, map[string]string{})
	require.Len(t, result.MCPServers, 2)
	require.ElementsMatch(
		t,
		[]string{"static", "remote"},
		[]string{result.MCPServers[0].Name, result.MCPServers[1].Name},
	)
	for _, server := range result.MCPServers {
		require.Equal(t, model.ConfigurationScopeProfile, server.Source.Scope)
		require.Contains(t, server.Source.Owner, "profile:review")
		require.NotEmpty(t, server.Native["entry"])
		if server.Name == "static" {
			require.Nil(t, server.Enabled)
			require.Empty(t, server.Args)
		}
	}
	require.Contains(t, mcpFixtureDiagnosticCodes(result), "unresolved_cordis_expression")
	require.Contains(t, mcpFixtureDiagnosticCodes(result), "unresolved_cordis_patch")
	require.Contains(t, mcpFixtureDiagnosticCodes(result), "unnamed_mcp_server")
}

func TestReadConfigurationMCPDoesNotFallbackWhenDSCodeHomeIsExplicit(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	custom := filepath.Join(home, "pax-owned-dscode")
	mcpFixtureFile(
		t,
		filepath.Join(custom, "config", "mcp.local.yml"),
		"- insert:\n    - id: only\n      name: '@deepseek-ai/dsh-mcp-client'\n      config: {serverName: selected, transport: stdio, command: selected-mcp}\n",
	)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".local", "share", "dscode-state", "config", "mcp.local.yml"),
		"- insert:\n    - id: unrelated\n      name: '@deepseek-ai/dsh-mcp-client'\n      config: {serverName: unrelated, transport: stdio, command: unrelated-mcp}\n",
	)
	result := mcpFixtureRead(
		t,
		model.AgentNameDSCode,
		home,
		cwd,
		map[string]string{"DSCODE_HOME": custom},
	)
	require.Len(t, result.MCPServers, 1)
	require.Equal(t, "selected", result.MCPServers[0].Name)
}

func TestReadConfigurationMCPReportsMalformedFilesAndNonObjectDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		agent   model.AgentName
		path    string
		content string
		code    string
	}{
		{
			"malformed_json",
			model.AgentNamePi,
			".pi/agent/mcp.json",
			`{"mcpServers": "do-not-echo-secret`,
			"",
		},
		{
			"non_object_list",
			model.AgentNamePi,
			".pi/agent/mcp.json",
			`{"mcpServers":["do-not-echo-secret"]}`,
			"invalid_mcp_declarations",
		},
		{
			"non_object_server",
			model.AgentNamePi,
			".pi/agent/mcp.json",
			`{"mcpServers":{"invalid":"do-not-echo-secret"}}`,
			"invalid_mcp_server",
		},
		{
			"malformed_agent",
			model.AgentNameKiro,
			".kiro/agents/broken.md",
			"---\nmcpServers: [\ndo-not-echo-secret\n---\n",
			"invalid_agent_frontmatter",
		},
		{
			"unterminated_agent",
			model.AgentNameKiro,
			".kiro/agents/broken.md",
			"---\nmcpServers: {}\ndo-not-echo-secret\n",
			"invalid_agent_frontmatter",
		},
		{
			"malformed_cordis",
			model.AgentNameDSH,
			".dsh/cordis.patch.yml",
			"- insert: [do-not-echo-secret",
			"invalid_cordis_configuration",
		},
		{
			"multiple_cordis_documents",
			model.AgentNameDSH,
			".dsh/cordis.patch.yml",
			"[]\n---\n[]\n",
			"invalid_cordis_configuration",
		},
		{
			"non_sequence_cordis",
			model.AgentNameDSH,
			".dsh/cordis.patch.yml",
			"mcp: do-not-echo-secret",
			"unsupported_cordis_configuration",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, cwd := mcpFixtureDirectories(t)
			mcpFixtureFile(t, filepath.Join(home, filepath.FromSlash(test.path)), test.content)
			result := mcpFixtureRead(t, test.agent, home, cwd, map[string]string{})
			require.Empty(t, result.MCPServers)
			require.NotEmpty(t, result.Diagnostics)
			if test.code != "" {
				require.Contains(t, mcpFixtureDiagnosticCodes(result), test.code)
			}
			encoded, err := json.Marshal(result.Diagnostics)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "do-not-echo-secret")
		})
	}
}

func TestReadConfigurationMCPRestrictsClaudePrivateProjects(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	document := map[string]any{
		"mcpServers": map[string]any{"global": map[string]any{"command": "user-mcp"}},
		"projects": map[string]any{
			cwd: map[string]any{
				"mcpServers": map[string]any{"private": map[string]any{"command": "local-mcp"}},
			},
			filepath.Join(home, "other"): map[string]any{
				"mcpServers": map[string]any{"unrelated": map[string]any{"command": "other-mcp"}},
			},
		},
	}
	encoded, err := json.Marshal(document)
	require.NoError(t, err)
	mcpFixtureFile(t, filepath.Join(home, ".claude.json"), string(encoded))
	mcpFixtureFile(
		t,
		filepath.Join(cwd, ".mcp.json"),
		`{"mcpServers":{"team":{"type":"http","url":"https://example.invalid/mcp"}}}`,
	)
	result := mcpFixtureRead(t, model.AgentNameClaude, home, cwd, map[string]string{})
	names := make([]string, 0, len(result.MCPServers))
	for _, server := range result.MCPServers {
		names = append(names, server.Name)
	}
	require.ElementsMatch(t, []string{"global", "private", "team"}, names)
}

func TestReadConfigurationMCPKeepsInvalidClaudeTransportUnknown(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"invalid":{"url":"https://example.invalid/mcp"}}}`,
	)
	result := mcpFixtureRead(t, model.AgentNameClaude, home, cwd, map[string]string{})
	require.Len(t, result.MCPServers, 1)
	require.Equal(t, "unknown", result.MCPServers[0].Transport)
	require.Nil(t, result.MCPServers[0].Enabled)
	require.Contains(t, mcpFixtureDiagnosticCodes(result), "invalid_mcp_transport")
}

func TestReadConfigurationMCPReadsKiroAgentSourcesWithoutSelectingOne(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".kiro", "agents", "review.json"),
		`{"includeMcpJson":false,"mcpServers":{"review":{"command":"review-mcp"}}}`,
	)
	mcpFixtureFile(
		t,
		filepath.Join(cwd, ".kiro", "agents", "dev.md"),
		"---\nmcpServers:\n  dev:\n    command: dev-mcp\n---\nPrivate instructions are not part of the MCP declaration.\n",
	)
	result := mcpFixtureRead(t, model.AgentNameKiro, home, cwd, map[string]string{})
	require.Len(t, result.MCPServers, 2)
	for _, server := range result.MCPServers {
		require.Equal(t, model.ConfigurationScopeAgent, server.Source.Scope)
		require.NotEmpty(t, server.Source.Owner)
		require.Nil(t, server.Enabled)
	}
}

func TestReadConfigurationMCPUsesExplicitNativePathsAndInlineOpenCode(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	custom := filepath.Join(home, "custom")
	mcpFixtureFile(
		t,
		filepath.Join(custom, "opencode.json"),
		`{"mcp":{"fromFile":{"type":"remote","url":"https://example.invalid/{env:HOST}"}}}`,
	)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		`{"mcp":{"global":{"command":["global-mcp"]}}}`,
	)
	result := mcpFixtureRead(t, model.AgentNameOpenCode, home, cwd, map[string]string{
		"OPENCODE_CONFIG_DIR":     custom,
		"OPENCODE_CONFIG_CONTENT": "{ // inline\n\"mcp\":{\"inline\":{\"type\":\"local\",\"command\":[\"inline-mcp\"]},},}",
	})
	require.Len(t, result.MCPServers, 3)
	for _, server := range result.MCPServers {
		if server.Name == "fromFile" {
			require.Equal(t, "https://example.invalid/{env:HOST}", server.URL)
		}
		if server.Name == "inline" {
			require.Equal(t, "env:OPENCODE_CONFIG_CONTENT", server.Source.Path)
		}
	}
}

func TestReadConfigurationMCPUsesOpenClawStateAndConfigOverrides(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	custom := filepath.Join(home, "selected", "custom.json")
	mcpFixtureFile(
		t,
		custom,
		`{"mcp":{"servers":{"selected":{"url":"https://example.invalid/mcp","transport":"streamable-http"}}}}`,
	)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".openclaw", "openclaw.json"),
		`{"mcp":{"servers":{"ignored":{"command":"ignored-mcp"}}}}`,
	)
	result := mcpFixtureRead(
		t,
		model.AgentNameOpenClaw,
		home,
		cwd,
		map[string]string{"OPENCLAW_CONFIG_PATH": custom},
	)
	require.Len(t, result.MCPServers, 1)
	require.Equal(t, "selected", result.MCPServers[0].Name)
	require.Equal(t, "http", result.MCPServers[0].Transport)
}

func mcpFixtureRead(
	t *testing.T,
	agent model.AgentName,
	home, cwd string,
	env map[string]string,
) *model.AgentConfiguration {
	t.Helper()
	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent: agent, CWD: cwd, Kind: model.AgentConfigurationKindMCP,
		Home: home, Environment: env, IncludeSensitive: true,
	})
	require.NoError(t, err)
	validateConfigurationSnapshot(t, result)
	return result
}

func mcpFixtureDiagnosticCodes(result *model.AgentConfiguration) []string {
	codes := make([]string, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

func TestReadConfigurationMCPPreservesNativeDeclarationsWithoutConnecting(t *testing.T) {
	home, cwd := mcpFixtureDirectories(t)
	mcpFixtureFile(
		t,
		filepath.Join(home, ".pi", "agent", "mcp.json"),
		`{"mcpServers":{"issues":{"command":"missing-mcp-executable","args":["--flag"],"env":{"API_TOKEN":"fixture-secret"},"enabled":false,"custom":{"keep":true}}}}`,
	)

	result, err := ReadConfiguration(context.Background(), &ReadConfigurationRequest{
		Agent: model.AgentNamePi, CWD: cwd, Kind: model.AgentConfigurationKindMCP,
		Home: home, Environment: map[string]string{}, IncludeSensitive: true,
	})
	require.NoError(t, err)
	require.False(t, result.RuntimeVerified)
	require.Empty(t, result.Skills)
	require.Len(t, result.MCPServers, 1)
	server := result.MCPServers[0]
	require.Equal(t, "issues", server.Name)
	require.Equal(t, "stdio", server.Transport)
	require.Equal(t, "missing-mcp-executable", server.Command)
	require.Equal(t, []string{"--flag"}, server.Args)
	require.Equal(t, "fixture-secret", server.Environment["API_TOKEN"])
	require.Equal(t, map[string]any{"keep": true}, server.Native["custom"])
	require.NotNil(t, server.Enabled)
	require.False(t, *server.Enabled)
	require.Equal(t, "/mcpServers/issues", server.Source.Pointer)
	require.Equal(t, model.ConfigurationScopeUser, server.Source.Scope)
}

func mcpFixtureDirectories(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, ".git"), 0o700))
	return home, cwd
}

func mcpFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
