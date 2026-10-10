package main

import (
	"bytes"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonHarnessInstallDSCodeDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(
		t.Context(),
		[]string{"daemon", "harness", "install", "dscode", "--dry-run"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "npm install -g @toddzheng024/dscode@latest")
	assert.Contains(t, stdout.String(), "DEEPSEEK_API_KEY")
}

func TestDaemonDSCodeDiscoveryAndCreation(t *testing.T) {
	client := &cmdFakeDaemonControlClient{
		remotes: &model.DaemonQueryResult{
			Remotes: &model.DaemonListRemotesResult{
				Items: []*model.DaemonRemoteView{{Remote: model.DaemonRemote{ID: "prod"}}},
			},
		},
		harnesses: &model.DaemonQueryResult{
			Harnesses: &model.DaemonListHarnessesResult{
				Items: []*model.DaemonHarnessView{
					{
						Harness: "dscode",
						State:   "available",
						Command: []string{"dscode", "acp"},
					},
				},
			},
		},
	}
	defer stubDaemonFacade(t, client)()
	var stdout, stderr bytes.Buffer
	require.NoError(
		t,
		run(t.Context(), []string{"daemon", "harness", "discover", "dscode"}, &stdout, &stderr),
	)
	assert.Contains(t, stdout.String(), "dscode")
	require.NoError(
		t,
		run(
			t.Context(),
			[]string{"daemon", "agent", "create", "--harness", "dscode", "--name", "deepseek"},
			&stdout,
			&stderr,
		),
	)
	require.NotNil(t, client.createdAgent)
	assert.Equal(t, "dscode", client.createdAgent.Harness)
	assert.Equal(t, "dscode", client.createdAgent.AgentType)
	assert.Equal(t, "agent_cloud_deepseek", client.createdAgent.CloudAgentID)
	assert.Equal(t, []string{"dscode", "acp"}, client.createdAgent.Command)
}
