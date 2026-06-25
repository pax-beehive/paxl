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

func (s *SetupFacadeSuite) TestInstallSetsUpCodexClaudeAndHermesHooks() {
	s.T().Setenv("CODEX_HOME", filepath.Join(s.home, ".codex"))
	s.T().Setenv("CLAUDE_HOME", filepath.Join(s.home, ".claude"))
	s.T().Setenv("HERMES_HOME", filepath.Join(s.home, ".hermes"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.home, ".claude"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.home, ".claude", "settings.json"),
		[]byte(`{"theme":"dark"}`),
		0o600,
	))

	resp, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{
		Agents: []model.AgentName{
			model.AgentNameCodex,
			model.AgentNameClaude,
			model.AgentNameHermes,
		},
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 3)
	s.Equal(model.AgentNameCodex, resp.Adapters[0].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[0].Status)
	s.Equal(model.AgentNameClaude, resp.Adapters[1].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[1].Status)
	s.Equal(model.AgentNameHermes, resp.Adapters[2].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[2].Status)

	s.FileExists(filepath.Join(s.home, ".pax", "paxl", "hooks", "agent-hook"))
	s.FileExists(filepath.Join(s.home, ".codex", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".hermes", "paxl", "hooks", "user-prompt.json"))
	s.claudeHookCommandContains("paxl __agent-hook --agent claude --event user-prompt")
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
