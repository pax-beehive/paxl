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

func (s *HermesSuite) TestAdapterListsSessionsThroughACPBeforeHTTP() {
	logPath := s.installHermesACPFake(hermesACPFakeOptions{})
	withFailingHermesHTTPClient(s.T())

	resp, err := NewHermesAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("hermes:acp-session", resp.Sessions[0].ID)
	s.Equal("ACP Hermes", resp.Sessions[0].Title)
	s.Equal("/tmp/acp", resp.Sessions[0].ProjectID)
	s.Contains(s.readFile(logPath), `"method":"session/list"`)
}

func (s *HermesSuite) TestAdapterListsACPSessionsWithSnakeCaseTimestamps() {
	s.installHermesACPFake(hermesACPFakeOptions{
		sessionsJSON: `[{"session_id":"acp-session","title":"ACP Hermes","cwd":"/tmp/acp","updated_at":"2026-06-22T04:05:06Z","last_active":"2026-06-22T04:05:00Z"}]`,
	})
	withFailingHermesHTTPClient(s.T())

	resp, err := NewHermesAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("2026-06-22T04:05:06Z", resp.Sessions[0].UpdatedAt)
	s.Equal("2026-06-22T04:05:00Z", resp.Sessions[0].LastActive)
}

func (s *HermesSuite) TestAdapterListsACPSessionsWithNumericTimestamps() {
	s.installHermesACPFake(hermesACPFakeOptions{
		sessionsJSON: `[{"sessionId":"acp-session","title":"ACP Hermes","cwd":"/tmp/acp","updatedAt":1781999813,"lastActive":1781999800}]`,
	})
	withFailingHermesHTTPClient(s.T())

	resp, err := NewHermesAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("2026-06-20T23:56:53Z", resp.Sessions[0].UpdatedAt)
	s.Equal("2026-06-20T23:56:40Z", resp.Sessions[0].LastActive)
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

func (s *HermesSuite) TestAdapterListsHermesHomeSessionsWithNumericTimestamps() {
	hermesHome := s.T().TempDir()
	sessionDir := filepath.Join(hermesHome, "sessions")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("HERMES_HOME", hermesHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "sess-local.jsonl"),
		[]byte(
			`{"sessionId":"sess-local","title":"Local Hermes","projectId":"/tmp/project","updatedAt":1781999813}`+"\n",
		),
		0o600,
	))

	resp, err := NewHermesAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("2026-06-20T23:56:53Z", resp.Sessions[0].UpdatedAt)
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

func (s *HermesSuite) TestAdapterInfoShowsOnlineWhenACPInitializes() {
	s.installHermesACPFake(hermesACPFakeOptions{})
	withFailingHermesHTTPClient(s.T())

	resp, err := NewHermesAdapter().Info(context.Background(), &InfoRequest{})

	s.Require().NoError(err)
	s.True(resp.Agent.Available)
	s.True(resp.Agent.CLIAvailable)
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

func (s *HermesSuite) TestAdapterPromptsExistingSessionThroughACPBeforeHTTP() {
	logPath := s.installHermesACPFake(hermesACPFakeOptions{})
	withFailingHermesHTTPClient(s.T())

	resp, err := NewHermesAdapter().Prompt(
		context.Background(),
		&PromptRequest{NativeID: "acp-session", Text: "handoff"},
	)

	s.Require().NoError(err)
	s.Equal("acp_session_prompt", resp.DeliveryMethod)
	logText := s.readFile(logPath)
	s.Contains(logText, `"method":"session/prompt"`)
	s.Contains(logText, `"sessionId":"acp-session"`)
	s.Contains(logText, "handoff")
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

func withFailingHermesHTTPClient(t *testing.T) {
	t.Helper()
	withHermesHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected HTTP request to %s", req.URL.String())
	})
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

func TestHermesNumericTimestampSupportsAppleEpochSeconds(t *testing.T) {
	got := hermesNumericTimestamp(801371386)
	want := "2026-05-25T03:09:46Z"
	if got != want {
		t.Fatalf("hermesNumericTimestamp() = %q, want %q", got, want)
	}
}

func TestHermesNumericTimestampSupportsUnixMillisAndNanos(t *testing.T) {
	cases := map[string]struct {
		raw  float64
		want string
	}{
		"millis": {raw: 1781999813000, want: "2026-06-20T23:56:53Z"},
		"nanos":  {raw: 1781999813000000000, want: "2026-06-20T23:56:53Z"},
		"zero":   {raw: 0, want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := hermesNumericTimestamp(tc.raw)
			if got != tc.want {
				t.Fatalf("hermesNumericTimestamp() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHermesMapValueHelpersHandleNumericAndSliceValues(t *testing.T) {
	fields := map[string]any{
		"id":             float64(123),
		"updatedAt":      float64(1781999813),
		"workspaceRoots": []any{"/tmp/project", "", 42},
	}

	if got := stringMapValue(fields, "id"); got != "123" {
		t.Fatalf("stringMapValue() = %q, want 123", got)
	}
	if got := timestampMapValue(fields, "updatedAt"); got != "2026-06-20T23:56:53Z" {
		t.Fatalf("timestampMapValue() = %q", got)
	}
	roots := stringSliceMapValue(fields, "workspaceRoots")
	if len(roots) != 1 || roots[0] != "/tmp/project" {
		t.Fatalf("stringSliceMapValue() = %#v", roots)
	}
}

func syscallEPERM() error {
	return syscall.EPERM
}

type hermesACPFakeOptions struct {
	sessionsJSON string
}

func (s *HermesSuite) installHermesACPFake(options hermesACPFakeOptions) string {
	dir := s.T().TempDir()
	logPath := filepath.Join(dir, "acp.jsonl")
	scriptPath := filepath.Join(dir, "hermes")
	sessionsJSON := options.sessionsJSON
	if sessionsJSON == "" {
		sessionsJSON = `[{"sessionId":"acp-session","title":"ACP Hermes","cwd":"/tmp/acp","updatedAt":"2026-06-22T04:05:06Z"}]`
	}
	s.Require().NoError(os.WriteFile(
		scriptPath,
		[]byte(fmt.Sprintf(`#!/bin/sh
if [ "$1" != "acp" ]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
while IFS= read -r line; do
  printf '%%s\n' "$line" >> "$PAXL_TEST_HERMES_ACP_LOG"
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":1,"result":{"authMethods":[]}}\n'
      ;;
    *'"method":"session/list"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{"sessions":%s}}\n'
      ;;
    *'"method":"session/prompt"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{}}\n'
      ;;
  esac
done
`, sessionsJSON)),
		0o700,
	))
	s.T().Setenv("PAXL_TEST_HERMES_ACP_LOG", logPath)
	original := hermesACPCommand
	hermesACPCommand = []string{scriptPath, "acp"}
	s.T().Cleanup(func() {
		hermesACPCommand = original
	})
	return logPath
}

func (s *HermesSuite) readFile(path string) string {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	return string(raw)
}
