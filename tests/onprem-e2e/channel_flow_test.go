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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const onPremE2EBootstrapSecret = "e2e-bootstrap-secret"

func TestPaxlOnPremChannelFlow(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("TEAM_MEMORY_E2E_BASE_URL"), "/")
	paxlBinary := strings.TrimSpace(os.Getenv("PAXL_E2E_PAXL_BIN"))
	if baseURL == "" {
		t.Skip("TEAM_MEMORY_E2E_BASE_URL is not set")
	}
	if paxlBinary == "" {
		t.Skip("PAXL_E2E_PAXL_BIN is not set")
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
	root := t.TempDir()
	sender := cliAgent{
		binary: paxlBinary,
		db:     filepath.Join(root, "sender.db"),
		home:   filepath.Join(root, "sender-home"),
	}
	receiver := cliAgent{
		binary: paxlBinary,
		db:     filepath.Join(root, "receiver.db"),
		home:   filepath.Join(root, "receiver-home"),
	}

	senderProfile := firstJSONLine(t, sender.run(t, "", map[string]string{
		"PAXL_ENROLLMENT_TOKEN": senderToken,
	}, "channel", "connect", "onprem", "--url", baseURL, "--format", "jsonl"))
	receiverProfile := firstJSONLine(t, receiver.run(t, "", map[string]string{
		"PAXL_ENROLLMENT_TOKEN": receiverToken,
	}, "channel", "connect", "onprem", "--url", baseURL, "--format", "jsonl"))
	require.Equal(t, "paxl-channel-sender", requiredString(t, senderProfile, "agent_id"))
	receiverAgentID := requiredString(t, receiverProfile, "agent_id")
	require.Equal(t, "paxl-channel-receiver", receiverAgentID)
	require.NotContains(t, string(mustJSON(t, senderProfile)), "tm_key_")

	directory := jsonLines(t, sender.run(t, "", nil,
		"channel", "agents", "list", "--channel", "onprem", "--query", receiverAgentID,
		"--limit", "10", "--format", "jsonl"))
	require.Len(t, directory, 1)
	require.Equal(t, receiverAgentID, requiredString(t, directory[0], "agent_id"))
	fetchedAgent := firstJSONLine(t, sender.run(t, "", nil,
		"channel", "agents", "get", receiverAgentID, "--channel", "onprem", "--format", "jsonl"))
	require.Equal(t, receiverAgentID, requiredString(t, fetchedAgent, "agent_id"))

	created := firstJSONLine(t, sender.run(t, "", nil,
		"capsule", "create", "--manual", "--keyword", "onprem", "--title", "Paxl on-prem E2E",
		"--summary", "Public HTTP channel flow.", "--content",
		"The capsule crossed Team Memory and was materialized locally exactly once.",
		"--format", "jsonl"))
	capsuleID := requiredString(t, created, "capsuleId")
	sent := firstJSONLine(t, sender.run(t, "", nil,
		"capsule", "send", capsuleID, "--channel", "onprem", "--to-agent-id", receiverAgentID,
		"--message", "Review the on-prem handoff.", "--match", "project", "--project", "paxl",
		"--agent", "codex", "--format", "jsonl"))
	envelopeID := requiredString(t, sent, "envelopeId")
	require.Equal(t, "pending", requiredString(t, sent, "status"))

	inbox := jsonLines(t, receiver.run(t, "", nil,
		"inbox", "list", "--channel", "onprem", "--status", "pending", "--limit", "10",
		"--format", "jsonl"))
	require.Len(t, inbox, 1)
	require.Equal(t, envelopeID, requiredString(t, inbox[0], "envelopeId"))
	fetched := firstJSONLine(t, receiver.run(t, "", nil,
		"inbox", "get", envelopeID, "--channel", "onprem", "--format", "jsonl"))
	require.Equal(t, envelopeID, requiredString(t, fetched, "envelopeId"))

	firstAccepted := jsonLines(t, receiver.run(t, "", nil,
		"inbox", "accept", envelopeID, "--channel", "onprem", "--format", "jsonl"))
	require.Len(t, firstAccepted, 3)
	secondAccepted := jsonLines(t, receiver.run(t, "", nil,
		"inbox", "accept", envelopeID, "--channel", "onprem", "--format", "jsonl"))
	require.Len(t, secondAccepted, 3)
	require.Equal(
		t,
		requiredString(t, firstAccepted[1], "capsuleId"),
		requiredString(t, secondAccepted[1], "capsuleId"),
	)
	require.Equal(
		t,
		requiredString(t, firstAccepted[2], "injectionId"),
		requiredString(t, secondAccepted[2], "injectionId"),
	)
	sourceKey := "remote_envelope:onprem:" + requiredString(
		t,
		receiverProfile,
		"profile_id",
	) + ":" + envelopeID
	localCapsules := jsonLines(t, receiver.run(t, "", nil,
		"capsule", "list", "--source-session", sourceKey, "--format", "jsonl"))
	require.Len(t, localCapsules, 1)
	localInjections := jsonLines(t, receiver.run(t, "", nil,
		"capsule", "injection", "--format", "jsonl"))
	require.Len(t, localInjections, 1)
	require.Equal(t, sourceKey, requiredString(t, localInjections[0], "sourceSessionId"))

	receiver.run(t, "", nil, "inbox", "archive", envelopeID, "--channel", "onprem")
	outbox := jsonLines(t, sender.run(t, "", nil,
		"outbox", "list", "--channel", "onprem", "--status", "archived", "--limit", "10",
		"--format", "jsonl"))
	require.Len(t, outbox, 1)
	require.Equal(t, envelopeID, requiredString(t, outbox[0], "envelopeId"))
	outboxEnvelope := firstJSONLine(t, sender.run(t, "", nil,
		"outbox", "get", envelopeID, "--channel", "onprem", "--format", "jsonl"))
	require.Equal(t, "archived", requiredString(t, outboxEnvelope, "status"))
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

type cliAgent struct {
	binary string
	db     string
	home   string
}

func (a cliAgent) run(
	t *testing.T,
	stdin string,
	extraEnv map[string]string,
	args ...string,
) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(a.home, 0o700))
	commandArgs := append([]string{"--db", a.db}, args...)
	command := exec.CommandContext(context.Background(), a.binary, commandArgs...)
	command.Stdin = strings.NewReader(stdin)
	command.Env = append(os.Environ(), "HOME="+a.home, "XDG_DATA_HOME="+a.home)
	for key, value := range extraEnv {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	require.NoError(t, err, "paxl %s\nstderr: %s", strings.Join(args, " "), stderr.String())
	return stdout.String()
}

func jsonLines(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	results := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		value := make(map[string]any)
		require.NoError(t, json.Unmarshal([]byte(line), &value), line)
		results = append(results, value)
	}
	return results
}

func firstJSONLine(t *testing.T, output string) map[string]any {
	t.Helper()
	lines := jsonLines(t, output)
	require.NotEmpty(t, lines)
	return lines[0]
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
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
