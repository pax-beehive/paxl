package onpreme2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/require"
)

const onPremE2EBootstrapSecret = "e2e-bootstrap-secret"

func TestPaxlOnPremChannelFlow(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("TEAM_MEMORY_E2E_BASE_URL"), "/")
	if baseURL == "" {
		t.Skip("TEAM_MEMORY_E2E_BASE_URL is not set")
	}
	client := newHumanClient(t)
	waitForTeamMemory(t, client, baseURL)
	loginRequest, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		baseURL+"/v1/auth/login", nil)
	require.NoError(t, err)
	login, err := client.Do(loginRequest)
	require.NoError(t, err)
	require.NoError(t, login.Body.Close())
	humanRequest(t, client, baseURL, http.MethodPost, "/v1/bootstrap/claim", nil, map[string]string{
		"X-PAX-Bootstrap-Secret": onPremE2EBootstrapSecret,
	})

	senderToken := createEnrollment(
		t,
		client,
		baseURL,
		"paxl-channel-sender",
		[]string{"channel_send"},
	)
	receiverToken := createEnrollment(
		t,
		client,
		baseURL,
		"paxl-channel-receiver",
		[]string{"channel_receive"},
	)
	senderStore := openTestStore(t, "sender.db")
	receiverStore := openTestStore(t, "receiver.db")

	senderProfile := connectAgent(t, client, senderStore, baseURL, senderToken)
	receiverProfile := connectAgent(t, client, receiverStore, baseURL, receiverToken)
	require.Equal(t, "paxl-channel-sender", senderProfile.AgentID)
	require.Equal(t, "paxl-channel-receiver", receiverProfile.AgentID)

	directory := facade.NewChannelFacade(client, senderStore)
	agents, err := directory.ListAgents(context.Background(), &facade.ListDirectoryAgentsRequest{
		Channel: "onprem", Query: receiverProfile.AgentID, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, agents.Agents, 1)
	require.Equal(t, receiverProfile.AgentID, agents.Agents[0].AgentID)
	fetchedAgent, err := directory.GetAgent(context.Background(), &facade.GetDirectoryAgentRequest{
		Channel: "onprem", AgentID: receiverProfile.AgentID,
	})
	require.NoError(t, err)
	require.Equal(t, receiverProfile.AgentID, fetchedAgent.Agent.AgentID)

	capsule, err := facade.NewCapsuleFacade(nil, senderStore).Create(
		context.Background(),
		&facade.CreateCapsuleRequest{
			Keyword: "onprem",
			Title:   "Paxl on-prem E2E",
			Summary: "Public HTTP channel flow.",
			Content: "The capsule crossed Team Memory and was materialized locally exactly once.",
			Manual:  true,
		},
	)
	require.NoError(t, err)

	senderEnvelopes := facade.NewEnvelopeFacade(client, senderStore)
	sent, err := senderEnvelopes.Send(context.Background(), &facade.SendEnvelopeRequest{
		Channel: "onprem", CapsuleID: capsule.Capsule.CapsuleID,
		ToAgentID: receiverProfile.AgentID, Message: "Review the on-prem handoff.",
		MatchType: "project", MatchValue: "paxl", TargetAgent: model.AgentNameCodex,
		IdempotencyKey: "paxl-onprem-e2e-send-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, sent.Envelope.EnvelopeID)
	require.Equal(t, "pending", sent.Envelope.Status)

	receiverEnvelopes := facade.NewEnvelopeFacade(client, receiverStore)
	inbox, err := receiverEnvelopes.ListInbox(context.Background(), &facade.ListInboxRequest{
		Channel: "onprem", Status: "pending", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, inbox.Envelopes, 1)
	require.Equal(t, sent.Envelope.EnvelopeID, inbox.Envelopes[0].EnvelopeID)
	fetched, err := receiverEnvelopes.Get(context.Background(), &facade.GetEnvelopeRequest{
		Channel: "onprem", EnvelopeID: sent.Envelope.EnvelopeID,
	})
	require.NoError(t, err)
	require.Equal(t, sent.Envelope.EnvelopeID, fetched.Envelope.EnvelopeID)

	firstAccepted, err := receiverEnvelopes.Accept(
		context.Background(),
		&facade.AcceptEnvelopeRequest{
			Channel: "onprem", EnvelopeID: sent.Envelope.EnvelopeID,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, firstAccepted.Capsule)
	require.NotNil(t, firstAccepted.Injection)
	require.Equal(t, "pending", firstAccepted.Injection.Status)
	secondAccepted, err := receiverEnvelopes.Accept(
		context.Background(),
		&facade.AcceptEnvelopeRequest{
			Channel: "onprem", EnvelopeID: sent.Envelope.EnvelopeID,
		},
	)
	require.NoError(t, err)
	require.Equal(t, firstAccepted.Capsule.CapsuleID, secondAccepted.Capsule.CapsuleID)
	require.Equal(t, firstAccepted.Injection.InjectionID, secondAccepted.Injection.InjectionID)
	assertSingleMaterialization(t, receiverStore)

	archived, err := receiverEnvelopes.Archive(context.Background(), &facade.ArchiveEnvelopeRequest{
		Channel: "onprem", EnvelopeID: sent.Envelope.EnvelopeID,
	})
	require.NoError(t, err)
	require.Equal(t, "archived", archived.Envelope.Status)
	outbox, err := senderEnvelopes.ListOutbox(context.Background(), &facade.ListOutboxRequest{
		Channel: "onprem", Status: "archived", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, outbox.Envelopes, 1)
	require.Equal(t, sent.Envelope.EnvelopeID, outbox.Envelopes[0].EnvelopeID)
}

func newHumanClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Timeout: 5 * time.Second, Jar: jar}
}

func waitForTeamMemory(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			baseURL+"/healthz", nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		return response.Body.Close() == nil && response.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond)
}

func createEnrollment(
	t *testing.T,
	client *http.Client,
	baseURL string,
	agentID string,
	permissions []string,
) string {
	t.Helper()
	humanRequest(t, client, baseURL, http.MethodPost, "/v1/me/agents", map[string]any{
		"agent_id": agentID, "display_name": agentID, "description": "paxl on-prem E2E agent",
		"agent_type": "test", "directory_visible": true,
	}, map[string]string{"Idempotency-Key": "create-" + agentID})
	enrollment := humanRequest(t, client, baseURL, http.MethodPost,
		"/v1/me/agents/"+agentID+"/enrollments", map[string]any{
			"credential_label": "paxl-e2e", "permissions": permissions, "expires_in_seconds": 300,
		}, nil)
	return requiredString(t, enrollment, "token")
}

func connectAgent(
	t *testing.T,
	client *http.Client,
	sessionStore *store.Store,
	baseURL string,
	token string,
) *model.ChannelProfile {
	t.Helper()
	connected, err := facade.NewChannelFacade(client, sessionStore).Connect(
		context.Background(),
		&facade.ConnectChannelRequest{
			Kind: "onprem", Name: "onprem", URL: baseURL,
			EnrollmentToken: token, AutoReceive: true,
		},
	)
	require.NoError(t, err)
	return connected.Profile
}

func openTestStore(t *testing.T, name string) *store.Store {
	t.Helper()
	opened, err := store.Open(
		context.Background(),
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), name)},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Store.Close()) })
	return opened.Store
}

func assertSingleMaterialization(t *testing.T, sessionStore *store.Store) {
	t.Helper()
	capsules, err := sessionStore.ListKnowledgeCapsules(
		context.Background(), &store.ListKnowledgeCapsulesRequest{},
	)
	require.NoError(t, err)
	require.Len(t, capsules.Capsules, 1)
	injections, err := sessionStore.ListKnowledgeInjections(
		context.Background(), &store.ListKnowledgeInjectionsRequest{},
	)
	require.NoError(t, err)
	require.Len(t, injections.Injections, 1)
}

func humanRequest(
	t *testing.T,
	client *http.Client,
	baseURL string,
	method string,
	path string,
	body any,
	headers map[string]string,
) map[string]any {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		method,
		baseURL+path,
		requestBody,
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("X-CSRF-Token", cookieValue(t, client, baseURL, "tm_csrf"))
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.GreaterOrEqual(t, response.StatusCode, http.StatusOK, string(responseBody))
	require.Less(t, response.StatusCode, http.StatusMultipleChoices, string(responseBody))
	result := make(map[string]any)
	require.NoError(
		t,
		json.Unmarshal(responseBody, &result),
		fmt.Sprintf("decode %s %s", method, path),
	)
	return result
}

func cookieValue(t *testing.T, client *http.Client, baseURL string, name string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	t.Fatalf("cookie %s is missing", name)
	return ""
}

func requiredString(t *testing.T, value map[string]any, name string) string {
	t.Helper()
	result, ok := value[name].(string)
	require.True(t, ok, "%s is not a string", name)
	require.NotEmpty(t, result)
	return result
}
