package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentConfigurationSchemaDescribesStaticVersionOne(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(AgentConfigurationSchema(), &schema))
	require.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	versionProperty, ok := properties["schema_version"].(map[string]any)
	require.True(t, ok)
	runtimeProperty, ok := properties["runtime_verified"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 1, versionProperty["const"])
	require.Equal(t, false, runtimeProperty["const"])
	require.Contains(t, properties, "skills")
	require.Contains(t, properties, "mcp_servers")
	require.Contains(t, properties, "sources")
	require.Contains(t, properties, "coverage")
	require.Contains(t, properties, "conflicts")
}

func TestParseAgentConfigurationKindRejectsUnsupportedInput(t *testing.T) {
	for _, raw := range []string{"", "unknown", "prompts", "SKILLS"} {
		kind, err := ParseAgentConfigurationKind(raw)
		require.Error(t, err)
		require.Equal(t, AgentConfigurationKindUnknown, kind)
	}
	for _, raw := range []string{"all", "skills", "mcp"} {
		kind, err := ParseAgentConfigurationKind(raw)
		require.NoError(t, err)
		require.Equal(t, AgentConfigurationKind(raw), kind)
	}
}
