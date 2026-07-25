package model_test

import (
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestParseAgentPermissionAcceptsGrantablePermissions(t *testing.T) {
	tests := []struct {
		raw  string
		want model.AgentPermission
	}{
		{raw: " observe ", want: model.AgentPermissionObserve},
		{raw: "SEARCH", want: model.AgentPermissionSearch},
		{raw: "get", want: model.AgentPermissionGet},
		{raw: "channel_send", want: model.AgentPermissionChannelSend},
		{raw: "channel_receive", want: model.AgentPermissionChannelReceive},
	}

	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			got, err := model.ParseAgentPermission(test.raw)

			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestParseAgentPermissionRejectsUnsupportedPermissions(t *testing.T) {
	for _, raw := range []string{"", "admin", "agent_provision"} {
		t.Run(raw, func(t *testing.T) {
			got, err := model.ParseAgentPermission(raw)

			require.Error(t, err)
			require.Equal(t, model.AgentPermissionUnknown, got)
		})
	}
}
