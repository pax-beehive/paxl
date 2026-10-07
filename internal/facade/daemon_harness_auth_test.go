package facade_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestHarnessAuthStatusQueriesDaemonWithoutStartingLogin(t *testing.T) {
	calls := 0
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(
			t,
			"/v1/harnesses/claude/auth/status?session_id=session-1",
			r.URL.RequestURI(),
		)
		return daemonJSONResponse(
			t,
			http.StatusOK,
			json.RawMessage(
				`{"harness_auth":{"harness":"claude","session_id":"session-1","state":"succeeded","logged_in":true}}`,
			),
		), nil
	})
	f := facade.NewDaemonHarnessAuthFacade(facade.NewDaemonHTTPClient("http://paxd.test", client))
	view, err := f.Status(
		context.Background(),
		&facade.DaemonHarnessAuthStatusRequest{Harness: "claude-code", SessionID: "session-1"},
	)
	require.NoError(t, err)
	require.Equal(t, model.DaemonHarnessAuthSucceeded, view.State)
	require.NotNil(t, view.LoggedIn)
	require.True(t, *view.LoggedIn)
	require.Equal(t, 1, calls)
}

func TestHarnessLoginStartsOnceAndPollsUntilAuthorizationIsReady(t *testing.T) {
	starts, queries := 0, 0
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			starts++
			require.Equal(t, "/v1/harnesses/claude/auth/login", r.URL.Path)
			require.NotEmpty(t, r.Header.Get("X-Pax-Command-ID"))
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, map[string]string{"harness": "claude", "operation": "start"}, payload)
			return daemonJSONResponse(
				t,
				http.StatusAccepted,
				json.RawMessage(
					`{"ok":true,"result":{"harness_auth":{"harness":"claude","session_id":"login-1","state":"starting"}}}`,
				),
			), nil
		}
		queries++
		require.Equal(t, "login-1", r.URL.Query().Get("session_id"))
		return daemonJSONResponse(
			t,
			http.StatusOK,
			json.RawMessage(
				`{"harness_auth":{"harness":"claude","session_id":"login-1","state":"awaiting_code","authorization_url":"https://claude.com/cai/oauth/authorize?state=test"}}`,
			),
		), nil
	})
	f := facade.NewDaemonHarnessAuthFacade(facade.NewDaemonHTTPClient("http://paxd.test", client))
	view, err := f.Login(
		t.Context(),
		&facade.DaemonHarnessAuthLoginRequest{
			Harness:   "claude",
			Operation: model.DaemonHarnessAuthStart,
			Wait:      true,
		},
	)
	require.NoError(t, err)
	require.Equal(t, model.DaemonHarnessAuthAwaitingCode, view.State)
	require.NotEmpty(t, view.AuthorizationURL)
	require.Equal(t, 1, starts)
	require.Equal(t, 1, queries)
}

func TestHarnessLoginClientCancellationDoesNotCancelDaemonAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	posts := 0
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		posts++
		cancel()
		return daemonJSONResponse(
			t,
			http.StatusAccepted,
			json.RawMessage(
				`{"ok":true,"result":{"harness_auth":{"harness":"claude","session_id":"login-kept-alive","state":"starting"}}}`,
			),
		), nil
	})
	f := facade.NewDaemonHarnessAuthFacade(facade.NewDaemonHTTPClient("http://paxd.test", client))
	view, err := f.Login(
		ctx,
		&facade.DaemonHarnessAuthLoginRequest{
			Harness:   "claude",
			Operation: model.DaemonHarnessAuthStart,
			Wait:      true,
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "login-kept-alive", view.SessionID)
	require.Equal(t, 1, posts)
}

func TestHarnessAuthRejectsMissingAndUnknownDaemonResponses(t *testing.T) {
	for _, raw := range []string{`{}`, `{"harness_auth":{"harness":"claude","state":"new-state"}}`, `{"error":{"code":"not_found","message":"old daemon"}}`} {
		client := daemonRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return daemonJSONResponse(t, http.StatusOK, json.RawMessage(raw)), nil
		})
		f := facade.NewDaemonHarnessAuthFacade(
			facade.NewDaemonHTTPClient("http://paxd.test", client),
		)
		_, err := f.Status(t.Context(), &facade.DaemonHarnessAuthStatusRequest{Harness: "claude"})
		require.Error(t, err)
	}
}

func TestCodexLoginUsesCodexRouteAndReturnsBrowserChallenge(t *testing.T) {
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/harnesses/codex/auth/login", r.URL.Path)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "codex", payload["harness"])
		return daemonJSONResponse(
			t,
			http.StatusAccepted,
			json.RawMessage(
				`{"ok":true,"result":{"harness_auth":{"harness":"codex","state":"awaiting_browser","session_id":"device-1","authorization_url":"https://auth.openai.com/codex/device","user_code":"ABCD-EFGHJ"}}}`,
			),
		), nil
	})
	f := facade.NewDaemonHarnessAuthFacade(facade.NewDaemonHTTPClient("http://paxd.test", client))
	view, err := f.Login(
		t.Context(),
		&facade.DaemonHarnessAuthLoginRequest{
			Harness:   "codex",
			Operation: model.DaemonHarnessAuthStart,
			Wait:      true,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "awaiting_browser", string(view.State))
}

func TestAuthorizationCodeIsBoundToTheObservedAttempt(t *testing.T) {
	calls := 0
	client := daemonRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			require.Equal(t, http.MethodGet, r.Method)
			return daemonJSONResponse(
				t,
				200,
				json.RawMessage(
					`{"harness_auth":{"harness":"claude","session_id":"observed-attempt","state":"awaiting_code"}}`,
				),
			), nil
		}
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "observed-attempt", payload["session_id"])
		return daemonJSONResponse(
			t,
			200,
			json.RawMessage(
				`{"ok":true,"result":{"harness_auth":{"harness":"claude","session_id":"observed-attempt","state":"exchanging"}}}`,
			),
		), nil
	})
	f := facade.NewDaemonHarnessAuthFacade(facade.NewDaemonHTTPClient("http://paxd.test", client))
	_, err := f.Login(
		t.Context(),
		&facade.DaemonHarnessAuthLoginRequest{
			Harness:   "claude",
			Operation: model.DaemonHarnessAuthSubmit,
			Code:      "code#state",
		},
	)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}
