package facade_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type SetupFacadeSuite struct {
	suite.Suite
	ctx  context.Context
	home string
}

func TestSetupFacadeSuite(t *testing.T) {
	suite.Run(t, new(SetupFacadeSuite))
}

func (s *SetupFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	s.home = s.T().TempDir()
	s.T().Setenv("HOME", s.home)
}

func (s *SetupFacadeSuite) TestInstallSetsUpAllSupportedAgentHooks() {
	s.T().Setenv("CODEX_HOME", filepath.Join(s.home, ".codex"))
	s.T().Setenv("CLAUDE_HOME", filepath.Join(s.home, ".claude"))
	s.T().Setenv("HERMES_HOME", filepath.Join(s.home, ".hermes"))
	s.T().Setenv("PI_HOME", filepath.Join(s.home, ".pi"))
	s.T().Setenv("PI_CODING_AGENT_DIR", filepath.Join(s.home, ".pi", "agent"))
	s.T().Setenv("KIRO_HOME", filepath.Join(s.home, ".kiro"))
	s.T().Setenv("GEMINI_HOME", filepath.Join(s.home, ".gemini"))
	s.T().Setenv("OPENCLAW_HOME", filepath.Join(s.home, ".openclaw"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.home, ".claude"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.home, ".claude", "settings.json"),
		[]byte(`{"theme":"dark"}`),
		0o600,
	))

	resp, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 7)
	s.Equal(model.AgentNameCodex, resp.Adapters[0].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[0].Status)
	s.Equal(model.AgentNameClaude, resp.Adapters[1].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[1].Status)
	for _, result := range resp.Adapters[2:] {
		s.Equal(facade.SetupStatusInstalled, result.Status)
	}

	s.FileExists(filepath.Join(s.home, ".pax", "paxl", "hooks", "agent-hook"))
	s.FileExists(filepath.Join(s.home, ".codex", "paxl", "hooks", "user-prompt.json"))
	s.codexConfigContains("UserPromptSubmit")
	s.codexConfigContains(`type = "command"`)
	s.codexConfigContains("async = false")
	s.codexConfigContains("paxl --db ")
	s.codexConfigContains("__agent-hook --agent codex --event user-prompt")
	s.FileExists(filepath.Join(s.home, ".hermes", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".pi", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".pi", "agent", "extensions", "paxl-hook", "index.ts"))
	s.FileExists(filepath.Join(s.home, ".kiro", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".gemini", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".openclaw", "paxl", "hooks", "user-prompt.json"))
	s.claudeHookCommandContains("paxl __agent-hook --agent claude --event user-prompt")
}

func (s *SetupFacadeSuite) TestInstallPiHookWritesBeforeAgentStartExtension() {
	s.T().Setenv("PI_HOME", filepath.Join(s.home, ".pi"))
	s.T().Setenv("PI_CODING_AGENT_DIR", filepath.Join(s.home, ".pi", "agent"))
	s.T().Setenv("XDG_DATA_HOME", filepath.Join(s.home, ".data"))

	resp, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{
		Agents:      []model.AgentName{model.AgentNamePi},
		PaxlCommand: "/opt/paxl test/bin/paxl",
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 1)
	s.Equal(model.AgentNamePi, resp.Adapters[0].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[0].Status)
	extensionPath := filepath.Join(
		s.home,
		".pi",
		"agent",
		"extensions",
		"paxl-hook",
		"index.ts",
	)
	s.Equal(extensionPath, resp.Adapters[0].Path)
	raw, err := os.ReadFile(extensionPath)
	s.Require().NoError(err)
	extension := string(raw)
	s.Contains(extension, `pi.on("before_agent_start"`)
	s.Contains(extension, `spawnSync(paxlCommand`)
	s.Contains(extension, `timestamped = fileName.match`)
	s.Contains(extension, `"__agent-hook"`)
	s.Contains(extension, `"--agent", "pi"`)
	s.Contains(extension, filepath.Join(s.home, ".data", "paxl", "paxl.sqlite"))
	s.Contains(extension, `/opt/paxl test/bin/paxl`)
	s.FileExists(filepath.Join(s.home, ".pi", "paxl", "hooks", "user-prompt.json"))
}

func (s *SetupFacadeSuite) TestInstallIsIdempotentForClaudeSettings() {
	s.T().Setenv("CLAUDE_HOME", filepath.Join(s.home, ".claude"))

	setup := facade.NewSetupFacade()
	_, err := setup.Install(s.ctx, &facade.SetupRequest{Agents: []model.AgentName{
		model.AgentNameClaude,
	}})
	s.Require().NoError(err)
	_, err = setup.Install(s.ctx, &facade.SetupRequest{Agents: []model.AgentName{
		model.AgentNameClaude,
	}})
	s.Require().NoError(err)

	settings := s.readClaudeSettings()
	hooks, ok := settings["hooks"].(map[string]any)
	s.Require().True(ok)
	groups, ok := hooks["UserPromptSubmit"].([]any)
	s.Require().True(ok)
	s.Len(groups, 1)
}

func (s *SetupFacadeSuite) TestInstallReplacesLegacyCodexHookShape() {
	s.T().Setenv("CODEX_HOME", filepath.Join(s.home, ".codex"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.home, ".codex"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.home, ".codex", "config.toml"),
		[]byte(
			"[hooks]\nuserPromptSubmit = [\"paxl __agent-hook --agent codex --event user-prompt\"]\n",
		),
		0o600,
	))

	_, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{Agents: []model.AgentName{
		model.AgentNameCodex,
	}})

	s.Require().NoError(err)
	raw, err := os.ReadFile(filepath.Join(s.home, ".codex", "config.toml"))
	s.Require().NoError(err)
	config := string(raw)
	s.Contains(config, "UserPromptSubmit")
	s.Contains(config, `type = "command"`)
	s.Contains(config, "async = false")
	s.NotContains(config, "userPromptSubmit")
}

func (s *SetupFacadeSuite) TestInstallSupportsDryRun() {
	s.T().Setenv("CODEX_HOME", filepath.Join(s.home, ".codex"))

	resp, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{
		Agents: []model.AgentName{model.AgentNameCodex},
		DryRun: true,
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 1)
	s.Equal(facade.SetupStatusPending, resp.Adapters[0].Status)
	s.NoFileExists(filepath.Join(s.home, ".codex", "paxl", "hooks", "user-prompt.json"))
}

func (s *SetupFacadeSuite) readClaudeSettings() map[string]any {
	raw, err := os.ReadFile(filepath.Join(s.home, ".claude", "settings.json"))
	s.Require().NoError(err)
	var settings map[string]any
	s.Require().NoError(json.Unmarshal(raw, &settings))
	return settings
}

func (s *SetupFacadeSuite) codexConfigContains(fragment string) {
	raw, err := os.ReadFile(filepath.Join(s.home, ".codex", "config.toml"))
	s.Require().NoError(err)
	s.Contains(string(raw), fragment)
}

func (s *SetupFacadeSuite) claudeHookCommandContains(fragment string) {
	settings := s.readClaudeSettings()
	hooks, ok := settings["hooks"].(map[string]any)
	s.Require().True(ok)
	groups, ok := hooks["UserPromptSubmit"].([]any)
	s.Require().True(ok)
	s.Require().Len(groups, 1)
	group, ok := groups[0].(map[string]any)
	s.Require().True(ok)
	handlers, ok := group["hooks"].([]any)
	s.Require().True(ok)
	s.Require().Len(handlers, 1)
	handler, ok := handlers[0].(map[string]any)
	s.Require().True(ok)
	command, ok := handler["command"].(string)
	s.Require().True(ok)
	s.Contains(command, fragment)
}
