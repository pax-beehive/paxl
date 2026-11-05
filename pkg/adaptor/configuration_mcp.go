package adaptor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func (r *configurationReader) readMCP() {
	switch r.req.Agent {
	case model.AgentNameCodex:
		r.readCodexMCP()
	case model.AgentNameClaude:
		r.readClaudeMCP()
	case model.AgentNamePi:
		r.readPiMCP()
	case model.AgentNameKiro:
		r.readKiroMCP()
	case model.AgentNameOpenCode:
		r.readOpenCodeMCP()
	case model.AgentNameKimi:
		r.readKimiMCP()
	case model.AgentNameHermes:
		r.mcpDocument(
			filepath.Join(
				r.envPath("HERMES_HOME", filepath.Join(r.home, ".hermes")),
				"config.yaml",
			),
			model.ConfigurationScopeProfile,
			"",
			"mcp_servers",
		)
	case model.AgentNameOpenClaw:
		r.readOpenClawMCP()
	case model.AgentNameDSH, model.AgentNameDSCode:
		r.readCordisMCP()
	case model.AgentNameUnknown,
		model.AgentNameGemini,
		model.AgentNamePaxl,
		model.AgentNameAntigravity:
		return
	}
	r.mcpLimitation(
		"This is a passive inventory of known local declarations. Runtime connections, credentials, trust, startup overrides, managed policies and dynamically registered plugin servers are not verified.",
	)
}

func (r *configurationReader) mcpDocument(
	path string,
	scope model.ConfigurationScope,
	owner string,
	keys ...string,
) {
	document, source, ok := r.readDocument(path, scope)
	if !ok {
		return
	}
	source.Owner = owner
	r.mcpDocumentMap(document, &source, keys...)
}

func (r *configurationReader) mcpDocumentMap(
	document map[string]any,
	source *model.ConfigurationSource,
	keys ...string,
) {
	var value any = document
	for _, key := range keys {
		value = configurationMap(value)[key]
	}
	if value == nil {
		return
	}
	servers := configurationMap(value)
	if servers == nil {
		r.addDiagnostic(
			"invalid_mcp_declarations",
			"MCP server declarations must be an object keyed by server name.",
			source,
		)
		return
	}
	for _, name := range configurationMCPKeys(servers) {
		candidateSource := *source
		candidateSource.Pointer = source.Pointer + configurationPointer(append(keys, name)...)
		native := configurationMap(servers[name])
		if native == nil {
			r.addDiagnostic(
				"invalid_mcp_server",
				"An MCP server declaration is not an object; its value was not returned.",
				&candidateSource,
			)
			continue
		}
		r.appendMCP(name, native, &candidateSource, nil)
	}
}

func (r *configurationReader) appendMCP(
	name string,
	native map[string]any,
	source *model.ConfigurationSource,
	enabled *bool,
) {
	if name == "" {
		r.addDiagnostic(
			"unnamed_mcp_server",
			"An MCP server declaration has no literal server name.",
			source,
		)
		return
	}
	command := configurationString(native["command"])
	args := configurationStrings(native["args"])
	if values := configurationStrings(native["command"]); command == "" && len(values) > 0 {
		command, args = values[0], append(values[1:], args...)
	}
	if enabled == nil {
		enabled = configurationBool(native["enabled"])
		if disabled := configurationBool(native["disabled"]); enabled == nil && disabled != nil {
			value := !*disabled
			enabled = &value
		}
	}
	url := configurationString(native["url"])
	if url == "" {
		url = configurationString(native["serverUrl"])
	}
	environment := configurationMCPStringMap(native["env"])
	for key, value := range configurationMCPStringMap(native["environment"]) {
		environment[key] = value
	}
	headers := configurationMCPStringMap(native["headers"])
	for key, value := range configurationMCPStringMap(native["http_headers"]) {
		headers[key] = value
	}
	for key, name := range configurationMCPStringMap(native["env_http_headers"]) {
		headers[key] = "${" + name + "}"
	}
	for _, key := range configurationStrings(native["env_vars"]) {
		if _, configured := environment[key]; !configured {
			environment[key] = "${" + key + "}"
		}
	}
	server := model.MCPServerConfiguration{
		ID:          configurationID("mcp", name, source),
		Name:        name,
		Transport:   r.mcpTransport(native, command, url),
		Command:     command,
		Args:        args,
		CWD:         configurationString(native["cwd"]),
		URL:         url,
		Environment: environment,
		Headers:     headers,
		Enabled:     enabled,
		Source:      *source,
		Origin:      model.ConfigurationOrigin{Kind: "unknown"},
		Native:      native,
	}
	if server.Args == nil {
		server.Args = []string{}
	}
	if r.req.Agent == model.AgentNameClaude && url != "" && command == "" &&
		configurationString(native["type"]) == "" &&
		configurationString(native["transport"]) == "" {
		r.addDiagnostic(
			"invalid_mcp_transport",
			"Claude URL declarations require an explicit transport type; availability was not inferred.",
			source,
		)
	}
	r.result.MCPServers = append(r.result.MCPServers, server)
}

func (r *configurationReader) mcpTransport(native map[string]any, command, url string) string {
	transport := configurationString(native["transport"])
	if transport == "" {
		transport = configurationString(native["type"])
	}
	switch strings.ToLower(transport) {
	case "stdio", "local":
		return "stdio"
	case "http", "streamable-http", "streamable_http", "remote":
		return "http"
	case "sse":
		return "sse"
	case "ws", "websocket":
		return "ws"
	case "":
		if command != "" {
			return "stdio"
		}
		if url != "" {
			if r.req.Agent == model.AgentNameClaude {
				return "unknown"
			}
			if r.req.Agent == model.AgentNameOpenClaw {
				return "sse"
			}
			return "http"
		}
	}
	return "unknown"
}

func (r *configurationReader) mcpLimitation(message string) {
	if r.result.Coverage == nil {
		r.result.Coverage = make(map[model.AgentConfigurationKind]model.ConfigurationCoverage)
	}
	coverage := r.result.Coverage[model.AgentConfigurationKindMCP]
	coverage.Status = "partial"
	for _, existing := range coverage.Limitations {
		if existing == message {
			return
		}
	}
	coverage.Limitations = append(coverage.Limitations, message)
	r.result.Coverage[model.AgentConfigurationKindMCP] = coverage
}

func configurationMCPStringMap(value any) map[string]string {
	result := make(map[string]string)
	for key, value := range configurationMap(value) {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func configurationMCPKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func configurationMCPGlob(pattern string) []string {
	paths, _ := filepath.Glob(pattern)
	sort.Strings(paths)
	return paths
}

func configurationMCPProfileOwner(name string) string {
	return fmt.Sprintf("profile:%s", name)
}
