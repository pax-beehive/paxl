package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonAgentUpdateSendsDesiredSlots(t *testing.T) {
	client := &cmdFakeDaemonControlClient{
		ack: &model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived},
	}
	restore := stubDaemonFacade(t, client)
	defer restore()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"daemon", "agent", "update", "conn_codex", "--desired-slots", "2",
	}, &stdout, &stderr)
	require.NoError(t, err)
	require.NotNil(t, client.updatedAgent)
	raw, err := json.Marshal(client.updatedAgent)
	require.NoError(t, err)
	assert.JSONEq(t, `{"connection_id":"","desired_slots":2}`, string(raw))
}

func TestDaemonAgentUpdateRejectsDesiredSlotsOutsideDaemonRange(t *testing.T) {
	for _, desiredSlots := range []string{"0", "17"} {
		t.Run(desiredSlots, func(t *testing.T) {
			client := &cmdFakeDaemonControlClient{
				ack: &model.DaemonCommandAck{
					OK:     true,
					Status: model.DaemonCommandStatusReceived,
				},
			}
			restore := stubDaemonFacade(t, client)
			defer restore()

			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{
				"daemon", "agent", "update", "conn_codex", "--desired-slots", desiredSlots,
			}, &stdout, &stderr)

			assert.ErrorContains(t, err, "--desired-slots must be between 1 and 16")
			assert.Nil(t, client.updatedAgent)
		})
	}
}

func TestDaemonStatusCommandRendersStatus(t *testing.T) {
	restore := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		status: &model.DaemonQueryResult{Status: &model.DaemonStatus{
			Phase: "running",
			Remotes: []*model.DaemonRemoteStatusView{{
				RemoteID: "prod",
				Phase:    "connected",
			}},
		}},
	})
	defer restore()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"daemon", "status"}, &stdout, &stderr)

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "DAEMON")
	assert.Contains(t, stdout.String(), "running")
	assert.Contains(t, stdout.String(), "prod")
}

func TestDaemonRemoteCommandsUseDaemonFacade(t *testing.T) {
	client := &cmdFakeDaemonControlClient{
		ack: &model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived},
		remotes: &model.DaemonQueryResult{Remotes: &model.DaemonListRemotesResult{
			Items: []*model.DaemonRemoteView{{
				Remote: model.DaemonRemote{
					ID:          "prod",
					Name:        "Prod",
					CloudAPIURL: "https://api.test",
				},
			}},
		}},
	}
	restore := stubDaemonFacade(t, client)
	defer restore()

	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"daemon", "remote", "list", "--all"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.True(t, client.includeDisabled)
	assert.Contains(t, stdout.String(), "prod")

	stdout.Reset()
	err = run(context.Background(), []string{
		"daemon", "remote", "create", "staging",
		"--cloud-url", "https://staging.test",
		"--node-id", "node_staging",
		"--api-key-ref", "file:/secret",
	}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "staging", client.createdRemote.Remote.ID)
	assert.Contains(t, stdout.String(), "Remote create requested.")

	stdout.Reset()
	err = run(context.Background(), []string{
		"daemon", "remote", "create",
	}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "default", client.createdRemote.Remote.ID)
	assert.Equal(t, facade.DefaultManagerURL, client.createdRemote.Remote.CloudAPIURL)

	stdout.Reset()
	err = run(context.Background(), []string{
		"daemon", "remote", "update", "staging",
		"--name", "Staging",
		"--enabled",
	}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "staging", client.updatedRemoteID)
	assert.Equal(t, "Staging", *client.updatedRemote.Remote.Name)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "remote", "restart", "staging"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, "staging", client.restartedRemoteID)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "remote", "disconnect", "staging"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, "staging", client.deletedRemoteID)
	assert.False(t, client.deletedRemoteCascade)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "remote", "remove", "staging"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, "staging", client.deletedRemoteID)
	assert.True(t, client.deletedRemoteCascade)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "remote", "list", "--format", "jsonl"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"id":"prod"`)
}

func TestDaemonAgentHarnessAndLocalCommandsUseDaemonFacade(t *testing.T) {
	client := &cmdFakeDaemonControlClient{
		ack: &model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived},
		remotes: &model.DaemonQueryResult{Remotes: &model.DaemonListRemotesResult{
			Items: []*model.DaemonRemoteView{{
				Remote: model.DaemonRemote{ID: "prod", Name: "Production"},
			}},
		}},
		agents: &model.DaemonQueryResult{AgentConnections: &model.DaemonListAgentConnectionsResult{
			Items: []*model.DaemonAgentConnectionView{{
				ID:           "conn_work",
				RemoteID:     "prod",
				Name:         "work",
				Harness:      "codex",
				DesiredState: model.DaemonDesiredStateRunning,
			}},
		}},
		harnesses: &model.DaemonQueryResult{Harnesses: &model.DaemonListHarnessesResult{
			Items: []*model.DaemonHarnessView{
				{Harness: "codex", State: "available", Command: []string{"codex-acp"}},
				{
					Harness: "hermes",
					State:   "available",
					Command: []string{"hermes", "agent", "--acp"},
				},
			},
		}},
		localSessions: &model.DaemonQueryResult{LocalSessions: &model.DaemonListLocalSessionsResult{
			Items: []*model.DaemonLocalSessionView{{ID: "codex:1", Agent: "codex", Title: "Work"}},
		}},
		localSync: &model.DaemonQueryResult{
			LocalSessionSync: &model.DaemonLocalSessionSyncResult{Synced: 1},
		},
	}
	restore := stubDaemonFacade(t, client)
	defer restore()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"daemon", "agent", "list"}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "conn_work")

	stdout.Reset()
	err = run(context.Background(), []string{
		"daemon", "agent", "create",
		"--remote", "prod",
		"--name", "review",
		"--harness", "codex",
		"--command", "codex-acp",
	}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "review", client.createdAgent.Name)
	assert.Equal(t, "agent_cloud_review", client.createdAgent.CloudAgentID)

	stdout.Reset()
	err = run(context.Background(), []string{
		"daemon", "agent", "create",
		"--remote", "prod",
		"--harness", "hermes",
	}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "hermes", client.createdAgent.Name)
	assert.Equal(t, "agent_cloud_hermes", client.createdAgent.CloudAgentID)
	assert.Equal(t, []string{"hermes", "agent", "--acp"}, client.createdAgent.Command)

	stdout.Reset()
	err = run(context.Background(), []string{
		"daemon", "agent", "update", "conn_work",
		"--desired-state", "running",
		"--name", "Work 2",
		"--harness", "codex",
		"--command", "codex-acp",
		"--cloud-agent-id", "agent_123",
		"--instance-id", "work-2",
		"--agent-type", "codex",
		"--working-dir", "/tmp/project",
		"--disabled",
	}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "conn_work", client.updatedAgentID)
	assert.Equal(t, model.DaemonDesiredStateRunning, *client.updatedAgent.DesiredState)
	assert.Equal(t, "Work 2", *client.updatedAgent.Name)
	assert.False(t, *client.updatedAgent.Enabled)
	assert.Equal(t, []string{"codex-acp"}, *client.updatedAgent.Command)
	assert.Nil(t, client.updatedAgent.DesiredSlots)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "agent", "restart", "conn_work"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, "conn_work", client.restartedAgentID)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "agent", "stop", "conn_work"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, model.DaemonDesiredStateStopped, *client.updatedAgent.DesiredState)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "agent", "remove", "conn_work"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, "conn_work", client.deletedAgentID)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "harness", "discover", "--probe", "codex"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.True(t, client.probe)
	assert.Equal(t, []string{"codex"}, client.discoverNames)
	assert.Contains(t, stdout.String(), "codex-acp")

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "harness", "list", "--format", "jsonl"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"harness":"codex"`)

	stdout.Reset()
	err = run(context.Background(), []string{"daemon", "local", "overview"}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "SESSIONS")

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "local", "session", "list", "--agent", "codex"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, "codex", client.localAgent)
	assert.Contains(t, stdout.String(), "codex:1")

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "local", "session", "sync", "--timeout", "1s"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), client.syncTimeoutMillis)
	assert.Contains(t, stdout.String(), "Synced 1")

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "local", "session", "sync", "--format", "json"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"synced":1`)
}

func TestDaemonCommandRejectsInvalidDesiredState(t *testing.T) {
	restore := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		ack: &model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived},
	})
	defer restore()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"daemon", "agent", "update", "conn_work",
		"--desired-state", "sleepy",
	}, &stdout, &stderr)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported desired state")
}

func TestParseDaemonDesiredStateAcceptsKnownStates(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want model.DaemonDesiredState
	}{
		{raw: "running", want: model.DaemonDesiredStateRunning},
		{raw: "stopped", want: model.DaemonDesiredStateStopped},
		{raw: "deleted", want: model.DaemonDesiredStateDeleted},
	} {
		got, err := parseDaemonDesiredState(tc.raw)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

func TestDaemonCommandsRenderJSONOutputs(t *testing.T) {
	client := &cmdFakeDaemonControlClient{
		ack: &model.DaemonCommandAck{
			OK:       true,
			Status:   model.DaemonCommandStatusReceived,
			TargetID: "prod",
		},
		status: &model.DaemonQueryResult{Status: &model.DaemonStatus{
			Phase: "running",
		}},
		localSessions: &model.DaemonQueryResult{LocalSessions: &model.DaemonListLocalSessionsResult{
			Items: []*model.DaemonLocalSessionView{{ID: "codex:1", Agent: "codex"}},
		}},
	}
	restore := stubDaemonFacade(t, client)
	defer restore()

	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"daemon", "status", "--format", "jsonl"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"phase":"running"`)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "remote", "restart", "prod", "--format", "json"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"target_id":"prod"`)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "local", "overview", "--format", "jsonl"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"sessions"`)

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "local", "session", "list", "--format", "jsonl"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"id":"codex:1"`)
}

func TestDaemonCommandsDoNotInstallOldPaxctlAliases(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"status"}, &stdout, &stderr)
	require.Error(t, err)

	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"agents", "list"}, &stdout, &stderr)
	require.Error(t, err)

	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"harnesses", "list"}, &stdout, &stderr)
	require.Error(t, err)
}

func TestDaemonLifecycleCommandsSupportDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"daemon", "install", "--dry-run"}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Would install paxd")

	stdout.Reset()
	err = run(context.Background(), []string{"daemon", "update", "--dry-run"}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Would update paxd")

	stdout.Reset()
	err = run(context.Background(), []string{"daemon", "setup", "--dry-run"}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Would set up paxd")

	stdout.Reset()
	err = run(
		context.Background(),
		[]string{"daemon", "service", "restart", "--dry-run"},
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Would run paxd service restart")
}

func TestDaemonInstallDerivesResolverFromDefaultRemote(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{}
	restoreLifecycle := stubDaemonLifecycleFacade(t, lifecycle)
	defer restoreLifecycle()
	restoreDaemon := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		remotes: daemonRemoteQuery(
			"default",
			"https://self-hosted.test/base/",
		),
	})
	defer restoreDaemon()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "install", "--dry-run",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.installReq)
	assert.Equal(
		t,
		"https://self-hosted.test/base/api/v1/public/paxd/download",
		lifecycle.installReq.ResolverURL,
	)
}

func TestDaemonUpdateDerivesResolverFromSelectedRemote(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{}
	restoreLifecycle := stubDaemonLifecycleFacade(t, lifecycle)
	defer restoreLifecycle()
	restoreDaemon := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		remotes: &model.DaemonQueryResult{Remotes: &model.DaemonListRemotesResult{
			Items: []*model.DaemonRemoteView{
				{
					Remote: model.DaemonRemote{
						ID:          "default",
						CloudAPIURL: "https://hosted.test",
					},
				},
				{
					Remote: model.DaemonRemote{
						ID:          "prod",
						CloudAPIURL: "https://prod.test/pax/",
					},
				},
			},
		}},
	})
	defer restoreDaemon()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "update", "--dry-run", "--remote", "prod",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.updateReq)
	assert.Equal(
		t,
		"https://prod.test/pax/api/v1/public/paxd/download",
		lifecycle.updateReq.ResolverURL,
	)
}

func TestDaemonUpdateCheckExplicitResolverWinsOverRemote(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{
		check: &facade.DaemonUpdateCheckResponse{},
	}
	restoreLifecycle := stubDaemonLifecycleFacade(t, lifecycle)
	defer restoreLifecycle()
	restoreDaemon := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		remotesErr: assert.AnError,
	})
	defer restoreDaemon()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "update", "check",
		"--remote", "prod",
		"--resolver-url", "https://resolver.test/custom",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.checkReq)
	assert.Equal(t, "https://resolver.test/custom", lifecycle.checkReq.ResolverURL)
}

func TestDaemonUpdateCheckFallsBackToHostedWithoutDefaultRemote(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{
		check: &facade.DaemonUpdateCheckResponse{},
	}
	restoreLifecycle := stubDaemonLifecycleFacade(t, lifecycle)
	defer restoreLifecycle()
	restoreDaemon := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		remotes: daemonRemoteQuery("prod", "https://prod.test"),
	})
	defer restoreDaemon()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "update", "check",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.checkReq)
	assert.Equal(t, facade.DefaultDaemonResolverURL, lifecycle.checkReq.ResolverURL)
}

func TestDaemonInstallRejectsUnknownExplicitRemote(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{}
	restoreLifecycle := stubDaemonLifecycleFacade(t, lifecycle)
	defer restoreLifecycle()
	restoreDaemon := stubDaemonFacade(t, &cmdFakeDaemonControlClient{
		remotes: daemonRemoteQuery("default", "https://default.test"),
	})
	defer restoreDaemon()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "install", "--dry-run", "--remote", "missing",
	}, &stdout, &stderr)

	require.Error(t, err)
	assert.ErrorContains(t, err, `daemon remote "missing" was not found`)
	assert.Nil(t, lifecycle.installReq)
}

func daemonRemoteQuery(remoteID string, cloudAPIURL string) *model.DaemonQueryResult {
	return &model.DaemonQueryResult{Remotes: &model.DaemonListRemotesResult{
		Items: []*model.DaemonRemoteView{{
			Remote: model.DaemonRemote{ID: remoteID, CloudAPIURL: cloudAPIURL},
		}},
	}}
}

func TestDaemonRemoteLoginCommandUsesDaemonLifecycleFacade(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{
		remoteLogin: &facade.DaemonLifecycleResponse{
			Status:  facade.SetupStatusInstalled,
			Binary:  "paxd",
			Action:  "remote login",
			Message: "paxd remote login completed.",
		},
	}
	restore := stubDaemonLifecycleFacade(t, lifecycle)
	defer restore()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "remote", "login", "staging",
		"--cloud-url", "https://api.example.com/",
		"--api-endpoint", "http://127.0.0.1:9000",
		"--resolver-url", "https://resolver.test/resolve",
		"--platform", "linux/amd64",
		"--tag", "stable",
		"--install-dir", "/tmp/bin",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.remoteLoginReq)
	assert.Equal(t, "staging", lifecycle.remoteLoginReq.RemoteID)
	assert.Equal(t, "https://api.example.com/", lifecycle.remoteLoginReq.CloudURL)
	assert.Equal(t, "http://127.0.0.1:9000", lifecycle.remoteLoginReq.APIEndpoint)
	assert.Equal(t, "https://resolver.test/resolve", lifecycle.remoteLoginReq.ResolverURL)
	assert.Equal(t, "linux/amd64", lifecycle.remoteLoginReq.Platform)
	assert.Equal(t, "stable", lifecycle.remoteLoginReq.Tag)
	assert.Equal(t, "/tmp/bin", lifecycle.remoteLoginReq.InstallDir)
	assert.Contains(t, stdout.String(), "paxd remote login completed")
}

func TestDaemonSetupLeavesResolverUnsetForCloudDerivedDefault(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{}
	restore := stubDaemonLifecycleFacade(t, lifecycle)
	defer restore()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "setup",
		"--cloud-url", "https://self-hosted.test/",
		"--dry-run",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.setupReq)
	assert.Equal(t, "https://self-hosted.test/", lifecycle.setupReq.CloudURL)
	assert.Empty(t, lifecycle.setupReq.ResolverURL)
}

func TestDaemonRemoteLoginLeavesResolverUnsetForCloudDerivedDefault(t *testing.T) {
	lifecycle := &cmdFakeDaemonLifecycleFacade{}
	restore := stubDaemonLifecycleFacade(t, lifecycle)
	defer restore()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "remote", "login", "staging",
		"--cloud-url", "https://self-hosted.test/",
		"--dry-run",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.remoteLoginReq)
	assert.Equal(t, "https://self-hosted.test/", lifecycle.remoteLoginReq.CloudURL)
	assert.Empty(t, lifecycle.remoteLoginReq.ResolverURL)
}

func TestDaemonUpdateCheckCommandRendersLatestPaxdArtifact(t *testing.T) {
	signedURL := "https://objects.test/paxd?X-Amz-Credential=daemon-print-secret&X-Amz-Signature=daemon-signature-secret"
	lifecycle := &cmdFakeDaemonLifecycleFacade{
		check: &facade.DaemonUpdateCheckResponse{
			Binary:      "paxd",
			Version:     "0.2.0",
			Platform:    "linux/amd64",
			DownloadURL: signedURL,
			SHA256:      "abc123",
			SizeBytes:   42,
			Action:      "update check",
			Message:     "Latest paxd 0.2.0 is available for linux/amd64.",
		},
	}
	restore := stubDaemonLifecycleFacade(t, lifecycle)
	defer restore()
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{
		"daemon", "update", "check",
		"--resolver-url", "https://resolver.test/api/v1/public/paxd/download",
		"--platform", "linux/amd64",
		"--format", "json",
	}, &stdout, &stderr)

	require.NoError(t, err)
	require.NotNil(t, lifecycle.checkReq)
	assert.Equal(
		t,
		"https://resolver.test/api/v1/public/paxd/download",
		lifecycle.checkReq.ResolverURL,
	)
	assert.Equal(t, "linux/amd64", lifecycle.checkReq.Platform)
	assert.Contains(t, stdout.String(), `"binary":"paxd"`)
	assert.Contains(t, stdout.String(), `"version":"0.2.0"`)
	assert.Contains(t, stdout.String(), `"sha256":"abc123"`)
	assert.NotContains(t, stdout.String(), "download_url")
	assert.NotContains(t, stdout.String(), "daemon-print-secret")
	assert.NotContains(t, stdout.String(), "daemon-signature-secret")
}

func TestRenderDaemonUpdateCheckTextDoesNotPrintSignedURL(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := renderDaemonUpdateCheck(&stdout, &facade.DaemonUpdateCheckResponse{
		Message: "Latest paxd is available.",
		DownloadURL: "https://objects.test/paxd?" +
			"X-Amz-Credential=text-secret&X-Amz-Signature=signature-secret",
		SHA256:    "abc123",
		SizeBytes: 42,
	}, "text")

	require.NoError(t, err)
	assert.NotContains(t, stdout.String(), "URL:")
	assert.NotContains(t, stdout.String(), "text-secret")
	assert.NotContains(t, stdout.String(), "signature-secret")
}

func stubDaemonLifecycleFacade(t *testing.T, lifecycle *cmdFakeDaemonLifecycleFacade) func() {
	t.Helper()
	previous := newDaemonLifecycleFacade
	newDaemonLifecycleFacade = func() daemonLifecycleFacade {
		return lifecycle
	}
	return func() {
		newDaemonLifecycleFacade = previous
	}
}

func stubDaemonFacade(t *testing.T, client *cmdFakeDaemonControlClient) func() {
	t.Helper()
	previous := newDaemonFacade
	newDaemonFacade = func() *facade.DaemonFacade {
		return facade.NewDaemonFacade(
			client,
			facade.WithDaemonRemoteNodeKeyLoader(cmdFakeDaemonNodeKeyLoader{}),
			facade.WithDaemonCloudAgentRegistrar(cmdFakeDaemonCloudAgentRegistrar{}),
		)
	}
	return func() {
		newDaemonFacade = previous
	}
}

type cmdFakeDaemonNodeKeyLoader struct{}

func (cmdFakeDaemonNodeKeyLoader) LoadRemoteNodeKey(
	context.Context,
	string,
) (string, error) {
	return "node-secret", nil
}

type cmdFakeDaemonCloudAgentRegistrar struct{}

func (cmdFakeDaemonCloudAgentRegistrar) RegisterCloudAgent(
	_ context.Context,
	req *facade.DaemonCloudAgentRegistrationRequest,
) (string, error) {
	return "agent_cloud_" + req.Name, nil
}

type cmdFakeDaemonLifecycleFacade struct {
	installReq     *facade.DaemonInstallRequest
	updateReq      *facade.DaemonUpdateRequest
	check          *facade.DaemonUpdateCheckResponse
	checkReq       *facade.DaemonUpdateCheckRequest
	setupReq       *facade.DaemonSetupRequest
	remoteLogin    *facade.DaemonLifecycleResponse
	remoteLoginReq *facade.DaemonRemoteLoginRequest
}

func (f *cmdFakeDaemonLifecycleFacade) Install(
	_ context.Context,
	req *facade.DaemonInstallRequest,
	_ ...func(*facade.Option),
) (*facade.DaemonLifecycleResponse, error) {
	f.installReq = req
	return &facade.DaemonLifecycleResponse{}, nil
}

func (f *cmdFakeDaemonLifecycleFacade) Update(
	_ context.Context,
	req *facade.DaemonUpdateRequest,
	_ ...func(*facade.Option),
) (*facade.DaemonLifecycleResponse, error) {
	f.updateReq = req
	return &facade.DaemonLifecycleResponse{}, nil
}

func (f *cmdFakeDaemonLifecycleFacade) Check(
	_ context.Context,
	req *facade.DaemonUpdateCheckRequest,
	_ ...func(*facade.Option),
) (*facade.DaemonUpdateCheckResponse, error) {
	f.checkReq = req
	return f.check, nil
}

func (f *cmdFakeDaemonLifecycleFacade) Setup(
	_ context.Context,
	req *facade.DaemonSetupRequest,
	_ ...func(*facade.Option),
) (*facade.DaemonLifecycleResponse, error) {
	f.setupReq = req
	return &facade.DaemonLifecycleResponse{}, nil
}

func (f *cmdFakeDaemonLifecycleFacade) RemoteLogin(
	_ context.Context,
	req *facade.DaemonRemoteLoginRequest,
	_ ...func(*facade.Option),
) (*facade.DaemonLifecycleResponse, error) {
	f.remoteLoginReq = req
	if f.remoteLogin != nil {
		return f.remoteLogin, nil
	}
	return &facade.DaemonLifecycleResponse{}, nil
}

func (f *cmdFakeDaemonLifecycleFacade) Service(
	context.Context,
	*facade.DaemonServiceRequest,
	...func(*facade.Option),
) (*facade.DaemonLifecycleResponse, error) {
	return &facade.DaemonLifecycleResponse{}, nil
}

type cmdFakeDaemonControlClient struct {
	ack           *model.DaemonCommandAck
	status        *model.DaemonQueryResult
	remotes       *model.DaemonQueryResult
	remotesErr    error
	agents        *model.DaemonQueryResult
	harnesses     *model.DaemonQueryResult
	localSessions *model.DaemonQueryResult
	localSync     *model.DaemonQueryResult

	includeDisabled bool
	probe           bool
	discoverNames   []string

	createdRemote        *model.DaemonCreateRemoteCommand
	updatedRemoteID      string
	updatedRemote        *model.DaemonUpdateRemoteCommand
	restartedRemoteID    string
	deletedRemoteID      string
	deletedRemoteCascade bool
	createdAgent         *model.DaemonCreateAgentConnectionCommand
	updatedAgentID       string
	updatedAgent         *model.DaemonUpdateAgentConnectionCommand
	restartedAgentID     string
	deletedAgentID       string

	localAgent        string
	syncTimeoutMillis int64
}

func (c *cmdFakeDaemonControlClient) GetStatus(context.Context) (*model.DaemonQueryResult, error) {
	return firstCmdDaemonQuery(c.status), nil
}

func (c *cmdFakeDaemonControlClient) ListRemotes(
	_ context.Context,
	includeDisabled bool,
) (*model.DaemonQueryResult, error) {
	c.includeDisabled = includeDisabled
	return firstCmdDaemonQuery(c.remotes), c.remotesErr
}

func (c *cmdFakeDaemonControlClient) CreateRemote(
	_ context.Context,
	_ string,
	cmd *model.DaemonCreateRemoteCommand,
) (*model.DaemonCommandAck, error) {
	c.createdRemote = cmd
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) UpdateRemote(
	_ context.Context,
	_ string,
	remoteID string,
	cmd *model.DaemonUpdateRemoteCommand,
) (*model.DaemonCommandAck, error) {
	c.updatedRemoteID = remoteID
	c.updatedRemote = cmd
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) RestartRemote(
	_ context.Context,
	_ string,
	remoteID string,
) (*model.DaemonCommandAck, error) {
	c.restartedRemoteID = remoteID
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) DeleteRemote(
	_ context.Context,
	_ string,
	remoteID string,
	cascade bool,
) (*model.DaemonCommandAck, error) {
	c.deletedRemoteID = remoteID
	c.deletedRemoteCascade = cascade
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) ListAgentConnections(
	context.Context,
	bool,
) (*model.DaemonQueryResult, error) {
	return firstCmdDaemonQuery(c.agents), nil
}

func (c *cmdFakeDaemonControlClient) CreateAgentConnection(
	_ context.Context,
	_ string,
	cmd *model.DaemonCreateAgentConnectionCommand,
) (*model.DaemonCommandAck, error) {
	c.createdAgent = cmd
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) UpdateAgentConnection(
	_ context.Context,
	_ string,
	connectionID string,
	cmd *model.DaemonUpdateAgentConnectionCommand,
) (*model.DaemonCommandAck, error) {
	c.updatedAgentID = connectionID
	c.updatedAgent = cmd
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) RestartAgentConnection(
	_ context.Context,
	_ string,
	connectionID string,
) (*model.DaemonCommandAck, error) {
	c.restartedAgentID = connectionID
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) DeleteAgentConnection(
	_ context.Context,
	_ string,
	connectionID string,
) (*model.DaemonCommandAck, error) {
	c.deletedAgentID = connectionID
	return firstCmdDaemonAck(c.ack), nil
}

func (c *cmdFakeDaemonControlClient) ListHarnesses(
	context.Context,
	bool,
) (*model.DaemonQueryResult, error) {
	return firstCmdDaemonQuery(c.harnesses), nil
}

func (c *cmdFakeDaemonControlClient) DiscoverHarnesses(
	_ context.Context,
	probe bool,
	names []string,
) (*model.DaemonQueryResult, error) {
	c.probe = probe
	c.discoverNames = append([]string(nil), names...)
	return firstCmdDaemonQuery(c.harnesses), nil
}

func (c *cmdFakeDaemonControlClient) GetLocalOverview(
	context.Context,
) (*model.DaemonQueryResult, error) {
	return &model.DaemonQueryResult{LocalOverview: &model.DaemonLocalOverview{
		Sessions: []*model.DaemonLocalSessionView{{ID: "codex:1"}},
	}}, nil
}

func (c *cmdFakeDaemonControlClient) ListLocalSessions(
	_ context.Context,
	agent string,
	limit int,
) (*model.DaemonQueryResult, error) {
	c.localAgent = agent
	_ = limit
	return firstCmdDaemonQuery(c.localSessions), nil
}

func (c *cmdFakeDaemonControlClient) SyncLocalSessions(
	_ context.Context,
	agent string,
	limit int,
	timeoutMillis int64,
) (*model.DaemonQueryResult, error) {
	_ = agent
	_ = limit
	c.syncTimeoutMillis = timeoutMillis
	return firstCmdDaemonQuery(c.localSync), nil
}

func firstCmdDaemonQuery(result *model.DaemonQueryResult) *model.DaemonQueryResult {
	if result != nil {
		return result
	}
	return &model.DaemonQueryResult{}
}

func firstCmdDaemonAck(ack *model.DaemonCommandAck) *model.DaemonCommandAck {
	if ack != nil {
		return ack
	}
	return &model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived}
}
