package model_test

import (
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestLocalAuthHarnessAndProviderMethodBoundaries(t *testing.T) {
	for _, name := range []string{"claude", "claude-code", "codex", "kimi", "pi", "opencode", "kiro", "kiro-cli", "hermes", "openclaw"} {
		agent, err := model.ParseLocalAuthHarness(" " + strings.ToUpper(name) + " ")
		require.NoError(t, err)
		_, err = model.ParseHarnessAuthMethod("", agent)
		require.NoError(t, err)
	}
	_, err := model.ParseLocalAuthHarness("not-a-harness")
	require.Error(t, err)
	for _, harness := range []model.AgentName{model.AgentNameOpenClaw} {
		for _, method := range []string{"API Key", "ChatGPT Plus/Pro", "device-code"} {
			result, err := model.ParseHarnessAuthMethod(method, harness)
			require.NoError(t, err)
			require.Equal(t, model.HarnessAuthMethod(method), result)
		}
		for _, method := range []string{"-option", " trailing ", "line\nbreak", strings.Repeat("x", 129)} {
			_, err := model.ParseHarnessAuthMethod(method, harness)
			require.Error(t, err)
		}
	}
	for _, harness := range []model.AgentName{model.AgentNameClaude, model.AgentNameCodex, model.AgentNameKimi, model.AgentNamePi, model.AgentNameKiro, model.AgentNameHermes} {
		_, err := model.ParseHarnessAuthMethod("not-a-method", harness)
		require.ErrorContains(t, err, "supported methods")
	}
	_, err = model.ParseHarnessAuthMethod("API Key", model.AgentNameOpenCode)
	require.ErrorContains(t, err, "omit --method")
	_, err = model.ParseHarnessAuthMethod("oauth", model.AgentNameUnknown)
	require.Error(t, err)
}
