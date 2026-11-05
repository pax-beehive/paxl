package adaptor

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"gopkg.in/yaml.v3"
)

func (r *configurationReader) readCordisMCP() {
	if r.req.Agent == model.AgentNameDSH {
		home := r.envPath("DSH_HOME", filepath.Join(r.home, ".dsh"))
		r.cordisMCPHome(home, false)
	} else if home := r.envPath("DSCODE_HOME", ""); home != "" {
		r.cordisMCPHome(home, true)
	} else {
		r.cordisMCPHome(filepath.Join(r.home, ".local", "share", "dscode-state"), true)
		r.cordisMCPHome(filepath.Join(r.home, ".local", "share", "dscode-hub"), true)
	}
	r.mcpLimitation(
		"Cordis inventory includes literal MCP client/manager rows from known home/profile/local patches only. Bundle dependencies, id-only overrides, selected profile and runtime plugin registrations are not composed. YAML expressions and aliases are never evaluated.",
	)
	if r.req.Agent == model.AgentNameDSCode {
		r.mcpLimitation(
			"DSCODE current and legacy state layouts may coexist. Source/tar installation-local files and lazy browser MCP connections require runtime-specific discovery and are not enumerated here.",
		)
	}
}

func (r *configurationReader) cordisMCPHome(home string, dscode bool) {
	r.cordisMCPFile(filepath.Join(home, "cordis.patch.yml"), model.ConfigurationScopeUser, "home")
	for _, path := range configurationMCPGlob(filepath.Join(home, "profiles", "*", "cordis.patch.yml")) {
		owner := configurationMCPProfileOwner(filepath.Base(filepath.Dir(path)))
		r.cordisMCPFile(path, model.ConfigurationScopeProfile, owner)
	}
	if dscode {
		for _, name := range []string{"mcp.local.yml", "harness.local.yml"} {
			r.cordisMCPFile(
				filepath.Join(home, "config", name),
				model.ConfigurationScopeUser,
				"local",
			)
		}
	}
}

func (r *configurationReader) cordisMCPFile(
	path string,
	scope model.ConfigurationScope,
	owner string,
) {
	data, source, ok := r.readFile(path, scope, "yaml")
	if !ok {
		return
	}
	source.Owner = owner
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		r.addDiagnostic(
			"invalid_cordis_configuration",
			"Cordis configuration could not be parsed as YAML; parser details were withheld.",
			&source,
		)
		return
	}
	var extra yaml.Node
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		r.addDiagnostic(
			"invalid_cordis_configuration",
			"Cordis configuration must contain one YAML document.",
			&source,
		)
		return
	}
	if document.Content[0].Kind != yaml.SequenceNode {
		r.addDiagnostic(
			"unsupported_cordis_configuration",
			"Cordis patch configuration must be a sequence of loader rows.",
			&source,
		)
		return
	}
	r.cordisMCPRows(document.Content[0], "", &source)
}

func (r *configurationReader) cordisMCPRows(
	sequence *yaml.Node,
	pointer string,
	source *model.ConfigurationSource,
) {
	for index, node := range sequence.Content {
		if r.ctx.Err() != nil {
			return
		}
		rowPointer := pointer + configurationPointer(strconv.Itoa(index))
		row, expressions := configurationCordisMap(node)
		rowSource := *source
		rowSource.Pointer = rowPointer
		if expressions {
			r.addDiagnostic(
				"unresolved_cordis_expression",
				"Cordis expressions or aliases were retained as unresolved markers and were not evaluated.",
				&rowSource,
			)
		}
		if row == nil {
			continue
		}
		if insert := configurationCordisChild(node, "insert"); insert != nil &&
			insert.Kind == yaml.SequenceNode {
			r.cordisMCPRows(insert, rowPointer+"/insert", source)
		}
		if group := configurationBool(row["group"]); group != nil && *group {
			if config := configurationCordisChild(node, "config"); config != nil &&
				config.Kind == yaml.SequenceNode {
				r.cordisMCPRows(config, rowPointer+"/config", source)
			}
		}
		r.cordisMCPRow(row, &rowSource)
	}
}

func (r *configurationReader) cordisMCPRow(row map[string]any, source *model.ConfigurationSource) {
	name := configurationString(row["name"])
	if name == "" && row["id"] != nil && row["insert"] == nil {
		r.addDiagnostic(
			"unresolved_cordis_patch",
			"An id-only Cordis patch requires native bundle composition; it is not treated as an effective MCP server declaration.",
			source,
		)
		return
	}
	config := configurationMap(row["config"])
	if config == nil {
		return
	}
	rowSource := *source
	if id := configurationString(row["id"]); id != "" {
		rowSource.Owner += "/entry:" + id
	}
	switch name {
	case "@deepseek-ai/dsh-mcp-client":
		rowSource.Pointer += "/config"
		r.appendCordisMCP(configurationString(config["serverName"]), config, row, &rowSource)
	case "@deepseek-ai/dsh-mcp-manager":
		for _, serverName := range configurationMCPKeys(configurationMap(config["servers"])) {
			serverSource := rowSource
			serverSource.Pointer += configurationPointer("config", "servers", serverName)
			server := configurationMap(configurationMap(config["servers"])[serverName])
			if server != nil {
				r.appendCordisMCP(serverName, server, row, &serverSource)
			}
		}
	}
}

func (r *configurationReader) appendCordisMCP(
	name string,
	config, row map[string]any,
	source *model.ConfigurationSource,
) {
	var enabled *bool
	if disabled := configurationBool(row["disabled"]); disabled != nil && *disabled {
		value := false
		enabled = &value
	}
	before := len(r.result.MCPServers)
	r.appendMCP(name, config, source, enabled)
	if len(r.result.MCPServers) > before {
		// Keep loader metadata alongside the complete native server definition.
		r.result.MCPServers[before].Native = map[string]any{"entry": row, "server": config}
		if disabled, exists := row["disabled"]; exists && disabled != nil &&
			configurationBool(disabled) == nil {
			r.result.MCPServers[before].Enabled = nil
		}
	}
}

func configurationCordisChild(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func configurationCordisMap(node *yaml.Node) (map[string]any, bool) {
	value, unresolved := configurationCordisValue(node, 0)
	return configurationMap(value), unresolved
}

func configurationCordisValue(node *yaml.Node, depth int) (any, bool) {
	if depth > 32 || node.Kind == yaml.AliasNode || node.Tag == "!!js" || node.Tag == "!js" ||
		strings.HasSuffix(node.Tag, ":js") {
		return map[string]any{"unresolved": "native_yaml_expression_or_alias"}, true
	}
	switch node.Kind {
	case yaml.MappingNode:
		result, unresolved := map[string]any{}, false
		for index := 0; index+1 < len(node.Content); index += 2 {
			value, unknown := configurationCordisValue(node.Content[index+1], depth+1)
			result[node.Content[index].Value] = value
			unresolved = unresolved || unknown
		}
		return result, unresolved
	case yaml.SequenceNode:
		result, unresolved := []any{}, false
		for _, child := range node.Content {
			value, unknown := configurationCordisValue(child, depth+1)
			result = append(result, value)
			unresolved = unresolved || unknown
		}
		return result, unresolved
	case yaml.ScalarNode:
		var value any
		if node.Decode(&value) == nil {
			return value, false
		}
	case yaml.DocumentNode, yaml.AliasNode:
	}
	return map[string]any{"unresolved": "unsupported_native_yaml_value"}, true
}
