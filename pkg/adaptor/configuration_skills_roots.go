package adaptor

import (
	"path/filepath"

	"github.com/pax-oss/paxl/internal/model"
)

func (r *configurationReader) skillRoots() []configurationSkillRoot {
	switch r.req.Agent {
	case model.AgentNameCodex:
		roots := r.projectSkillRoots(".agents", "skills")
		return append(
			roots,
			r.skillRoot(filepath.Join(r.home, ".agents", "skills"), model.ConfigurationScopeUser),
			r.skillRoot(
				filepath.Join(r.envPath("CODEX_HOME", filepath.Join(r.home, ".codex")), "skills"),
				model.ConfigurationScopeUser,
			),
		)
	case model.AgentNameClaude:
		roots := r.projectSkillRoots(".claude", "skills")
		commands := r.projectSkillRoots(".claude", "commands")
		for i := range commands {
			commands[i].flat = true
		}
		roots = append(roots, commands...)
		home := r.envPath(
			"CLAUDE_CONFIG_DIR",
			r.envPath("CLAUDE_HOME", filepath.Join(r.home, ".claude")),
		)
		return append(
			roots,
			r.skillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeUser),
			r.flatSkillRoot(filepath.Join(home, "commands"), model.ConfigurationScopeUser),
		)
	case model.AgentNamePi:
		return r.piSkillRoots()
	case model.AgentNameKiro:
		return append(
			r.projectSkillRoots(".kiro", "skills"),
			r.skillRoot(
				filepath.Join(r.envPath("KIRO_HOME", filepath.Join(r.home, ".kiro")), "skills"),
				model.ConfigurationScopeUser,
			),
		)
	case model.AgentNameOpenCode:
		return r.openCodeSkillRoots()
	case model.AgentNameKimi:
		return r.kimiSkillRoots()
	case model.AgentNameHermes:
		return r.hermesSkillRoots()
	case model.AgentNameOpenClaw:
		return r.openClawSkillRoots()
	case model.AgentNameDSH:
		return r.dshSkillRoots()
	case model.AgentNameDSCode:
		return r.dscodeSkillRoots()
	case model.AgentNameUnknown,
		model.AgentNameGemini,
		model.AgentNamePaxl,
		model.AgentNameAntigravity:
		return nil
	default:
		return nil
	}
}

func (r *configurationReader) skillRoot(
	path string,
	scope model.ConfigurationScope,
) configurationSkillRoot {
	return configurationSkillRoot{path: path, scope: scope}
}

func (r *configurationReader) flatSkillRoot(
	path string,
	scope model.ConfigurationScope,
) configurationSkillRoot {
	root := r.skillRoot(path, scope)
	root.flat = true
	return root
}

func (r *configurationReader) projectSkillRoots(parts ...string) []configurationSkillRoot {
	dirs := r.projectDirs()
	roots := make([]configurationSkillRoot, 0, len(dirs))
	for _, dir := range dirs {
		path := filepath.Join(append([]string{dir}, parts...)...)
		roots = append(roots, r.skillRoot(path, model.ConfigurationScopeProject))
	}
	return roots
}

func (r *configurationReader) piSkillRoots() []configurationSkillRoot {
	home := r.envPath("PI_CODING_AGENT_DIR", filepath.Join(r.home, ".pi", "agent"))
	roots := append(
		r.projectSkillRoots(".pi", "skills"),
		r.projectSkillRoots(".agents", "skills")...)
	roots = append(
		roots,
		r.flatSkillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeUser),
		r.skillRoot(filepath.Join(r.home, ".agents", "skills"), model.ConfigurationScopeUser),
	)
	return roots
}

func (r *configurationReader) openCodeSkillRoots() []configurationSkillRoot {
	xdg := r.envPath("XDG_CONFIG_HOME", filepath.Join(r.home, ".config"))
	home := filepath.Join(xdg, "opencode")
	roots := append(
		r.projectSkillRoots(".opencode", "skills"),
		r.skillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeUser),
	)
	if extra := r.env["OPENCODE_CONFIG_DIR"]; extra != "" {
		roots = append(
			roots,
			r.skillRoot(
				filepath.Join(r.expandPath(extra, r.cwd), "skills"),
				model.ConfigurationScopeUser,
			),
		)
	}
	if r.env["OPENCODE_DISABLE_EXTERNAL_SKILLS"] != "1" &&
		r.env["OPENCODE_DISABLE_EXTERNAL_SKILLS"] != "true" {
		roots = append(roots, r.projectSkillRoots(".agents", "skills")...)
		roots = append(
			roots,
			r.skillRoot(filepath.Join(r.home, ".agents", "skills"), model.ConfigurationScopeUser),
		)
		if r.env["OPENCODE_DISABLE_CLAUDE_CODE_SKILLS"] != "1" &&
			r.env["OPENCODE_DISABLE_CLAUDE_CODE_SKILLS"] != "true" {
			roots = append(roots, r.projectSkillRoots(".claude", "skills")...)
			roots = append(
				roots,
				r.skillRoot(
					filepath.Join(r.home, ".claude", "skills"),
					model.ConfigurationScopeUser,
				),
			)
		}
	}
	return roots
}

func (r *configurationReader) kimiSkillRoots() []configurationSkillRoot {
	home := r.envPath("KIMI_CODE_HOME", filepath.Join(r.home, ".kimi-code"))
	legacy := r.flatSkillRoot(
		filepath.Join(r.home, ".kimi", "skills"),
		model.ConfigurationScopeUser,
	)
	legacy.owner = "legacy"
	roots := []configurationSkillRoot{
		r.flatSkillRoot(
			filepath.Join(r.root, ".kimi-code", "skills"),
			model.ConfigurationScopeProject,
		),
		r.flatSkillRoot(
			filepath.Join(r.root, ".agents", "skills"),
			model.ConfigurationScopeProject,
		),
		r.flatSkillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeUser),
		r.flatSkillRoot(filepath.Join(r.home, ".agents", "skills"), model.ConfigurationScopeUser),
		legacy,
	}
	return roots
}

func (r *configurationReader) hermesSkillRoots() []configurationSkillRoot {
	home := r.envPath("HERMES_HOME", filepath.Join(r.home, ".hermes"))
	return []configurationSkillRoot{
		r.skillRoot(filepath.Join(r.root, ".hermes", "skills"), model.ConfigurationScopeProject),
		r.skillRoot(filepath.Join(r.root, ".agents", "skills"), model.ConfigurationScopeProject),
		r.skillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeProfile),
	}
}

func (r *configurationReader) openClawSkillRoots() []configurationSkillRoot {
	state, _ := r.skillOpenClawPaths()
	return []configurationSkillRoot{
		r.skillRoot(filepath.Join(r.cwd, "skills"), model.ConfigurationScopeProject),
		r.skillRoot(filepath.Join(r.cwd, ".agents", "skills"), model.ConfigurationScopeProject),
		r.skillRoot(filepath.Join(r.home, ".agents", "skills"), model.ConfigurationScopeUser),
		r.skillRoot(filepath.Join(state, "skills"), model.ConfigurationScopeProfile),
	}
}

func (r *configurationReader) dshSkillRoots() []configurationSkillRoot {
	home := r.envPath("DSH_HOME", filepath.Join(r.home, ".dsh"))
	agents := r.envPath("DSH_AGENTS_HOME", filepath.Join(r.home, ".agents"))
	return []configurationSkillRoot{
		r.flatSkillRoot(filepath.Join(r.root, ".dsh", "skills"), model.ConfigurationScopeProject),
		r.flatSkillRoot(
			filepath.Join(r.root, ".agents", "skills"),
			model.ConfigurationScopeProject,
		),
		r.flatSkillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeUser),
		r.flatSkillRoot(filepath.Join(agents, "skills"), model.ConfigurationScopeUser),
	}
}

func (r *configurationReader) dscodeSkillRoots() []configurationSkillRoot {
	home := r.envPath("DSCODE_HOME", filepath.Join(r.home, ".local", "share", "dscode-state"))
	return []configurationSkillRoot{
		r.flatSkillRoot(filepath.Join(r.root, ".dsh", "skills"), model.ConfigurationScopeProject),
		r.flatSkillRoot(
			filepath.Join(r.root, ".agents", "skills"),
			model.ConfigurationScopeProject,
		),
		r.flatSkillRoot(filepath.Join(home, "skills"), model.ConfigurationScopeProfile),
		r.flatSkillRoot(filepath.Join(home, "agents", "skills"), model.ConfigurationScopeProfile),
	}
}
