package model_test

import (
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestRemainingLocalAuthHarnessesAcceptTheirNativeMethods(t *testing.T) {
	for _, tc := range []struct{ harness, method string }{
		{"gemini", "browser"}, {"gemini", "api-key"}, {"dsh", "oauth"}, {"dscode", "api-key"}, {"antigravity", "interactive"},
	} {
		h, err := model.ParseLocalAuthHarness(tc.harness)
		require.NoError(t, err)
		_, err = model.ParseHarnessAuthMethod(tc.method, h)
		require.NoError(t, err)
	}
}
