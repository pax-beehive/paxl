package adaptor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func (r *configurationReader) readCodexMCP() {
	home := r.envPath("CODEX_HOME", filepath.Join(r.home, ".codex"))
	r.codexMCPDocument(filepath.Join(home, "config.toml"), model.ConfigurationScopeUser)
	for _, path := range configurationMCPGlob(filepath.Join(home, "*.config.toml")) {
		r.codexMCPDocument(path, model.ConfigurationScopeProfile)
	}
	for _, dir := range r.projectDirs() {
		r.codexMCPDocument(
			filepath.Join(dir, ".codex", "config.toml"),
			model.ConfigurationScopeProject,
		)
	}
	r.mcpLimitation(
		"Codex project trust, selected native profile, managed requirements and plugin MCP declarations are not evaluated; profile declarations are retained as separate candidates.",
	)
}

func (r *configurationReader) codexMCPDocument(path string, scope model.ConfigurationScope) {
	document, source, ok := r.readDocument(path, scope)
	if !ok {
		return
	}
	if scope == model.ConfigurationScopeProfile {
		source.Owner = filepath.Base(path)
	}
	r.mcpDocumentMap(document, &source, "mcp_servers")
	profiles := configurationMap(document["profiles"])
	for _, name := range configurationMCPKeys(profiles) {
		profileSource := source
		profileSource.Scope, profileSource.Owner = model.ConfigurationScopeProfile, configurationMCPProfileOwner(
			name,
		)
		profileSource.Pointer = configurationPointer("profiles", name)
		r.mcpDocumentMap(configurationMap(profiles[name]), &profileSource, "mcp_servers")
	}
}

func (r *configurationReader) readClaudeMCP() {
	configHome := r.envPath("CLAUDE_CONFIG_DIR", filepath.Join(r.home, ".claude"))
	globalPath := filepath.Join(r.home, ".claude.json")
	if strings.TrimSpace(r.env["CLAUDE_CONFIG_DIR"]) != "" {
		globalPath = filepath.Join(configHome, ".claude.json")
	}
	if document, source, ok := r.readDocument(globalPath, model.ConfigurationScopeUser); ok {
		r.mcpDocumentMap(document, &source, "mcpServers")
		for path, value := range configurationMap(document["projects"]) {
			if !r.mcpSameDirectory(path, r.cwd) {
				continue
			}
			localSource := source
			localSource.Scope, localSource.Owner = model.ConfigurationScopeProject, "local"
			localSource.Pointer = configurationPointer("projects", path)
			r.mcpDocumentMap(configurationMap(value), &localSource, "mcpServers")
		}
	}
	for _, dir := range r.projectDirs() {
		r.mcpDocument(
			filepath.Join(dir, ".mcp.json"),
			model.ConfigurationScopeProject,
			"",
			"mcpServers",
		)
	}
	r.mcpLimitation(
		"Claude managed and plugin servers, claude.ai connectors, project approvals and persisted MCP toggles are not evaluated; local declarations are limited to the requested working directory.",
	)
}

func (r *configurationReader) mcpSameDirectory(left, right string) bool {
	left, right = r.expandPath(left, r.home), filepath.Clean(right)
	if left == right {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && resolvedLeft == resolvedRight
}

func (r *configurationReader) readPiMCP() {
	home := r.envPath("PI_CODING_AGENT_DIR", filepath.Join(r.home, ".pi", "agent"))
	r.mcpDocument(filepath.Join(home, "mcp.json"), model.ConfigurationScopeUser, "", "mcpServers")
	r.mcpDocument(
		filepath.Join(r.cwd, ".pi", "mcp.json"),
		model.ConfigurationScopeProject,
		"",
		"mcpServers",
	)
	r.mcpLimitation(
		"Pi SDK generation, project trust, built-in MCP enablement and extension-provided servers are not inferred from file presence.",
	)
}

func (r *configurationReader) readKiroMCP() {
	home := r.envPath("KIRO_HOME", filepath.Join(r.home, ".kiro"))
	r.mcpDocument(
		filepath.Join(home, "settings", "mcp.json"),
		model.ConfigurationScopeUser,
		"",
		"mcpServers",
	)
	r.kiroAgentMCP(filepath.Join(home, "agents"))
	for _, dir := range r.projectDirs() {
		r.mcpDocument(
			filepath.Join(dir, ".kiro", "settings", "mcp.json"),
			model.ConfigurationScopeProject,
			"",
			"mcpServers",
		)
		r.kiroAgentMCP(filepath.Join(dir, ".kiro", "agents"))
	}
	r.mcpLimitation(
		"Kiro V2/V3 engine selection, selected agent, includeMcpJson, Powers, cloud and managed policy are not evaluated; agent definitions are independent candidates.",
	)
}

func (r *configurationReader) kiroAgentMCP(dir string) {
	for _, path := range configurationMCPGlob(filepath.Join(dir, "*.json")) {
		r.mcpDocument(path, model.ConfigurationScopeAgent, filepath.Base(path), "mcpServers")
	}
	for _, path := range configurationMCPGlob(filepath.Join(dir, "*.md")) {
		data, source, ok := r.readFile(path, model.ConfigurationScopeAgent, "markdown")
		if !ok {
			continue
		}
		document, err := configurationMCPFrontmatter(data)
		if err != nil {
			r.addDiagnostic(
				"invalid_agent_frontmatter",
				"Agent frontmatter could not be parsed; parser details were withheld.",
				&source,
			)
			continue
		}
		if document == nil {
			continue
		}
		source.Owner = filepath.Base(path)
		r.mcpDocumentMap(document, &source, "mcpServers")
	}
}

func configurationMCPFrontmatter(data []byte) (map[string]any, error) {
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return map[string]any{}, nil
	}
	lines := strings.Split(text, "\n")
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return parseConfigurationDocument([]byte(strings.Join(lines[1:index], "\n")), "yaml")
		}
	}
	return nil, fmt.Errorf("unterminated agent frontmatter")
}

func (r *configurationReader) readOpenCodeMCP() {
	global := filepath.Join(
		r.envPath("XDG_CONFIG_HOME", filepath.Join(r.home, ".config")),
		"opencode",
	)
	r.openCodeMCPDirectory(global, model.ConfigurationScopeUser, "")
	if path := r.envPath("OPENCODE_CONFIG", ""); path != "" {
		r.mcpDocument(path, model.ConfigurationScopeUser, "OPENCODE_CONFIG", "mcp")
	}
	for _, dir := range r.projectDirs() {
		r.openCodeMCPDirectory(dir, model.ConfigurationScopeProject, "")
		r.openCodeMCPDirectory(filepath.Join(dir, ".opencode"), model.ConfigurationScopeProject, "")
	}
	if dir := r.envPath("OPENCODE_CONFIG_DIR", ""); dir != "" && dir != global {
		r.openCodeMCPDirectory(dir, model.ConfigurationScopeUser, "OPENCODE_CONFIG_DIR")
	}
	if content := r.env["OPENCODE_CONFIG_CONTENT"]; content != "" {
		source := model.ConfigurationSource{
			Path:   "env:OPENCODE_CONFIG_CONTENT",
			Scope:  model.ConfigurationScopeUser,
			Format: "json",
			Owner:  "OPENCODE_CONFIG_CONTENT",
			Status: model.ConfigurationSourceStatusRead,
		}
		document, err := parseConfigurationDocument([]byte(content), "jsonc")
		if err != nil {
			source.Status = model.ConfigurationSourceStatusError
			r.addDiagnostic(
				"invalid_inline_mcp_configuration",
				"Inline OpenCode configuration is not supported JSON/JSONC; its content was not returned.",
				&source,
			)
		} else {
			r.mcpDocumentMap(document, &source, "mcp")
		}
		r.result.Sources = append(r.result.Sources, source)
	}
	r.mcpLimitation(
		"OpenCode v1 known local config files are inventoried. Remote defaults, managed/MDM policy, plugins, file interpolation and runtime merging are not evaluated.",
	)
}

func (r *configurationReader) openCodeMCPDirectory(
	dir string,
	scope model.ConfigurationScope,
	owner string,
) {
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		r.mcpDocument(filepath.Join(dir, name), scope, owner, "mcp")
	}
}

func (r *configurationReader) readKimiMCP() {
	home := r.envPath("KIMI_CODE_HOME", filepath.Join(r.home, ".kimi-code"))
	r.mcpDocument(
		filepath.Join(home, "mcp.json"),
		model.ConfigurationScopeUser,
		"current",
		"mcpServers",
	)
	r.mcpDocument(
		filepath.Join(r.home, ".kimi", "mcp.json"),
		model.ConfigurationScopeUser,
		"legacy",
		"mcpServers",
	)
	r.mcpDocument(
		filepath.Join(r.root, ".mcp.json"),
		model.ConfigurationScopeProject,
		"current",
		"mcpServers",
	)
	r.mcpDocument(
		filepath.Join(r.cwd, ".kimi-code", "mcp.json"),
		model.ConfigurationScopeProject,
		"current",
		"mcpServers",
	)
	r.mcpLimitation(
		"Current Node.js and legacy Python Kimi declarations are labeled separately. Installed version, workspace trust, plugins and CLI MCP additions are not resolved.",
	)
}

func (r *configurationReader) readOpenClawMCP() {
	home := r.envPath("OPENCLAW_HOME", r.home)
	name := ".openclaw"
	if profile := strings.TrimSpace(r.env["OPENCLAW_PROFILE"]); profile != "" &&
		profile != "default" {
		name += "-" + profile
	}
	state := r.envPath("OPENCLAW_STATE_DIR", filepath.Join(home, name))
	path := r.envPath("OPENCLAW_CONFIG_PATH", filepath.Join(state, "openclaw.json"))
	r.mcpDocument(path, model.ConfigurationScopeProfile, "", "mcp", "servers")
	r.mcpLimitation(
		"OpenClaw inventory reads the selected local configuration path. A running Gateway may use different paths, includes, agent overrides, registry or plugin servers; these are not queried.",
	)
}
