package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestAuthMethodsTextGroupsProviders(t *testing.T) {
	result := &model.HarnessAuthResult{
		Harness: model.AgentNamePi, State: model.HarnessAuthAvailable,
		Providers: []model.HarnessAuthProvider{
			{ID: "first", Methods: []model.HarnessAuthMethod{model.HarnessAuthAPIKey}},
			{ID: "second", Methods: []model.HarnessAuthMethod{model.HarnessAuthAPIKey}},
			{ID: "third", Methods: []model.HarnessAuthMethod{model.HarnessAuthOAuth}},
		},
	}
	var output bytes.Buffer
	require.NoError(t, renderLocalHarnessAuth(&output, result, "text"))
	require.Contains(t, output.String(), "api-key (2 providers):\n  first, second")
	require.NotContains(t, output.String(), "credential=")
	output.Reset()
	require.NoError(t, renderLocalHarnessAuth(&output, result, "json"))
	require.Contains(t, output.String(), `"methods":["api-key"]`)
	output.Reset()
	require.NoError(
		t,
		renderProviderNames(&output, []string{strings.Repeat("a", 50), strings.Repeat("b", 50)}),
	)
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		require.LessOrEqual(t, len(line), 80)
	}
}
