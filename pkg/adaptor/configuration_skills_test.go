package adaptor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

func TestReadConfigurationSkillsKeepsFileProvenanceWithoutInventingAnOrigin(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	require.NoError(t, os.MkdirAll(cwd, 0o700))
	path := filepath.Join(home, ".claude", "skills", "review", "SKILL.md")
	configurationSkillsWrite(
		t,
		path,
		"---\nname: review\ndescription: Review changes\ndisable-model-invocation: true\n---\nRead the diff carefully.\n",
	)

	result, err := adaptor.ReadConfiguration(
		context.Background(),
		&adaptor.ReadConfigurationRequest{
			Agent: model.AgentNameClaude, CWD: cwd, Home: home,
			Environment: map[string]string{}, Kind: model.AgentConfigurationKindSkills,
		},
	)

	require.NoError(t, err)
	require.Len(t, result.Skills, 1)
	skill := result.Skills[0]
	require.Equal(t, "review", skill.Name)
	require.Equal(t, path, skill.Source.Path)
	require.Equal(t, model.ConfigurationScopeUser, skill.Source.Scope)
	require.Equal(t, "unknown", skill.Origin.Kind)
	require.Empty(t, skill.Origin.URL)
	require.Len(t, skill.Source.SHA256, 64)
	require.NotNil(t, skill.ModelInvocable)
	require.False(t, *skill.ModelInvocable)
	require.Empty(t, skill.Instructions)
}

func configurationSkillsWrite(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestReadConfigurationSkillsDiscoversEachNativeUserDirectory(t *testing.T) {
	cases := []struct {
		agent model.AgentName
		root  string
	}{
		{model.AgentNameCodex, ".agents/skills"},
		{model.AgentNameClaude, ".claude/skills"},
		{model.AgentNamePi, ".pi/agent/skills"},
		{model.AgentNameKiro, ".kiro/skills"},
		{model.AgentNameOpenCode, ".config/opencode/skills"},
		{model.AgentNameKimi, ".kimi-code/skills"},
		{model.AgentNameHermes, ".hermes/skills"},
		{model.AgentNameOpenClaw, ".openclaw/skills"},
		{model.AgentNameDSH, ".dsh/skills"},
		{model.AgentNameDSCode, ".local/share/dscode-state/skills"},
	}
	for _, tc := range cases {
		t.Run(string(tc.agent), func(t *testing.T) {
			home := t.TempDir()
			configurationSkillsWrite(
				t,
				filepath.Join(home, filepath.FromSlash(tc.root), "review", "SKILL.md"),
				"---\nname: review\ndescription: Check a change\n---\nKeep supporting files.\n",
			)
			result := configurationReadSkills(t, home, tc.agent, nil, true)
			require.Len(t, result.Skills, 1)
			require.Equal(t, "review", result.Skills[0].Name)
			require.Equal(t, "Keep supporting files.", result.Skills[0].Instructions)
			require.Equal(t, "partial", result.Coverage[model.AgentConfigurationKindSkills].Status)
			require.False(t, result.RuntimeVerified)
		})
	}
}

func TestReadConfigurationSkillsUsesNativeCodexPolicyAndRetainsDuplicates(t *testing.T) {
	home := t.TempDir()
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".agents", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: User review\n---\nUser rules.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, "project", ".agents", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: Project review\n---\nProject rules.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".agents", "skills", "review", "agents", "openai.yaml"),
		"policy:\n  allow_implicit_invocation: false\n",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".codex", "config.toml"),
		"[[skills.config]]\nname = 'review'\nenabled = false\n",
	)
	result := configurationReadSkills(t, home, model.AgentNameCodex, nil, false)
	require.Len(t, result.Skills, 2)
	for _, skill := range result.Skills {
		require.NotNil(t, skill.Enabled)
		require.False(t, *skill.Enabled)
		if skill.Source.Scope == model.ConfigurationScopeUser {
			require.NotNil(t, skill.ModelInvocable)
			require.False(t, *skill.ModelInvocable)
		}
	}
}

func TestReadConfigurationSkillsReadsHermesExternalDirectoriesAndRecordedOrigin(t *testing.T) {
	home := t.TempDir()
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".hermes", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: Review\n---\nReview.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, "team-skills", "team", "SKILL.md"),
		"---\nname: team\ndescription: Team process\n---\nTeam.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".hermes", "config.yaml"),
		"skills:\n  disabled: [review]\n  external_dirs: [~/team-skills]\n",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".hermes", "skills", ".hub", "lock.json"),
		`{"version":1,"installed":{"review":{"source":"github","identifier":"owner/repo/review","install_path":"review","metadata":{"source_url":"https://github.com/owner/repo","source_revision":"abc123"}}}}`,
	)
	result := configurationReadSkills(t, home, model.AgentNameHermes, nil, false)
	require.Len(t, result.Skills, 2)
	for _, skill := range result.Skills {
		if skill.Name == "review" {
			require.NotNil(t, skill.Enabled)
			require.False(t, *skill.Enabled)
			require.Equal(t, "https://github.com/owner/repo", skill.Origin.URL)
			require.Equal(t, "abc123", skill.Origin.Ref)
			require.True(t, strings.HasSuffix(skill.Origin.Evidence, "lock.json"))
		}
	}
}

func TestReadConfigurationSkillsDoesNotScanOpenCodeCompatibilityRootsWhenDisabled(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{".agents/skills/shared", ".claude/skills/claude", ".config/opencode/skills/native", "custom/skills/custom"} {
		name := filepath.Base(path)
		configurationSkillsWrite(
			t,
			filepath.Join(home, filepath.FromSlash(path), "SKILL.md"),
			"---\nname: "+name+"\ndescription: Review\n---\nReview.",
		)
	}
	result := configurationReadSkills(
		t,
		home,
		model.AgentNameOpenCode,
		map[string]string{
			"OPENCODE_DISABLE_EXTERNAL_SKILLS": "true",
			"OPENCODE_CONFIG_DIR":              filepath.Join(home, "custom"),
		},
		false,
	)
	require.Len(t, result.Skills, 2)
	names := []string{result.Skills[0].Name, result.Skills[1].Name}
	require.ElementsMatch(t, []string{"native", "custom"}, names)
}

func TestReadConfigurationSkillsRestrictsDSHDiscoveryToNativeDepth(t *testing.T) {
	home := t.TempDir()
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".dsh", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: Review\n---\nReview.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".dsh", "skills", "category", "hidden", "SKILL.md"),
		"---\nname: hidden\ndescription: Not a native candidate\n---\nHidden.",
	)
	result := configurationReadSkills(t, home, model.AgentNameDSH, nil, false)
	require.Len(t, result.Skills, 1)
	require.Equal(t, "review", result.Skills[0].Name)
}

func TestReadConfigurationSkillsKeepsClaudePluginOwnershipAndLegacyCommands(t *testing.T) {
	home := t.TempDir()
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".claude", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: Personal review\n---\nReview.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, "project", ".claude", "commands", "deploy.md"),
		"Deploy this project.",
	)
	plugin := filepath.Join(home, "bundle")
	configurationSkillsWrite(
		t,
		filepath.Join(plugin, "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: Plugin review\n---\nReview.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"plugins":{"review@market":[{"installPath":"`+filepath.ToSlash(
			plugin,
		)+`","version":"1.2.3"}]}}`,
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".claude", "settings.json"),
		`{"skillOverrides":{"review":"off","deploy":"name-only"},"enabledPlugins":{"review@market":true}}`,
	)
	result := configurationReadSkills(t, home, model.AgentNameClaude, nil, false)
	require.Len(t, result.Skills, 3)
	for _, skill := range result.Skills {
		require.NotNil(t, skill.Enabled)
		switch {
		case skill.Source.Scope == model.ConfigurationScopePlugin:
			require.True(t, *skill.Enabled)
			require.Equal(t, "review@market", skill.Source.Owner)
			require.Equal(t, "1.2.3", skill.Origin.Version)
		case skill.Name == "deploy":
			require.True(t, *skill.Enabled)
			require.NotNil(t, skill.UserInvocable)
			require.True(t, *skill.UserInvocable)
		default:
			require.False(t, *skill.Enabled)
		}
	}
}

func TestReadConfigurationSkillsLoadsKiroResourcesAndOpenClawProfileWorkspace(t *testing.T) {
	home := t.TempDir()
	configurationSkillsWrite(
		t,
		filepath.Join(home, "team", "review", "SKILL.md"),
		"---\nname: review\ndescription: Team review\n---\nReview.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".kiro", "agents", "reviewer.json"),
		`{"resources":["skill://~/team/*"]}`,
	)
	kiro := configurationReadSkills(t, home, model.AgentNameKiro, nil, false)
	require.Len(t, kiro.Skills, 1)
	require.Equal(t, "reviewer", kiro.Skills[0].Source.Owner)
	configurationSkillsWrite(
		t,
		filepath.Join(home, "workspace", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: Workspace review\n---\nReview.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(home, ".openclaw-work", "openclaw.json"),
		`{"agents":{"defaults":{"workspace":"~/workspace"}},"skills":{"entries":{"review":{"enabled":false}}}}`,
	)
	openclaw := configurationReadSkills(
		t,
		home,
		model.AgentNameOpenClaw,
		map[string]string{"OPENCLAW_PROFILE": "work"},
		false,
	)
	require.Len(t, openclaw.Skills, 1)
	require.Equal(t, model.ConfigurationScopeAgent, openclaw.Skills[0].Source.Scope)
	require.NotNil(t, openclaw.Skills[0].Enabled)
	require.False(t, *openclaw.Skills[0].Enabled)
}

func TestReadConfigurationSkillsReportsInvalidFrontmatterAndStopsSymlinkCycles(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".agents", "skills")
	configurationSkillsWrite(
		t,
		filepath.Join(base, "bad", "SKILL.md"),
		"---\nname: incomplete\ndescription: Never closed",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(base, "invalid", "SKILL.md"),
		"---\nname: invalid\ndescription: Invalid JSON metadata\nmetadata: {limit: .nan}\n---\nBody.",
	)
	configurationSkillsWrite(
		t,
		filepath.Join(base, "valid", "SKILL.md"),
		"---\nname: valid\ndescription: Valid skill\n---\nBody.",
	)
	if err := os.Symlink(base, filepath.Join(base, "cycle")); err != nil {
		t.Log("Symlinks unavailable; continuing with malformed-file coverage")
	}
	result := configurationReadSkills(t, home, model.AgentNameCodex, nil, true)
	require.Len(t, result.Skills, 1)
	require.Equal(t, "valid", result.Skills[0].Name)
	require.NotEmpty(t, result.Diagnostics)
}

func configurationReadSkills(
	t *testing.T,
	home string,
	agent model.AgentName,
	environment map[string]string,
	content bool,
) *model.AgentConfiguration {
	t.Helper()
	cwd := filepath.Join(home, "project")
	require.NoError(t, os.MkdirAll(cwd, 0o700))
	if environment == nil {
		environment = map[string]string{}
	}
	result, err := adaptor.ReadConfiguration(
		context.Background(),
		&adaptor.ReadConfigurationRequest{
			Agent:          agent,
			CWD:            cwd,
			Home:           home,
			Environment:    environment,
			Kind:           model.AgentConfigurationKindSkills,
			IncludeContent: content,
		},
	)
	require.NoError(t, err)
	return result
}
