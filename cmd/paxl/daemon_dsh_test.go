package main

import (
	"bytes"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonHarnessInstallDSHDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(
		t.Context(),
		[]string{"daemon", "harness", "install", "dsh", "--dry-run"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "npm install -g @deepseek-ai/dsh@latest")
	assert.Contains(t, stdout.String(), "DEEPSEEK_API_KEY")
}

func TestDaemonDSHDiscoveryAndCreation(t *testing.T) {
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
						Harness: "dsh",
						State:   "available",
						Command: []string{"dsh", "--profile", "acp"},
					},
				},
			},
		},
	}
	defer stubDaemonFacade(t, client)()
	var stdout, stderr bytes.Buffer
	require.NoError(
		t,
		run(t.Context(), []string{"daemon", "harness", "discover", "dsh"}, &stdout, &stderr),
	)
	assert.Contains(t, stdout.String(), "dsh")
	require.NoError(
		t,
		run(
			t.Context(),
			[]string{"daemon", "agent", "create", "--harness", "dsh", "--name", "deepseek"},
			&stdout,
			&stderr,
		),
	)
	require.NotNil(t, client.createdAgent)
	assert.Equal(t, "dsh", client.createdAgent.Harness)
	assert.Equal(t, "dsh", client.createdAgent.AgentType)
	assert.Equal(t, "agent_cloud_deepseek", client.createdAgent.CloudAgentID)
	assert.Equal(t, []string{"dsh", "--profile", "acp"}, client.createdAgent.Command)
}
