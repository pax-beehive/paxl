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
	"gopkg.in/yaml.v3"
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
	s.T().Setenv("OPENCLAW_HOME", filepath.Join(s.home, ".openclaw"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.home, ".claude"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.home, ".claude", "settings.json"),
		[]byte(`{"theme":"dark"}`),
		0o600,
	))

	resp, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 6)
	s.Equal(model.AgentNameCodex, resp.Adapters[0].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[0].Status)
	s.Equal(model.AgentNameClaude, resp.Adapters[1].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[1].Status)
	for _, result := range resp.Adapters[2:] {
		s.Equal(facade.SetupStatusInstalled, result.Status)
	}

	s.FileExists(filepath.Join(s.home, ".pax", "paxl", "hooks", "agent-hook"))
	s.agentShimContains(model.AgentNameCodex, "PAXL_CALLER_AGENT=codex")
	s.agentShimContains(model.AgentNameCodex, "--caller-agent codex")
	s.agentShimContains(model.AgentNameClaude, "PAXL_CALLER_AGENT=claude")
	s.agentShimContains(model.AgentNameHermes, "PAXL_CALLER_AGENT=hermes")
	s.FileExists(filepath.Join(s.home, ".codex", "paxl", "hooks", "user-prompt.json"))
	s.codexConfigContains("UserPromptSubmit")
	s.codexConfigContains(`type = "command"`)
	s.codexConfigContains("async = false")
	s.codexConfigContains(
		filepath.Join(s.home, ".pax", "paxl", "shims", "codex", "paxl") + " --db ",
	)
	s.codexConfigContains("__agent-hook --agent codex --event user-prompt")
	s.hermesConfigHookContains("__agent-hook --agent hermes --event pre_llm_call")
	s.hermesConfigHookContains("__agent-env --agent hermes --event pre_tool_call")
	s.FileExists(filepath.Join(s.home, ".pi", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".pi", "agent", "extensions", "paxl-hook", "index.ts"))
	s.FileExists(filepath.Join(s.home, ".kiro", "paxl", "hooks", "user-prompt.json"))
	s.FileExists(filepath.Join(s.home, ".kiro", "agents", "paxl.json"))
	s.FileExists(filepath.Join(s.home, ".openclaw", "paxl", "hooks", "user-prompt.json"))
	s.claudeHookCommandContains("paxl __agent-hook --agent claude --event user-prompt")
}

func (s *SetupFacadeSuite) TestInstallHermesHookWritesPreLLMCallShellHook() {
	s.T().Setenv("HERMES_HOME", filepath.Join(s.home, ".hermes"))
	s.T().Setenv("XDG_DATA_HOME", filepath.Join(s.home, ".data"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.home, ".hermes"), 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.home, ".hermes", "config.yaml"),
		[]byte(
			"model:\n  provider: openrouter\nhooks:\n  post_llm_call:\n    - command: \"~/.hermes/agent-hooks/log.sh\"\n",
		),
		0o600,
	))

	resp, err := facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{
		Agents:      []model.AgentName{model.AgentNameHermes},
		PaxlCommand: "/opt/paxl test/bin/paxl",
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 1)
	s.Equal(model.AgentNameHermes, resp.Adapters[0].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[0].Status)
	s.Equal(filepath.Join(s.home, ".hermes", "config.yaml"), resp.Adapters[0].Path)
	s.agentShimContains(model.AgentNameHermes, `/opt/paxl test/bin/paxl`)
	s.hermesConfigHookContains(filepath.Join(s.home, ".pax", "paxl", "shims", "hermes", "paxl"))
	s.hermesConfigHookContains("--db")
	s.hermesConfigHookContains(filepath.Join(s.home, ".data", "paxl", "paxl.sqlite"))
	s.hermesConfigHookContains("__agent-hook --agent hermes --event pre_llm_call")
	s.hermesConfigHookContains("__agent-env --agent hermes --event pre_tool_call")
	s.hermesConfigHookContains("post_llm_call")

	_, err = facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{
		Agents:      []model.AgentName{model.AgentNameHermes},
		PaxlCommand: "/opt/paxl test/bin/paxl",
	})
	s.Require().NoError(err)
	s.assertHermesHookCount(1)

	_, err = facade.NewSetupFacade().Install(s.ctx, &facade.SetupRequest{
		Agents:      []model.AgentName{model.AgentNameHermes},
		PaxlCommand: "/tmp/new-paxl",
	})
	s.Require().NoError(err)
	s.assertHermesHookCount(1)
	s.agentShimContains(model.AgentNameHermes, "/tmp/new-paxl")
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
	s.Contains(extension, filepath.Join(s.home, ".pax", "paxl", "shims", "pi", "paxl"))
	s.agentShimContains(model.AgentNamePi, `/opt/paxl test/bin/paxl`)
	s.FileExists(filepath.Join(s.home, ".pi", "paxl", "hooks", "user-prompt.json"))
}

func (s *SetupFacadeSuite) TestInstallKiroHookWritesAgentConfig() {
	s.T().Setenv("KIRO_HOME", filepath.Join(s.home, ".kiro"))
	s.T().Setenv("XDG_DATA_HOME", filepath.Join(s.home, ".data"))

	setup := facade.NewSetupFacade()
	resp, err := setup.Install(s.ctx, &facade.SetupRequest{
		Agents:      []model.AgentName{model.AgentNameKiro},
		PaxlCommand: "/opt/paxl test/bin/paxl",
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Adapters, 1)
	s.Equal(model.AgentNameKiro, resp.Adapters[0].Agent)
	s.Equal(facade.SetupStatusInstalled, resp.Adapters[0].Status)
	agentPath := filepath.Join(s.home, ".kiro", "agents", "paxl.json")
	s.Equal(agentPath, resp.Adapters[0].Path)
	s.FileExists(filepath.Join(s.home, ".kiro", "paxl", "hooks", "user-prompt.json"))
	s.assertKiroAgentConfig(agentPath, 1)
	s.assertKiroDefaultAgent("paxl")

	_, err = setup.Install(s.ctx, &facade.SetupRequest{
		Agents:      []model.AgentName{model.AgentNameKiro},
		PaxlCommand: "/opt/paxl test/bin/paxl",
	})
	s.Require().NoError(err)
	s.assertKiroAgentConfig(agentPath, 1)
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

func (s *SetupFacadeSuite) hermesConfigHookContains(fragment string) {
	raw, err := os.ReadFile(filepath.Join(s.home, ".hermes", "config.yaml"))
	s.Require().NoError(err)
	s.Contains(string(raw), fragment)
}

func (s *SetupFacadeSuite) agentShimContains(agent model.AgentName, fragment string) {
	raw, err := os.ReadFile(filepath.Join(s.home, ".pax", "paxl", "shims", string(agent), "paxl"))
	s.Require().NoError(err)
	s.Contains(string(raw), fragment)
}

func (s *SetupFacadeSuite) assertHermesHookCount(want int) {
	raw, err := os.ReadFile(filepath.Join(s.home, ".hermes", "config.yaml"))
	s.Require().NoError(err)
	var config map[string]any
	s.Require().NoError(yaml.Unmarshal(raw, &config))
	hooks, ok := config["hooks"].(map[string]any)
	s.Require().True(ok)
	preLLMCall, ok := hooks["pre_llm_call"].([]any)
	s.Require().True(ok)
	s.Len(preLLMCall, want)
}

func (s *SetupFacadeSuite) assertKiroAgentConfig(path string, wantHookCount int) {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	var config map[string]any
	s.Require().NoError(json.Unmarshal(raw, &config))
	s.Equal("paxl", config["name"])
	hooks, ok := config["hooks"].(map[string]any)
	s.Require().True(ok)
	userPromptSubmit, ok := hooks["userPromptSubmit"].([]any)
	s.Require().True(ok)
	s.Require().Len(userPromptSubmit, wantHookCount)
	hook, ok := userPromptSubmit[0].(map[string]any)
	s.Require().True(ok)
	command, ok := hook["command"].(string)
	s.Require().True(ok)
	s.Contains(command, filepath.Join(s.home, ".pax", "paxl", "shims", "kiro", "paxl"))
	s.agentShimContains(model.AgentNameKiro, `/opt/paxl test/bin/paxl`)
	s.Contains(command, "--db")
	s.Contains(command, filepath.Join(s.home, ".data", "paxl", "paxl.sqlite"))
	s.Contains(command, "__agent-hook --agent kiro --event user-prompt")
}

func (s *SetupFacadeSuite) assertKiroDefaultAgent(agentName string) {
	raw, err := os.ReadFile(filepath.Join(s.home, ".kiro", "settings", "cli.json"))
	s.Require().NoError(err)
	var settings map[string]any
	s.Require().NoError(json.Unmarshal(raw, &settings))
	s.Equal(agentName, settings["chat.defaultAgent"])
}
