package adaptor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func (r *configurationReader) extraSkillRoots() []configurationSkillRoot {
	switch r.req.Agent {
	case model.AgentNameCodex:
		return r.codexConfiguredSkillRoots()
	case model.AgentNameClaude:
		return r.claudePluginSkillRoots()
	case model.AgentNamePi:
		return r.piConfiguredSkillRoots()
	case model.AgentNameKiro:
		return r.kiroConfiguredSkillRoots()
	case model.AgentNameKimi:
		return r.kimiConfiguredSkillRoots()
	case model.AgentNameHermes:
		return r.hermesConfiguredSkillRoots()
	case model.AgentNameOpenClaw:
		return r.openClawConfiguredSkillRoots()
	case model.AgentNameDSH, model.AgentNameDSCode:
		return r.dshConfiguredSkillRoots()
	case model.AgentNameUnknown,
		model.AgentNameGemini,
		model.AgentNamePaxl,
		model.AgentNameAntigravity,
		model.AgentNameOpenCode:
		return nil
	default:
		return nil
	}
}

func (r *configurationReader) skillPathRoots(
	values []string,
	base string,
	scope model.ConfigurationScope,
) []configurationSkillRoot {
	var roots []configurationSkillRoot
	for _, value := range values {
		if value == "" || strings.HasPrefix(value, "!") || strings.HasPrefix(value, "-") {
			continue
		}
		path := r.expandPath(strings.TrimPrefix(value, "+"), base)
		matches, err := filepath.Glob(path)
		if err != nil {
			r.addDiagnostic(
				"skill_path_pattern_invalid",
				"A configured skill path pattern is invalid.",
				&model.ConfigurationSource{Path: path, Scope: scope},
			)
			continue
		}
		if len(matches) == 0 {
			matches = []string{path}
		}
		for _, match := range matches {
			roots = append(roots, r.flatSkillRoot(match, scope))
		}
	}
	return roots
}

func (r *configurationReader) codexConfiguredSkillRoots() []configurationSkillRoot {
	var roots []configurationSkillRoot
	for _, item := range r.skillSettingsDocuments() {
		entries, _ := configurationMap(item.document["skills"])["config"].([]any)
		for _, raw := range entries {
			path := configurationString(configurationMap(raw)["path"])
			roots = append(
				roots,
				r.skillPathRoots(
					[]string{path},
					filepath.Dir(item.source.Path),
					item.source.Scope,
				)...)
		}
	}
	return roots
}

func (r *configurationReader) kimiConfiguredSkillRoots() []configurationSkillRoot {
	var roots []configurationSkillRoot
	for _, item := range r.skillSettingsDocuments() {
		roots = append(
			roots,
			r.skillPathRoots(
				configurationStrings(item.document["extra_skill_dirs"]),
				r.root,
				item.source.Scope,
			)...)
	}
	return append(roots, r.kimiPluginSkillRoots()...)
}

func (r *configurationReader) hermesConfiguredSkillRoots() []configurationSkillRoot {
	documents := r.skillSettingsDocuments()
	roots := make([]configurationSkillRoot, 0, len(documents))
	for _, item := range documents {
		skills := configurationMap(item.document["skills"])
		paths := append(
			configurationStrings(skills["external_dirs"]),
			configurationString(skills["create_dir"]),
		)
		base := r.envPath("HERMES_HOME", filepath.Join(r.home, ".hermes"))
		roots = append(roots, r.skillPathRoots(paths, base, model.ConfigurationScopeProfile)...)
	}
	return roots
}

func (r *configurationReader) piConfiguredSkillRoots() []configurationSkillRoot {
	var roots []configurationSkillRoot
	for _, item := range r.skillSettingsDocuments() {
		roots = append(
			roots,
			r.skillPathRoots(
				configurationStrings(item.document["skills"]),
				filepath.Dir(item.source.Path),
				item.source.Scope,
			)...)
		packages, _ := item.document["packages"].([]any)
		for _, entry := range packages {
			roots = append(roots, r.localPackageSkillRoots(entry, &item.source)...)
		}
	}
	return roots
}

func (r *configurationReader) localPackageSkillRoots(
	raw any,
	source *model.ConfigurationSource,
) []configurationSkillRoot {
	declaration := configurationMap(raw)
	path := configurationString(raw)
	if path == "" {
		path = configurationString(declaration["source"])
	}
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "npm:") || strings.HasPrefix(path, "git:") ||
		strings.Contains(path, "://") {
		r.addDiagnostic(
			"skill_package_unresolved",
			"A remote skill package declaration was retained as a source but not resolved or downloaded.",
			source,
		)
		return nil
	}
	path = r.expandPath(path, filepath.Dir(source.Path))
	root := r.flatSkillRoot(filepath.Join(path, "skills"), model.ConfigurationScopePackage)
	root.owner = path
	root.origin = model.ConfigurationOrigin{Kind: "package", Package: path, Evidence: source.Path}
	roots := []configurationSkillRoot{root}
	manifest, manifestSource, ok := r.readDocument(
		filepath.Join(path, "package.json"),
		model.ConfigurationScopePackage,
	)
	if ok {
		for _, configured := range configurationStrings(configurationMap(manifest["pi"])["skills"]) {
			for _, extra := range r.skillPathRoots([]string{configured}, path, model.ConfigurationScopePackage) {
				extra.owner = path
				extra.origin = model.ConfigurationOrigin{
					Kind:     "package",
					Package:  configurationString(manifest["name"]),
					Version:  configurationString(manifest["version"]),
					Evidence: manifestSource.Path,
				}
				roots = append(roots, extra)
			}
		}
	}
	return roots
}

func (r *configurationReader) kiroConfiguredSkillRoots() []configurationSkillRoot {
	home := r.envPath("KIRO_HOME", filepath.Join(r.home, ".kiro"))
	var roots []configurationSkillRoot
	for _, base := range []string{filepath.Join(home, "agents"), filepath.Join(r.root, ".kiro", "agents")} {
		files, _ := filepath.Glob(filepath.Join(base, "*.json"))
		for _, path := range files {
			document, source, ok := r.readDocument(path, model.ConfigurationScopeAgent)
			if !ok {
				continue
			}
			for _, resource := range configurationStrings(document["resources"]) {
				if strings.HasPrefix(resource, "skill://") {
					found := r.skillPathRoots(
						[]string{strings.TrimPrefix(resource, "skill://")},
						r.cwd,
						model.ConfigurationScopeAgent,
					)
					for i := range found {
						found[i].owner = strings.TrimSuffix(
							filepath.Base(source.Path),
							filepath.Ext(source.Path),
						)
					}
					roots = append(roots, found...)
				}
			}
		}
	}
	return roots
}

func (r *configurationReader) openClawConfiguredSkillRoots() []configurationSkillRoot {
	var roots []configurationSkillRoot
	for _, item := range r.skillSettingsDocuments() {
		skills := configurationMap(item.document["skills"])
		roots = append(
			roots,
			r.skillPathRoots(
				configurationStrings(configurationMap(skills["load"])["extraDirs"]),
				r.home,
				model.ConfigurationScopeProfile,
			)...)
		agents := configurationMap(item.document["agents"])
		workspace := configurationString(configurationMap(agents["defaults"])["workspace"])
		if workspace != "" {
			roots = append(roots, r.openClawWorkspaceSkillRoots(workspace, "default")...)
		}
		for name, raw := range configurationMap(agents["entries"]) {
			if workspace := configurationString(
				configurationMap(raw)["workspace"],
			); workspace != "" {
				roots = append(roots, r.openClawWorkspaceSkillRoots(workspace, name)...)
			}
		}
		entries, _ := agents["list"].([]any)
		for _, raw := range entries {
			entry := configurationMap(raw)
			if workspace := configurationString(entry["workspace"]); workspace != "" {
				roots = append(
					roots,
					r.openClawWorkspaceSkillRoots(workspace, configurationString(entry["id"]))...)
			}
		}
	}
	return roots
}

func (r *configurationReader) openClawWorkspaceSkillRoots(
	workspace, owner string,
) []configurationSkillRoot {
	path := r.expandPath(workspace, r.home)
	roots := []configurationSkillRoot{
		r.skillRoot(filepath.Join(path, "skills"), model.ConfigurationScopeAgent),
		r.skillRoot(filepath.Join(path, ".agents", "skills"), model.ConfigurationScopeAgent),
	}
	for i := range roots {
		roots[i].owner = owner
	}
	return roots
}

func (r *configurationReader) dshConfiguredSkillRoots() []configurationSkillRoot {
	var roots []configurationSkillRoot
	for _, item := range r.skillSettingsDocuments() {
		configurationVisitSkillMaps(item.document, func(document map[string]any) {
			paths := append(
				configurationStrings(document["customSkillDirs"]),
				configurationString(document["bundledSkillDir"]),
			)
			roots = append(roots, r.skillPathRoots(paths, r.root, item.source.Scope)...)
		})
	}
	if path := r.env["DSH_BUNDLED_SKILL_DIR"]; path != "" && r.req.Agent == model.AgentNameDSH {
		roots = append(
			roots,
			r.skillPathRoots([]string{path}, r.cwd, model.ConfigurationScopeSystem)...)
	}
	if r.req.Agent == model.AgentNameDSCode &&
		!configurationSkillFalse(r.env["DSCODE_SKILL_ANCESTORS"]) {
		roots = append(roots, r.dscodeAncestorSkillRoots()...)
	}
	return roots
}

func configurationVisitSkillMaps(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, nested := range typed {
			configurationVisitSkillMaps(nested, visit)
		}
	case []any:
		for _, nested := range typed {
			configurationVisitSkillMaps(nested, visit)
		}
	}
}

func (r *configurationReader) dscodeAncestorSkillRoots() []configurationSkillRoot {
	relative, err := filepath.Rel(r.home, r.cwd)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil
	}
	var roots []configurationSkillRoot
	for dir := r.cwd; ; dir = filepath.Dir(dir) {
		for _, name := range []string{".dsh", ".agents", ".claude"} {
			if dir == r.home && name == ".dsh" {
				continue
			}
			roots = append(
				roots,
				r.flatSkillRoot(
					filepath.Join(dir, name, "skills"),
					model.ConfigurationScopeProject,
				),
			)
		}
		if dir == r.home || dir == filepath.Dir(dir) {
			return roots
		}
	}
}

func configurationSkillFalse(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "off", "no", "none", "disable":
		return true
	default:
		return false
	}
}
