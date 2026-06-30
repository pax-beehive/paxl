package facade_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonLocalAPIClientUsesExpectedQueryRoutes(t *testing.T) {
	var seen []string
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		status := http.StatusOK
		var body any
		switch r.URL.Path {
		case "/v1/status":
			body = model.DaemonQueryResult{Status: &model.DaemonStatus{Phase: "running"}}
		case "/v1/remotes":
			body = model.DaemonQueryResult{Remotes: &model.DaemonListRemotesResult{}}
		case "/v1/agent-connections":
			body = model.DaemonQueryResult{
				AgentConnections: &model.DaemonListAgentConnectionsResult{},
			}
		case "/v1/harnesses":
			body = model.DaemonQueryResult{Harnesses: &model.DaemonListHarnessesResult{}}
		case "/v1/harnesses/discover":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, true, body["probe"])
			return daemonJSONResponse(
				t,
				status,
				model.DaemonQueryResult{Harnesses: &model.DaemonListHarnessesResult{}},
			), nil
		case "/v1/local/overview":
			body = model.DaemonQueryResult{LocalOverview: &model.DaemonLocalOverview{}}
		case "/v1/local/sessions/sync":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "codex", body["agent"])
			return daemonJSONResponse(
				t,
				status,
				model.DaemonQueryResult{
					LocalSessionSync: &model.DaemonLocalSessionSyncResult{Synced: 1},
				},
			), nil
		case "/v1/local/sessions":
			if r.Method == http.MethodGet {
				body = model.DaemonQueryResult{
					LocalSessions: &model.DaemonListLocalSessionsResult{},
				}
				break
			}
		default:
			t.Fatalf("unexpected route %s %s", r.Method, r.URL.RequestURI())
		}
		return daemonJSONResponse(t, status, body), nil
	})

	api := facade.NewDaemonHTTPClient("https://paxd.test", client)
	_, err := api.GetStatus(context.Background())
	require.NoError(t, err)
	_, err = api.ListRemotes(context.Background(), true)
	require.NoError(t, err)
	_, err = api.ListAgentConnections(context.Background(), true)
	require.NoError(t, err)
	_, err = api.ListHarnesses(context.Background(), true)
	require.NoError(t, err)
	_, err = api.DiscoverHarnesses(context.Background(), true, []string{"codex"})
	require.NoError(t, err)
	_, err = api.GetLocalOverview(context.Background())
	require.NoError(t, err)
	_, err = api.ListLocalSessions(context.Background(), "claude", 7)
	require.NoError(t, err)
	_, err = api.SyncLocalSessions(context.Background(), "codex", 2, 3000)
	require.NoError(t, err)

	assert.Contains(t, seen, "GET /v1/status")
	assert.Contains(t, seen, "GET /v1/remotes?include_disabled=true")
	assert.Contains(t, seen, "GET /v1/agent-connections?include_disabled=true")
	assert.Contains(t, seen, "GET /v1/harnesses?include_missing=true")
	assert.Contains(t, seen, "POST /v1/harnesses/discover")
	assert.Contains(t, seen, "GET /v1/local/overview")
	assert.Contains(t, seen, "GET /v1/local/sessions?agent=claude&limit=7")
	assert.Contains(t, seen, "POST /v1/local/sessions/sync")
}

func TestDaemonLocalAPIClientUsesExpectedCommandRoutes(t *testing.T) {
	var seen []string
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		assert.NotEmpty(t, r.Header.Get("X-Pax-Command-ID"))
		return daemonJSONResponse(
			t,
			http.StatusOK,
			model.DaemonCommandAck{OK: true, Status: model.DaemonCommandStatusReceived},
		), nil
	})

	api := facade.NewDaemonHTTPClient("https://paxd.test", client)
	_, err := api.CreateRemote(context.Background(), "cmd_1", &model.DaemonCreateRemoteCommand{
		Remote: model.DaemonRemote{ID: "prod", CloudAPIURL: "https://api.test"},
	})
	require.NoError(t, err)
	_, err = api.UpdateRemote(
		context.Background(),
		"cmd_2",
		"prod",
		&model.DaemonUpdateRemoteCommand{},
	)
	require.NoError(t, err)
	_, err = api.RestartRemote(context.Background(), "cmd_3", "prod")
	require.NoError(t, err)
	_, err = api.DeleteRemote(context.Background(), "cmd_4", "prod", true)
	require.NoError(t, err)
	_, err = api.CreateAgentConnection(
		context.Background(),
		"cmd_5",
		&model.DaemonCreateAgentConnectionCommand{Name: "work"},
	)
	require.NoError(t, err)
	_, err = api.UpdateAgentConnection(
		context.Background(),
		"cmd_6",
		"conn_work",
		&model.DaemonUpdateAgentConnectionCommand{},
	)
	require.NoError(t, err)
	_, err = api.RestartAgentConnection(context.Background(), "cmd_7", "conn_work")
	require.NoError(t, err)
	_, err = api.DeleteAgentConnection(context.Background(), "cmd_8", "conn_work")
	require.NoError(t, err)

	assert.Equal(t, []string{
		"POST /v1/remotes",
		"PATCH /v1/remotes/prod",
		"POST /v1/remotes/prod/restart",
		"DELETE /v1/remotes/prod?cascade_agent_connections=true",
		"POST /v1/agent-connections",
		"PATCH /v1/agent-connections/conn_work",
		"POST /v1/agent-connections/conn_work/restart",
		"DELETE /v1/agent-connections/conn_work",
	}, seen)
}

func TestDaemonLocalAPIClientReturnsHTTPStatusErrors(t *testing.T) {
	client := daemonRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return daemonJSONResponse(t, http.StatusServiceUnavailable, model.DaemonQueryResult{
			Error: &model.DaemonControlError{Code: "unavailable"},
		}), nil
	})

	_, err := facade.NewDaemonHTTPClient("https://paxd.test", client).
		GetStatus(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestDaemonClientConstructorsUseDefaults(t *testing.T) {
	t.Setenv("HOME", "/tmp/pax-home")

	assert.Contains(t, facade.DefaultDaemonControlSocketPath(), ".paxd/paxd.sock")
	assert.NotNil(t, facade.NewDaemonUnixClient(""))
	assert.NotNil(t, facade.NewDaemonHTTPClient("https://paxd.test/", nil))
	assert.NotNil(t, facade.NewDaemonFacade(nil))
}

type daemonRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f daemonRoundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func daemonJSONResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	var body bytes.Buffer
	require.NoError(t, json.NewEncoder(&body).Encode(value))
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body.Bytes())),
	}
}
