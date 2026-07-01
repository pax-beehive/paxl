package facade_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonFacadeStatusReturnsDaemonStatus(t *testing.T) {
	client := &fakeDaemonControlClient{
		status: &model.DaemonQueryResult{Status: &model.DaemonStatus{Phase: "running"}},
	}

	resp, err := facade.NewDaemonFacade(client).
		Status(context.Background(), &facade.DaemonStatusRequest{})

	require.NoError(t, err)
	assert.Equal(t, "running", resp.Status.Phase)
}

func TestDaemonFacadeStatusAddsGuidanceWhenLocalAPIFails(t *testing.T) {
	client := &fakeDaemonControlClient{err: errors.New("dial unix missing")}

	_, err := facade.NewDaemonFacade(client).
		Status(context.Background(), &facade.DaemonStatusRequest{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is paxd running")
}

func TestDaemonFacadeListRemotesReturnsItemsAndQueryErrors(t *testing.T) {
	queryErr := &model.DaemonControlError{Code: "bad_query", Message: "nope"}
	client := &fakeDaemonControlClient{
		remotes: &model.DaemonQueryResult{
			Remotes: &model.DaemonListRemotesResult{Items: []*model.DaemonRemoteView{{
				Remote: model.DaemonRemote{
					ID:          "prod",
					Name:        "Prod",
					CloudAPIURL: "https://api.test",
				},
			}}},
		},
	}
	resp, err := facade.NewDaemonFacade(client).
		ListRemotes(context.Background(), &facade.ListDaemonRemotesRequest{IncludeDisabled: true})
	require.NoError(t, err)
	require.Len(t, resp.Remotes, 1)
	assert.True(t, client.includeDisabled)
	assert.Equal(t, "prod", resp.Remotes[0].Remote.ID)

	client.remotes = &model.DaemonQueryResult{Error: queryErr}
	_, err = facade.NewDaemonFacade(client).ListRemotes(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad_query")
}

func TestDaemonFacadeRemoteCommandsCallLocalAPI(t *testing.T) {
	client := &fakeDaemonControlClient{ack: okDaemonAck()}
	daemon := facade.NewDaemonFacade(client)

	_, err := daemon.CreateRemote(context.Background(), &facade.CreateDaemonRemoteRequest{
		RemoteID:       "prod",
		CloudAPIURL:    "https://api.test/",
		NodeID:         "node_123",
		CloudAPIKeyRef: "file:/secret",
		Enabled:        true,
	})
	require.NoError(t, err)
	require.NotNil(t, client.createdRemote)
	assert.Equal(t, "prod", client.createdRemote.Remote.ID)
	assert.Equal(t, "https://api.test", client.createdRemote.Remote.CloudAPIURL)
	assert.Equal(t, "file:/secret", client.createdRemote.CloudAPIKeyRef)

	name := "Production"
	enabled := false
	_, err = daemon.UpdateRemote(context.Background(), &facade.UpdateDaemonRemoteRequest{
		RemoteID: "prod",
		Name:     &name,
		Enabled:  &enabled,
	})
	require.NoError(t, err)
	assert.Equal(t, "prod", client.updatedRemoteID)
	assert.Equal(t, "Production", *client.updatedRemote.Remote.Name)
	assert.False(t, *client.updatedRemote.Remote.Enabled)

	_, err = daemon.RestartRemote(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", client.restartedRemoteID)

	_, err = daemon.DisconnectRemote(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, []bool{false}, client.deletedRemoteCascades)

	_, err = daemon.RemoveRemote(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, []bool{false, true}, client.deletedRemoteCascades)
}

func TestDaemonFacadeRejectsInvalidRemoteRequests(t *testing.T) {
	daemon := facade.NewDaemonFacade(&fakeDaemonControlClient{ack: okDaemonAck()})

	_, err := daemon.CreateRemote(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is required")

	_, err = daemon.CreateRemote(
		context.Background(),
		&facade.CreateDaemonRemoteRequest{CloudAPIURL: "https://api.test"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote id")

	_, err = daemon.UpdateRemote(context.Background(), &facade.UpdateDaemonRemoteRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote id")
}

func TestDaemonFacadeReportsCommandAckFailures(t *testing.T) {
	daemon := facade.NewDaemonFacade(&fakeDaemonControlClient{
		ack: &model.DaemonCommandAck{Status: model.DaemonCommandStatusFailed},
	})
	_, err := daemon.RestartRemote(context.Background(), "prod")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")

	daemon = facade.NewDaemonFacade(&fakeDaemonControlClient{
		ack: &model.DaemonCommandAck{
			Status: model.DaemonCommandStatusFailed,
			Error:  &model.DaemonControlError{Code: "boom", Message: "failed"},
		},
	})
	_, err = daemon.RestartRemote(context.Background(), "prod")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestDaemonFacadeAgentCommandsCallLocalAPI(t *testing.T) {
	client := &fakeDaemonControlClient{
		ack: okDaemonAck(),
		agents: &model.DaemonQueryResult{AgentConnections: &model.DaemonListAgentConnectionsResult{
			Items: []*model.DaemonAgentConnectionView{
				{ID: "conn_work", RemoteID: "prod", Name: "work"},
			},
		}},
	}
	daemon := facade.NewDaemonFacade(client)

	list, err := daemon.ListAgents(
		context.Background(),
		&facade.ListDaemonAgentsRequest{IncludeDisabled: true},
	)
	require.NoError(t, err)
	require.Len(t, list.Agents, 1)
	assert.True(t, client.includeDisabled)

	_, err = daemon.CreateAgent(context.Background(), &facade.CreateDaemonAgentRequest{
		RemoteID: "prod",
		Name:     "work",
		Harness:  "codex",
		Command:  []string{"codex-acp"},
	})
	require.NoError(t, err)
	require.NotNil(t, client.createdAgent)
	assert.Equal(t, "work", client.createdAgent.InstanceID)
	assert.Equal(t, model.DaemonDesiredStateRunning, client.createdAgent.DesiredState)

	state := model.DaemonDesiredStateRunning
	_, err = daemon.UpdateAgent(context.Background(), &facade.UpdateDaemonAgentRequest{
		ConnectionID: "conn_work",
		DesiredState: &state,
	})
	require.NoError(t, err)
	assert.Equal(t, "conn_work", client.updatedAgentID)
	assert.Equal(t, model.DaemonDesiredStateRunning, *client.updatedAgent.DesiredState)

	_, err = daemon.StopAgent(context.Background(), "conn_work")
	require.NoError(t, err)
	assert.Equal(t, model.DaemonDesiredStateStopped, *client.updatedAgent.DesiredState)

	_, err = daemon.RestartAgent(context.Background(), "conn_work")
	require.NoError(t, err)
	assert.Equal(t, "conn_work", client.restartedAgentID)

	_, err = daemon.RemoveAgent(context.Background(), "conn_work")
	require.NoError(t, err)
	assert.Equal(t, "conn_work", client.deletedAgentID)
}

func TestDaemonFacadeRejectsInvalidAgentRequests(t *testing.T) {
	daemon := facade.NewDaemonFacade(&fakeDaemonControlClient{ack: okDaemonAck()})

	_, err := daemon.CreateAgent(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is required")

	_, err = daemon.CreateAgent(
		context.Background(),
		&facade.CreateDaemonAgentRequest{Name: "work", Harness: "codex"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote id")

	_, err = daemon.UpdateAgent(context.Background(), &facade.UpdateDaemonAgentRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection id")
}

func TestDaemonFacadeHarnessAndLocalQueries(t *testing.T) {
	client := &fakeDaemonControlClient{
		harnesses: &model.DaemonQueryResult{Harnesses: &model.DaemonListHarnessesResult{
			Items: []*model.DaemonHarnessView{{Harness: "codex", State: "available"}},
		}},
		overview: &model.DaemonQueryResult{LocalOverview: &model.DaemonLocalOverview{
			Sessions: []*model.DaemonLocalSessionView{{ID: "codex:1"}},
		}},
		localSessions: &model.DaemonQueryResult{LocalSessions: &model.DaemonListLocalSessionsResult{
			Items: []*model.DaemonLocalSessionView{{ID: "claude:2", Agent: "claude"}},
		}},
		localSync: &model.DaemonQueryResult{
			LocalSessionSync: &model.DaemonLocalSessionSyncResult{Synced: 2},
		},
	}
	daemon := facade.NewDaemonFacade(client)

	harnesses, err := daemon.ListHarnesses(
		context.Background(),
		&facade.ListDaemonHarnessesRequest{IncludeMissing: true},
	)
	require.NoError(t, err)
	assert.True(t, client.includeMissing)
	assert.Equal(t, "codex", harnesses.Harnesses[0].Harness)

	discovered, err := daemon.DiscoverHarnesses(
		context.Background(),
		&facade.DiscoverDaemonHarnessesRequest{
			Probe: true,
			Names: []string{"codex"},
		},
	)
	require.NoError(t, err)
	assert.True(t, client.probe)
	assert.Equal(t, []string{"codex"}, client.discoverNames)
	assert.Equal(t, "codex", discovered.Harnesses[0].Harness)

	overview, err := daemon.LocalOverview(
		context.Background(),
		&facade.DaemonLocalOverviewRequest{},
	)
	require.NoError(t, err)
	assert.Len(t, overview.Overview.Sessions, 1)

	sessions, err := daemon.ListLocalSessions(
		context.Background(),
		&facade.ListDaemonLocalSessionsRequest{Agent: "claude", Limit: 5},
	)
	require.NoError(t, err)
	assert.Equal(t, "claude", client.localAgent)
	assert.Equal(t, 5, client.localLimit)
	assert.Equal(t, "claude:2", sessions.Sessions[0].ID)

	sync, err := daemon.SyncLocalSessions(
		context.Background(),
		&facade.SyncDaemonLocalSessionsRequest{
			Agent:         "codex",
			Limit:         3,
			TimeoutMillis: 1000,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "codex", client.syncAgent)
	assert.Equal(t, int64(1000), client.syncTimeoutMillis)
	assert.Equal(t, 2, sync.Sync.Synced)
}

func TestDaemonFacadeQueriesReturnEmptyResponsesWhenDaemonOmitsCollections(t *testing.T) {
	daemon := facade.NewDaemonFacade(&fakeDaemonControlClient{})

	agents, err := daemon.ListAgents(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, agents.Agents)

	harnesses, err := daemon.ListHarnesses(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, harnesses.Harnesses)

	discovered, err := daemon.DiscoverHarnesses(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, discovered.Harnesses)

	overview, err := daemon.LocalOverview(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, overview.Overview)

	sessions, err := daemon.ListLocalSessions(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, sessions.Sessions)

	sync, err := daemon.SyncLocalSessions(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, sync.Sync)
}

func TestDaemonFacadeCommandAckFailuresReturnControlError(t *testing.T) {
	client := &fakeDaemonControlClient{ack: &model.DaemonCommandAck{
		Status: model.DaemonCommandStatusFailed,
		Error:  &model.DaemonControlError{Code: "boom", Message: "failed"},
	}}

	_, err := facade.NewDaemonFacade(client).RestartRemote(context.Background(), "prod")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func okDaemonAck() *model.DaemonCommandAck {
	return &model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived}
}

type fakeDaemonControlClient struct {
	err error
	ack *model.DaemonCommandAck

	status        *model.DaemonQueryResult
	remotes       *model.DaemonQueryResult
	agents        *model.DaemonQueryResult
	harnesses     *model.DaemonQueryResult
	overview      *model.DaemonQueryResult
	localSessions *model.DaemonQueryResult
	localSync     *model.DaemonQueryResult

	includeDisabled bool
	includeMissing  bool
	probe           bool
	discoverNames   []string

	createdRemote         *model.DaemonCreateRemoteCommand
	updatedRemoteID       string
	updatedRemote         *model.DaemonUpdateRemoteCommand
	restartedRemoteID     string
	deletedRemoteID       string
	deletedRemoteCascades []bool

	createdAgent     *model.DaemonCreateAgentConnectionCommand
	updatedAgentID   string
	updatedAgent     *model.DaemonUpdateAgentConnectionCommand
	restartedAgentID string
	deletedAgentID   string

	localAgent        string
	localLimit        int
	syncAgent         string
	syncLimit         int
	syncTimeoutMillis int64
}

func (f *fakeDaemonControlClient) GetStatus(context.Context) (*model.DaemonQueryResult, error) {
	return firstDaemonQueryResult(f.status), f.err
}

func (f *fakeDaemonControlClient) ListRemotes(
	_ context.Context,
	includeDisabled bool,
) (*model.DaemonQueryResult, error) {
	f.includeDisabled = includeDisabled
	return firstDaemonQueryResult(f.remotes), f.err
}

func (f *fakeDaemonControlClient) CreateRemote(
	_ context.Context,
	_ string,
	cmd *model.DaemonCreateRemoteCommand,
) (*model.DaemonCommandAck, error) {
	f.createdRemote = cmd
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) UpdateRemote(
	_ context.Context,
	_ string,
	remoteID string,
	cmd *model.DaemonUpdateRemoteCommand,
) (*model.DaemonCommandAck, error) {
	f.updatedRemoteID = remoteID
	f.updatedRemote = cmd
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) RestartRemote(
	_ context.Context,
	_ string,
	remoteID string,
) (*model.DaemonCommandAck, error) {
	f.restartedRemoteID = remoteID
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) DeleteRemote(
	_ context.Context,
	_ string,
	remoteID string,
	cascade bool,
) (*model.DaemonCommandAck, error) {
	f.deletedRemoteID = remoteID
	f.deletedRemoteCascades = append(f.deletedRemoteCascades, cascade)
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) ListAgentConnections(
	_ context.Context,
	includeDisabled bool,
) (*model.DaemonQueryResult, error) {
	f.includeDisabled = includeDisabled
	return firstDaemonQueryResult(f.agents), f.err
}

func (f *fakeDaemonControlClient) CreateAgentConnection(
	_ context.Context,
	_ string,
	cmd *model.DaemonCreateAgentConnectionCommand,
) (*model.DaemonCommandAck, error) {
	f.createdAgent = cmd
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) UpdateAgentConnection(
	_ context.Context,
	_ string,
	connectionID string,
	cmd *model.DaemonUpdateAgentConnectionCommand,
) (*model.DaemonCommandAck, error) {
	f.updatedAgentID = connectionID
	f.updatedAgent = cmd
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) RestartAgentConnection(
	_ context.Context,
	_ string,
	connectionID string,
) (*model.DaemonCommandAck, error) {
	f.restartedAgentID = connectionID
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) DeleteAgentConnection(
	_ context.Context,
	_ string,
	connectionID string,
) (*model.DaemonCommandAck, error) {
	f.deletedAgentID = connectionID
	return firstDaemonAck(f.ack), f.err
}

func (f *fakeDaemonControlClient) ListHarnesses(
	_ context.Context,
	includeMissing bool,
) (*model.DaemonQueryResult, error) {
	f.includeMissing = includeMissing
	return firstDaemonQueryResult(f.harnesses), f.err
}

func (f *fakeDaemonControlClient) DiscoverHarnesses(
	_ context.Context,
	probe bool,
	names []string,
) (*model.DaemonQueryResult, error) {
	f.probe = probe
	f.discoverNames = append([]string(nil), names...)
	return firstDaemonQueryResult(f.harnesses), f.err
}

func (f *fakeDaemonControlClient) GetLocalOverview(
	context.Context,
) (*model.DaemonQueryResult, error) {
	return firstDaemonQueryResult(f.overview), f.err
}

func (f *fakeDaemonControlClient) ListLocalSessions(
	_ context.Context,
	agent string,
	limit int,
) (*model.DaemonQueryResult, error) {
	f.localAgent = agent
	f.localLimit = limit
	return firstDaemonQueryResult(f.localSessions), f.err
}

func (f *fakeDaemonControlClient) SyncLocalSessions(
	_ context.Context,
	agent string,
	limit int,
	timeoutMillis int64,
) (*model.DaemonQueryResult, error) {
	f.syncAgent = agent
	f.syncLimit = limit
	f.syncTimeoutMillis = timeoutMillis
	return firstDaemonQueryResult(f.localSync), f.err
}

func firstDaemonQueryResult(result *model.DaemonQueryResult) *model.DaemonQueryResult {
	if result != nil {
		return result
	}
	return &model.DaemonQueryResult{}
}

func firstDaemonAck(ack *model.DaemonCommandAck) *model.DaemonCommandAck {
	if ack != nil {
		return ack
	}
	return okDaemonAck()
}
