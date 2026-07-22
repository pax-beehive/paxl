package facade

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/require"
)

func TestChannelConnectExchangesEnrollmentOnceAndStoresVerifiedIdentity(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	exchanges := 0
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/agent-enrollments/exchange":
			exchanges++
			body := decodeJSONBody(t, req)
			require.Equal(t, "tm_enroll_once", body["token"])
			return jsonResponse(`{"credential_id":"cred-1","api_key":"tm_key_secret"}`), nil
		case "/v1/agent-identity":
			require.Equal(t, "Bearer tm_key_secret", req.Header.Get("Authorization"))
			return jsonResponse(
				`{"user_id":"user-1","agent_id":"agent-1","credential_id":"cred-1","permissions":["channel_send","channel_receive"]}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	})
	facade := NewChannelFacade(client, opened.Store)

	connected, err := facade.Connect(ctx, &ConnectChannelRequest{
		Kind: "onprem", Name: "onprem", URL: "https://memory.internal",
		EnrollmentToken: "tm_enroll_once", AutoReceive: true,
	})

	require.NoError(t, err)
	require.Equal(t, 1, exchanges)
	require.Equal(t, "agent-1", connected.Profile.AgentID)
	require.NotEmpty(t, connected.Profile.ProfileID)
	encoded, err := json.Marshal(connected)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "tm_key_secret")
	require.NotContains(t, string(encoded), "tm_enroll_once")
	stored, err := opened.Store.GetChannelProfile(
		ctx,
		&store.GetChannelProfileRequest{Name: "onprem"},
	)
	require.NoError(t, err)
	require.Equal(t, "tm_key_secret", stored.Profile.APIKey)
}

func TestChannelConnectPersistsCredentialWhenIdentityVerificationFails(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/agent-enrollments/exchange" {
			return jsonResponse(`{"credential_id":"cred-1","api_key":"tm_key_saved"}`), nil
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	})

	_, err = NewChannelFacade(client, opened.Store).Connect(ctx, &ConnectChannelRequest{
		Kind: "onprem", Name: "onprem", URL: "https://memory.internal",
		EnrollmentToken: "tm_enroll_once", AutoReceive: true,
	})

	require.ErrorContains(t, err, "credential may be revoked or expired")
	require.NotContains(t, err.Error(), "tm_enroll_once")
	stored, getErr := opened.Store.GetChannelProfile(
		ctx,
		&store.GetChannelProfileRequest{Name: "onprem"},
	)
	require.NoError(t, getErr)
	require.Equal(t, "tm_key_saved", stored.Profile.APIKey)
}

func decodeJSONBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	defer func() { require.NoError(t, req.Body.Close()) }()
	var body map[string]any
	require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
	return body
}

func TestChannelConnectRejectsNonOriginURLAndMissingCA(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
		ca   string
		want string
	}{
		{name: "path", url: "https://memory.internal/api", want: "origin"},
		{name: "query", url: "https://memory.internal?token=secret", want: "origin"},
		{name: "missing ca", url: "https://memory.internal", ca: "/missing/ca.pem", want: "load CA file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewChannelFacade(
				http.DefaultClient,
				nil,
			).Connect(context.Background(), &ConnectChannelRequest{
				Kind: "onprem", Name: "onprem", URL: test.url, CAFile: test.ca,
				EnrollmentToken: "tm_enroll_once",
			})
			require.ErrorContains(t, err, test.want)
			require.False(t, strings.Contains(err.Error(), "tm_enroll_once"))
		})
	}
}

func TestChannelConnectTrustsExplicitCAFileWithoutDisablingVerification(t *testing.T) {
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Path == "/v1/agent-enrollments/exchange" {
				_, _ = io.WriteString(w, `{"credential_id":"cred","api_key":"tm_key"}`)
				return
			}
			_, _ = io.WriteString(
				w,
				`{"user_id":"user","agent_id":"agent","credential_id":"cred","permissions":["channel_receive"]}`,
			)
		}),
	)
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	}), 0o600))
	opened, err := store.Open(
		context.Background(),
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()

	_, err = NewChannelFacade(
		&http.Client{},
		opened.Store,
	).Connect(context.Background(), &ConnectChannelRequest{
		Kind:            "onprem",
		Name:            "onprem",
		URL:             server.URL,
		EnrollmentToken: "tm_enroll",
		CAFile:          caFile,
	})

	require.NoError(t, err)
}

func TestChannelDirectoryListAndGetUseOpaqueCursorAndBearerCredential(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	seedChannelProfile(ctx, t, opened.Store, "onprem", "chp_one", "tm_key_secret")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer tm_key_secret", req.Header.Get("Authorization"))
		switch req.URL.Path {
		case "/v1/channel/agents":
			require.Equal(t, "review", req.URL.Query().Get("q"))
			require.Equal(t, "17", req.URL.Query().Get("limit"))
			require.Equal(t, "opaque/+token==", req.URL.Query().Get("cursor"))
			return jsonResponse(
				`{"agents":[{"agent_id":"agent-review","display_name":"Reviewer","description":"reviews","agent_type":"codex"}],"next_cursor":"next/+opaque=="}`,
			), nil
		case "/v1/channel/agents/agent-review":
			return jsonResponse(
				`{"agent":{"agent_id":"agent-review","display_name":"Reviewer","description":"reviews","agent_type":"codex"}}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	})
	channelFacade := NewChannelFacade(client, opened.Store)

	listed, err := channelFacade.ListAgents(ctx, &ListDirectoryAgentsRequest{
		Channel: "onprem", Query: "review", Limit: 17, Cursor: "opaque/+token==",
	})
	require.NoError(t, err)
	require.Equal(t, "next/+opaque==", listed.NextCursor)
	require.Equal(t, "agent-review", listed.Agents[0].AgentID)
	got, err := channelFacade.GetAgent(
		ctx,
		&GetDirectoryAgentRequest{Channel: "onprem", AgentID: "agent-review"},
	)
	require.NoError(t, err)
	require.Equal(t, "Reviewer", got.Agent.DisplayName)
}

func TestOnPremPermissionErrorsAreActionable(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		permission string
		want       string
	}{
		{name: "revoked credential", status: http.StatusUnauthorized, want: "revoked or expired"},
		{name: "send permission", status: http.StatusForbidden, permission: "channel_send", want: "missing channel_send"},
		{name: "receive permission", status: http.StatusForbidden, permission: "channel_receive", want: "missing channel_receive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := onPremStatusError("test operation", test.permission, test.status)
			require.ErrorContains(t, err, test.want)
			if test.status == http.StatusForbidden {
				require.ErrorContains(t, err, "suspended")
			}
		})
	}
}

func seedChannelProfile(
	ctx context.Context,
	t *testing.T,
	sessionStore *store.Store,
	name string,
	profileID string,
	apiKey string,
) {
	t.Helper()
	_, err := sessionStore.SaveChannelProfile(
		ctx,
		&store.SaveChannelProfileRequest{Profile: &model.ChannelProfile{
			ProfileID:    profileID,
			Name:         name,
			Kind:         model.ChannelKindOnPrem,
			URL:          "https://memory.internal",
			APIKey:       apiKey,
			AgentID:      "sender",
			UserID:       "user",
			CredentialID: "cred",
			Permissions:  []string{"channel_send", "channel_receive"},
			Enabled:      true,
			AutoReceive:  true,
		}},
	)
	require.NoError(t, err)
}
