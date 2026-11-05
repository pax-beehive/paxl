package adaptor

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func (r *configurationReader) claudePluginSkillRoots() []configurationSkillRoot {
	home := r.envPath(
		"CLAUDE_CONFIG_DIR",
		r.envPath("CLAUDE_HOME", filepath.Join(r.home, ".claude")),
	)
	document, source, ok := r.readDocument(
		filepath.Join(home, "plugins", "installed_plugins.json"),
		model.ConfigurationScopeUser,
	)
	if !ok {
		return nil
	}
	plugins := configurationMap(document["plugins"])
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	var roots []configurationSkillRoot
	for _, name := range names {
		entries, _ := plugins[name].([]any)
		for _, raw := range entries {
			entry := configurationMap(raw)
			if project := configurationString(entry["projectPath"]); project != "" &&
				filepath.Clean(project) != r.root &&
				filepath.Clean(project) != r.cwd {
				continue
			}
			path := configurationString(entry["installPath"])
			if path == "" {
				continue
			}
			path = r.expandPath(path, home)
			for _, kind := range []string{"skills", "commands"} {
				root := r.flatSkillRoot(filepath.Join(path, kind), model.ConfigurationScopePlugin)
				root.flat = kind == "commands"
				root.owner = name
				root.origin = model.ConfigurationOrigin{
					Kind:     "plugin",
					Package:  name,
					Version:  configurationString(entry["version"]),
					Ref:      configurationString(entry["gitCommitSha"]),
					Evidence: source.Path,
				}
				roots = append(roots, root)
			}
		}
	}
	return roots
}

func (r *configurationReader) kimiPluginSkillRoots() []configurationSkillRoot {
	home := r.envPath("KIMI_CODE_HOME", filepath.Join(r.home, ".kimi-code"))
	document, source, ok := r.readDocument(
		filepath.Join(home, "plugins", "installed.json"),
		model.ConfigurationScopeUser,
	)
	if !ok {
		return nil
	}
	entries, _ := document["plugins"].([]any)
	var roots []configurationSkillRoot
	for _, raw := range entries {
		entry := configurationMap(raw)
		path := configurationString(entry["root"])
		if path == "" {
			continue
		}
		path = r.expandPath(path, home)
		manifest, manifestSource, exists := r.readDocument(
			filepath.Join(path, "kimi.plugin.json"),
			model.ConfigurationScopePlugin,
		)
		if !exists {
			continue
		}
		paths := configurationStrings(manifest["skills"])
		if len(paths) == 0 {
			paths = []string{"."}
		}
		for _, root := range r.skillPathRoots(paths, path, model.ConfigurationScopePlugin) {
			root.owner = configurationString(entry["id"])
			root.enabled = configurationBool(entry["enabled"])
			root.origin = model.ConfigurationOrigin{
				Kind:     "plugin",
				Package:  root.owner,
				Version:  configurationString(manifest["version"]),
				Evidence: source.Path,
			}
			if origin := configurationString(entry["originalSource"]); strings.Contains(
				origin,
				"://",
			) {
				root.origin.URL = origin
			}
			if root.origin.Version == "" {
				root.origin.Evidence = manifestSource.Path
			}
			roots = append(roots, root)
		}
	}
	return roots
}

func (r *configurationReader) applySkillOrigins() {
	for i := range r.result.Skills {
		skill := &r.result.Skills[i]
		path := filepath.Join(skill.Directory, ".clawhub", "origin.json")
		document, source, ok := r.readDocument(path, skill.Source.Scope)
		if !ok {
			continue
		}
		slug := configurationString(document["slug"])
		registry := configurationString(document["registry"])
		if slug != "" && registry != "" {
			skill.Origin = model.ConfigurationOrigin{
				Kind:     "registry",
				URL:      registry,
				Package:  slug,
				Version:  configurationString(document["installedVersion"]),
				Evidence: source.Path,
			}
		}
	}
	if r.req.Agent == model.AgentNameHermes {
		r.applyHermesSkillOrigins()
	}
}

func (r *configurationReader) applyHermesSkillOrigins() {
	home := r.envPath("HERMES_HOME", filepath.Join(r.home, ".hermes"))
	base := filepath.Join(home, "skills")
	document, source, ok := r.readDocument(
		filepath.Join(base, ".hub", "lock.json"),
		model.ConfigurationScopeProfile,
	)
	if !ok {
		return
	}
	installed := configurationMap(document["installed"])
	for i := range r.result.Skills {
		skill := &r.result.Skills[i]
		entry := configurationMap(installed[skill.Name])
		path := configurationString(entry["install_path"])
		if path == "" || r.expandPath(path, base) != filepath.Clean(skill.Directory) {
			continue
		}
		metadata := configurationMap(entry["metadata"])
		provenance := configurationMap(entry["scan_provenance"])
		url := configurationString(metadata["source_url"])
		if url == "" {
			url = configurationString(provenance["source_url"])
		}
		skill.Origin = model.ConfigurationOrigin{
			Kind:     "registry",
			URL:      url,
			Package:  configurationString(entry["identifier"]),
			Ref:      configurationString(metadata["source_revision"]),
			Evidence: source.Path,
		}
	}
}
