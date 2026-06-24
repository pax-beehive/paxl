package adaptor

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestACPFirstNonTerminalAuthMethodPrefersReadyEnvVar(t *testing.T) {
	t.Setenv("PAXL_TEST_ACP_TOKEN", "token")

	methodID := acpFirstNonTerminalAuthMethod([]acpAuthMethod{
		{ID: "oauth", Type: "oauth"},
		{
			ID:   "env",
			Type: "env_var",
			Vars: []struct {
				Name string `json:"name"`
			}{{Name: "PAXL_TEST_ACP_TOKEN"}},
		},
		{ID: "manual", Type: "custom"},
	})

	require.Equal(t, "env", methodID)
}

func TestACPFirstNonTerminalAuthMethodSkipsInteractiveMethods(t *testing.T) {
	methodID := acpFirstNonTerminalAuthMethod([]acpAuthMethod{
		{ID: "gateway-login", Name: "Gateway login", Type: "custom"},
		{ID: "api-key", Name: "API key setup", Type: "custom"},
		{ID: "safe", Name: "No auth", Type: "custom"},
	})

	require.Equal(t, "safe", methodID)
}

func TestACPWithStderrIncludesUsefulTail(t *testing.T) {
	err := acpWithStderr("initialize", errors.New("failed"), strings.Repeat("x", 600)+"tail")

	require.Contains(t, err.Error(), "initialize: failed")
	require.Contains(t, err.Error(), "tail")
	require.NotContains(t, err.Error(), strings.Repeat("x", 600))
}

func TestACPWithStderrOmitsEmptyStderr(t *testing.T) {
	err := acpWithStderr("session/list", errors.New("failed"), "  ")

	require.EqualError(t, err, "session/list: failed")
}
