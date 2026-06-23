package adaptor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type HermesSuite struct {
	suite.Suite
}

func TestHermesSuite(t *testing.T) {
	suite.Run(t, new(HermesSuite))
}

func (s *HermesSuite) TestAdapterListsSessionsThroughPublicInterface() {
	withHermesHTTPClient(s.T(), func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/sessions", req.URL.Path)
		return hermesJSONResponse(http.StatusOK, `[
			{
				"sessionId":"sess-hermes",
				"agentType":"hermes",
				"nativeId":"resp-hermes",
				"name":"Mock Hermes",
				"lastActive":"2026-06-22T01:02:03Z",
				"preview":"Use paxl with Hermes",
				"status":"running",
				"currentTask":"Testing Hermes adapter",
				"updatedAt":"2026-06-22T01:02:03Z"
			}
		]`), nil
	})

	resp, err := NewHermesAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("hermes:sess-hermes", resp.Sessions[0].ID)
	s.Equal(model.AgentNameHermes, resp.Sessions[0].Agent)
	s.Equal("Mock Hermes", resp.Sessions[0].Title)
	s.Equal("2026-06-22T01:02:03Z", resp.Sessions[0].UpdatedAt)
}

func (s *HermesSuite) TestAdapterListsHermesHomeSessionsWhenHTTPIsOffline() {
	hermesHome := s.T().TempDir()
	sessionDir := filepath.Join(hermesHome, "sessions")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("HERMES_HOME", hermesHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "sess-local.jsonl"),
		[]byte(
			`{"sessionId":"sess-local","title":"Local Hermes","projectId":"/tmp/project","updatedAt":"2026-06-22T03:04:05Z"}`+"\n"+
				`{"role":"user","timestamp":"2026-06-22T03:04:01Z","content":"Use local Hermes history"}`+"\n"+
				`{"role":"assistant","timestamp":"2026-06-22T03:04:05Z","content":"Local response"}`+"\n",
		),
		0o600,
	))

	resp, err := NewHermesAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("hermes:sess-local", resp.Sessions[0].ID)
	s.Equal("Local Hermes", resp.Sessions[0].Title)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
	s.Equal("2026-06-22T03:04:05Z", resp.Sessions[0].UpdatedAt)
}

func (s *HermesSuite) TestAdapterGetsSessionThroughPublicInterface() {
	withHermesHTTPClient(s.T(), func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/sessions/sess-hermes", req.URL.Path)
		return hermesJSONResponse(http.StatusOK, `{
			"sessionId":"sess-hermes",
			"agentType":"hermes",
			"name":"Mock Hermes",
			"lastActive":"2026-06-22T01:02:03Z",
			"preview":"Use paxl with Hermes",
			"status":"running",
			"currentTask":"Testing Hermes adapter",
			"updatedAt":"2026-06-22T01:02:03Z"
		}`), nil
	})

	resp, err := NewHermesAdapter().GetSession(
		context.Background(),
		&GetSessionRequest{NativeID: "sess-hermes"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("assistant", resp.Elements[0].Role)
	s.Contains(resp.Elements[0].ContentText, "running")
	s.Contains(resp.Elements[0].ContentText, "Use paxl with Hermes")
}

func (s *HermesSuite) TestAdapterGetsHermesHomeSessionElementsWhenHTTPIsOffline() {
	hermesHome := s.T().TempDir()
	sessionDir := filepath.Join(hermesHome, "history")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("HERMES_HOME", hermesHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "sess-local.json"),
		[]byte(`{
			"sessionId":"sess-local",
			"title":"Local Hermes",
			"messages":[
				{"role":"user","timestamp":"2026-06-22T03:04:01Z","content":"Use local Hermes history"},
				{"role":"assistant","timestamp":"2026-06-22T03:04:05Z","content":"Local response"}
			]
		}`),
		0o600,
	))

	resp, err := NewHermesAdapter().GetSession(
		context.Background(),
		&GetSessionRequest{NativeID: "sess-local"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("user", resp.Elements[0].Role)
	s.Equal("Use local Hermes history", resp.Elements[0].ContentText)
	s.Equal("assistant", resp.Elements[1].Role)
	s.Equal("Local response", resp.Elements[1].ContentText)
}

func (s *HermesSuite) TestAdapterReadsHermesHomeJSONMessageArray() {
	hermesHome := s.T().TempDir()
	sessionDir := filepath.Join(hermesHome, "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("HERMES_HOME", hermesHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "sess-array.json"),
		[]byte(`[
			{"session_id":"sess-array","role":"user","timestamp":"2026-06-22T03:04:01Z","content":"Array user"},
			{"session_id":"sess-array","role":"assistant","timestamp":"2026-06-22T03:04:05Z","content":[{"text":"Array assistant"}]}
		]`),
		0o600,
	))

	resp, err := NewHermesAdapter().GetSession(
		context.Background(),
		&GetSessionRequest{NativeID: "sess-array"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("Array user", resp.Elements[0].ContentText)
	s.Equal("Array assistant", resp.Elements[1].ContentText)
}

func (s *HermesSuite) TestAdapterInfoShowsOfflineWithLocalSessionsWhenHTTPIsOffline() {
	hermesHome := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(hermesHome, "sessions"), 0o700))
	s.T().Setenv("HERMES_HOME", hermesHome)

	resp, err := NewHermesAdapter().Info(context.Background(), &InfoRequest{})

	s.Require().NoError(err)
	s.False(resp.Agent.Available)
	s.True(resp.Agent.SessionsAvailable)
}

func (s *HermesSuite) TestAdapterPromptsExistingSessionThroughHTTP() {
	var capturedSession string
	var capturedPrompt string
	withHermesHTTPClient(s.T(), func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/v1/chat/completions", req.URL.Path)
		capturedSession = req.Header.Get("X-Hermes-Session-Id")
		raw, err := io.ReadAll(req.Body)
		s.Require().NoError(err)
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		s.Require().NoError(json.Unmarshal(raw, &payload))
		s.Require().Len(payload.Messages, 1)
		capturedPrompt = payload.Messages[0].Content
		return hermesSSEResponse(), nil
	})

	resp, err := NewHermesAdapter().Prompt(
		context.Background(),
		&PromptRequest{NativeID: "sess-hermes", Text: "handoff"},
	)

	s.Require().NoError(err)
	s.Equal("hermes_http", resp.DeliveryMethod)
	s.Equal("sess-hermes", capturedSession)
	s.Equal("handoff", capturedPrompt)
}

func (s *HermesSuite) TestAdapterStartsNewSessionThroughHTTPWithoutSessionHeader() {
	var capturedSession string
	withHermesHTTPClient(s.T(), func(req *http.Request) (*http.Response, error) {
		capturedSession = req.Header.Get("X-Hermes-Session-Id")
		return hermesSSEResponse(), nil
	})

	resp, err := NewHermesAdapter().StartSession(
		context.Background(),
		&StartSessionRequest{Text: "new handoff"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	s.Empty(capturedSession)
}

func (s *HermesSuite) TestAdapterSendsHermesAPIKeyHeaders() {
	s.T().Setenv("PAXL_HERMES_API_KEY", "secret")
	var apiKey string
	var bearer string
	withHermesHTTPClient(s.T(), func(req *http.Request) (*http.Response, error) {
		apiKey = req.Header.Get("X-API-Key")
		bearer = req.Header.Get("Authorization")
		return hermesSSEResponse(), nil
	})

	_, err := NewHermesAdapter().Prompt(
		context.Background(),
		&PromptRequest{NativeID: "sess-hermes", Text: "handoff"},
	)

	s.Require().NoError(err)
	s.Equal("secret", apiKey)
	s.Equal("Bearer secret", bearer)
}

func withHermesHTTPClient(
	t *testing.T,
	roundTrip func(req *http.Request) (*http.Response, error),
) {
	t.Helper()
	original := hermesHTTPClient
	hermesHTTPClient = &http.Client{Transport: roundTripFunc(roundTrip)}
	t.Cleanup(func() {
		hermesHTTPClient = original
	})
	t.Setenv("PAXL_HERMES_URL", "http://hermes.test")
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func hermesJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func hermesSSEResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
				"data: [DONE]\n\n",
		)),
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
	}
}

func TestHermesUnavailableMatchesDefaultSessionListBehavior(t *testing.T) {
	original := hermesHTTPClient
	hermesHTTPClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial tcp [::1]:8642: %w", syscallEPERM())
		}),
	}
	t.Cleanup(func() {
		hermesHTTPClient = original
	})

	resp, err := NewHermesAdapter().ListSessions(context.Background(), &ListSessionsRequest{})

	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(resp.Sessions) != 0 {
		t.Fatalf("ListSessions() returned %d sessions, want 0", len(resp.Sessions))
	}
}

func syscallEPERM() error {
	return syscall.EPERM
}
