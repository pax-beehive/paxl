package adaptor

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"gopkg.in/yaml.v3"
)

type configurationSkillSettings struct {
	document map[string]any
	source   model.ConfigurationSource
}

func (r *configurationReader) skillSettingsDocuments() []configurationSkillSettings {
	var paths []model.ConfigurationSource
	add := func(path string, scope model.ConfigurationScope) {
		paths = append(paths, model.ConfigurationSource{Path: path, Scope: scope})
	}
	switch r.req.Agent {
	case model.AgentNameCodex:
		add(
			filepath.Join(r.envPath("CODEX_HOME", filepath.Join(r.home, ".codex")), "config.toml"),
			model.ConfigurationScopeUser,
		)
		for _, dir := range r.skillProjectDirsAscending() {
			add(filepath.Join(dir, ".codex", "config.toml"), model.ConfigurationScopeProject)
		}
	case model.AgentNameClaude:
		home := r.envPath(
			"CLAUDE_CONFIG_DIR",
			r.envPath("CLAUDE_HOME", filepath.Join(r.home, ".claude")),
		)
		add(filepath.Join(home, "settings.json"), model.ConfigurationScopeUser)
		for _, dir := range r.skillProjectDirsAscending() {
			add(filepath.Join(dir, ".claude", "settings.json"), model.ConfigurationScopeProject)
			add(
				filepath.Join(dir, ".claude", "settings.local.json"),
				model.ConfigurationScopeProject,
			)
		}
	case model.AgentNamePi:
		add(
			filepath.Join(
				r.envPath("PI_CODING_AGENT_DIR", filepath.Join(r.home, ".pi", "agent")),
				"settings.json",
			),
			model.ConfigurationScopeUser,
		)
		add(filepath.Join(r.root, ".pi", "settings.json"), model.ConfigurationScopeProject)
	case model.AgentNameKimi:
		add(
			filepath.Join(
				r.envPath("KIMI_CODE_HOME", filepath.Join(r.home, ".kimi-code")),
				"config.toml",
			),
			model.ConfigurationScopeUser,
		)
	case model.AgentNameHermes:
		add(
			filepath.Join(
				r.envPath("HERMES_HOME", filepath.Join(r.home, ".hermes")),
				"config.yaml",
			),
			model.ConfigurationScopeProfile,
		)
	case model.AgentNameOpenClaw:
		state, config := r.skillOpenClawPaths()
		_ = state
		add(config, model.ConfigurationScopeProfile)
	case model.AgentNameDSH:
		home := r.envPath("DSH_HOME", filepath.Join(r.home, ".dsh"))
		add(
			filepath.Join(home, "profiles", "acp", "cordis.patch.yml"),
			model.ConfigurationScopeProfile,
		)
		add(filepath.Join(home, "cordis.patch.yml"), model.ConfigurationScopeUser)
	case model.AgentNameDSCode:
		home := r.envPath("DSCODE_HOME", filepath.Join(r.home, ".local", "share", "dscode-state"))
		add(filepath.Join(home, "config", "mcp.local.yml"), model.ConfigurationScopeProfile)
		add(filepath.Join(home, "config", "harness.local.yml"), model.ConfigurationScopeProfile)
	case model.AgentNameUnknown,
		model.AgentNameKiro,
		model.AgentNameOpenCode,
		model.AgentNameGemini,
		model.AgentNamePaxl,
		model.AgentNameAntigravity:
	}
	var result []configurationSkillSettings
	for _, path := range paths {
		if r.req.Agent == model.AgentNameDSH || r.req.Agent == model.AgentNameDSCode {
			if document, source, ok := r.skillCordisSettings(path.Path, path.Scope); ok {
				result = append(
					result,
					configurationSkillSettings{document: document, source: source},
				)
			}
			continue
		}
		if document, source, ok := r.readDocument(path.Path, path.Scope); ok {
			result = append(result, configurationSkillSettings{document: document, source: source})
		}
	}
	return result
}

func (r *configurationReader) skillCordisSettings(
	path string,
	scope model.ConfigurationScope,
) (map[string]any, model.ConfigurationSource, bool) {
	data, source, ok := r.readFile(path, scope, "yaml")
	if !ok {
		return nil, source, false
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document, extra yaml.Node
	if decoder.Decode(&document) != nil || len(document.Content) != 1 ||
		!errors.Is(decoder.Decode(&extra), io.EOF) {
		r.addDiagnostic(
			"invalid_cordis_skill_settings",
			"Cordis skill settings must contain one YAML document.",
			&source,
		)
		return nil, source, false
	}
	value, unresolved := configurationCordisValue(document.Content[0], 0)
	if unresolved {
		r.addDiagnostic(
			"unresolved_cordis_skill_settings",
			"Cordis skill setting expressions were not evaluated.",
			&source,
		)
	}
	return map[string]any{"rows": value}, source, true
}

func (r *configurationReader) skillProjectDirsAscending() []string {
	dirs := append([]string(nil), r.projectDirs()...)
	for left, right := 0, len(dirs)-1; left < right; left, right = left+1, right-1 {
		dirs[left], dirs[right] = dirs[right], dirs[left]
	}
	return dirs
}

func (r *configurationReader) skillOpenClawPaths() (string, string) {
	home := r.envPath("OPENCLAW_HOME", r.home)
	defaultState := filepath.Join(home, ".openclaw")
	if profile := r.env["OPENCLAW_PROFILE"]; profile != "" && profile != "default" {
		defaultState += "-" + profile
	}
	state := r.envPath("OPENCLAW_STATE_DIR", defaultState)
	return state, r.envPath("OPENCLAW_CONFIG_PATH", filepath.Join(state, "openclaw.json"))
}

func (r *configurationReader) applySkillSettings() {
	documents := r.skillSettingsDocuments()
	for i := range r.result.Skills {
		skill := &r.result.Skills[i]
		for _, item := range documents {
			r.applySkillDocumentSettings(skill, &item)
		}
	}
}

func (r *configurationReader) applySkillDocumentSettings(
	skill *model.SkillConfiguration,
	item *configurationSkillSettings,
) {
	switch r.req.Agent {
	case model.AgentNameCodex:
		r.applyCodexSkillSettings(skill, item)
	case model.AgentNameClaude:
		configurationApplyClaudeSkillSettings(skill, item.document)
	case model.AgentNamePi:
		r.applyPiSkillSettings(skill, item)
	case model.AgentNameHermes:
		r.applyHermesSkillSettings(skill, item)
	case model.AgentNameOpenClaw:
		configurationApplyOpenClawSkillSettings(skill, item.document)
	case model.AgentNameUnknown,
		model.AgentNameKiro,
		model.AgentNameOpenCode,
		model.AgentNameKimi,
		model.AgentNameGemini,
		model.AgentNamePaxl,
		model.AgentNameDSH,
		model.AgentNameDSCode,
		model.AgentNameAntigravity:
	}
}

func (r *configurationReader) applyCodexSkillSettings(
	skill *model.SkillConfiguration,
	item *configurationSkillSettings,
) {
	entries, _ := configurationMap(item.document["skills"])["config"].([]any)
	for _, raw := range entries {
		entry := configurationMap(raw)
		matches := configurationString(entry["name"]) == skill.Name
		if path := configurationString(entry["path"]); path != "" {
			path = r.expandPath(path, filepath.Dir(item.source.Path))
			matches = path == skill.Source.Path || path == skill.Directory
		}
		if matches {
			if enabled := configurationBool(entry["enabled"]); enabled != nil {
				skill.Enabled = enabled
			}
		}
	}
}

func configurationApplyClaudeSkillSettings(
	skill *model.SkillConfiguration,
	document map[string]any,
) {
	if skill.Source.Scope == model.ConfigurationScopePlugin {
		if enabled := configurationBool(
			configurationMap(document["enabledPlugins"])[skill.Source.Owner],
		); enabled != nil {
			skill.Enabled = enabled
		}
		return
	}
	overrides := configurationMap(document["skillOverrides"])
	state := configurationString(overrides[skill.Name])
	switch state {
	case "on":
		skill.Enabled = configurationSkillBool(true)
	case "off":
		skill.Enabled = configurationSkillBool(false)
	case "name-only":
		skill.Enabled = configurationSkillBool(true)
	case "user-invocable-only":
		skill.ModelInvocable = configurationSkillBool(false)
		skill.UserInvocable = configurationSkillBool(true)
	}
}

func (r *configurationReader) applyPiSkillSettings(
	skill *model.SkillConfiguration,
	item *configurationSkillSettings,
) {
	if commands := configurationBool(item.document["enableSkillCommands"]); commands != nil &&
		!*commands {
		skill.UserInvocable = configurationSkillBool(false)
	}
	for _, filter := range configurationStrings(item.document["skills"]) {
		if !strings.HasPrefix(filter, "!") && !strings.HasPrefix(filter, "-") {
			continue
		}
		pattern := r.expandPath(filter[1:], filepath.Dir(item.source.Path))
		for _, path := range []string{skill.Source.Path, skill.Directory} {
			if matched, _ := filepath.Match(pattern, path); matched {
				skill.Enabled = configurationSkillBool(false)
			}
		}
	}
}

func (r *configurationReader) applyHermesSkillSettings(
	skill *model.SkillConfiguration,
	item *configurationSkillSettings,
) {
	settings := configurationMap(item.document["skills"])
	for _, name := range configurationStrings(settings["disabled"]) {
		if name == skill.Name && name != "hermes-agent" {
			skill.Enabled = configurationSkillBool(false)
		}
	}
	if skill.Source.Scope == model.ConfigurationScopeProject {
		if enabled := configurationBool(settings["project_discovery"]); enabled != nil &&
			!*enabled {
			skill.Enabled = configurationSkillBool(false)
		}
	}
}

func configurationApplyOpenClawSkillSettings(
	skill *model.SkillConfiguration,
	document map[string]any,
) {
	key := skill.Name
	metadata := configurationMap(configurationMap(skill.Frontmatter["metadata"])["openclaw"])
	if alias := configurationString(metadata["skillKey"]); alias != "" {
		key = alias
	}
	entry := configurationMap(
		configurationMap(configurationMap(document["skills"])["entries"])[key],
	)
	if enabled := configurationBool(entry["enabled"]); enabled != nil {
		skill.Enabled = enabled
	}
}
