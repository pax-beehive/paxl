package adaptor

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type LocalSessionsSuite struct {
	suite.Suite
}

func TestLocalSessionsSuite(t *testing.T) {
	suite.Run(t, new(LocalSessionsSuite))
}

func (s *LocalSessionsSuite) TestListCodexSessionsReadsIndexAndRollouts() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(
			`{"id":"sess-index","thread_name":"Indexed","updated_at":"2026-06-20T01:00:00Z"}`+"\n",
		),
		0o600,
	))
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-rollout.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"sess-rollout","timestamp":"2026-06-20T02:00:00Z","cwd":"/tmp/project"}}`+"\n",
		),
		0o600,
	))

	resp, err := listCodexSessions(context.Background(), &ListSessionsRequest{Limit: 1})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("codex:sess-rollout", resp.Sessions[0].ID)
	s.Equal(model.AgentNameCodex, resp.Sessions[0].Agent)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
}

func (s *LocalSessionsSuite) TestListCodexSessionsReturnsEmptyWhenLocalRootIsMissing() {
	s.T().Setenv("CODEX_HOME", filepath.Join(s.T().TempDir(), "missing"))

	resp, err := listCodexSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Empty(resp.Sessions)
}

func (s *LocalSessionsSuite) TestListCodexSessionsDerivesReadableTitleFromRollout() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-rollout.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"sess-rollout","timestamp":"2026-06-20T02:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T02:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /tmp/project\nIgnore this bootstrap context."}]}}`+"\n"+
				`{"timestamp":"2026-06-20T02:02:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/tmp/project</cwd>\n</environment_context>"}]}}`+"\n"+
				`{"timestamp":"2026-06-20T02:03:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history whose request action you are assessing."}]}}`+"\n"+
				`{"timestamp":"2026-06-20T02:04:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history added since your last approval assessment."}]}}`+"\n"+
				`{"timestamp":"2026-06-20T02:05:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Make the session title readable from user intent."}]}}`+"\n",
		),
		0o600,
	))

	resp, err := listCodexSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("Make the session title readable from user intent.", resp.Sessions[0].Title)
}

func (s *LocalSessionsSuite) TestListCodexSessionsPrefersStructuredUserMessageEventForTitle() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-rollout.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"sess-rollout","timestamp":"2026-06-20T02:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T02:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history added since your last approval assessment."}]}}`+"\n"+
				`{"timestamp":"2026-06-20T02:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"Use a structured source for Codex titles.","images":[],"local_images":[],"text_elements":[]}}`+"\n",
		),
		0o600,
	))

	resp, err := listCodexSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("Use a structured source for Codex titles.", resp.Sessions[0].Title)
}

func (s *LocalSessionsSuite) TestListCodexSessionsUsesProjectNameInsteadOfRolloutFileName() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(
			rolloutDir,
			"rollout-2026-06-20T18-05-45-019ee7b6-5192-7393-9ba9-968eeedf4760.jsonl",
		),
		[]byte(
			`{"type":"session_meta","payload":{"id":"019ee7b6-5192-7393-9ba9-968eeedf4760","timestamp":"2026-06-21T01:05:45.925Z","cwd":"/tmp/paxl"}}`+"\n"+
				`{"timestamp":"2026-06-21T01:05:45.925Z","type":"event_msg","payload":{"type":"user_message","message":"The following is the Codex agent history added since your last approval assessment. Continue the same review conversation.","images":[],"local_images":[],"text_elements":[]}}`+"\n",
		),
		0o600,
	))

	resp, err := listCodexSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("paxl", resp.Sessions[0].Title)
}

func (s *LocalSessionsSuite) TestListClaudeSessionsReadsProjectLogs() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":[{"type":"text","text":"Claude title"}]}}`+"\n"+
				`{"type":"assistant","sessionId":"claude-session","timestamp":"2026-06-20T02:01:00Z","cwd":"/tmp/project","message":{"role":"assistant","content":"Reply"}}`+"\n",
		),
		0o600,
	))

	resp, err := listClaudeSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("claude:claude-session", resp.Sessions[0].ID)
	s.Equal(model.AgentNameClaude, resp.Sessions[0].Agent)
	s.Equal("Claude title", resp.Sessions[0].Title)
	s.Equal("2026-06-20T02:01:00Z", resp.Sessions[0].UpdatedAt)
}

func (s *LocalSessionsSuite) TestListClaudeSessionsReturnsEmptyWhenLocalRootIsMissing() {
	s.T().Setenv("CLAUDE_HOME", filepath.Join(s.T().TempDir(), "missing"))

	resp, err := listClaudeSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Empty(resp.Sessions)
}

func (s *LocalSessionsSuite) TestListClaudeSessionsDerivesProjectFromDirectory() {
	claudeHome := s.T().TempDir()
	projectPath := filepath.Join(s.T().TempDir(), "Application Support", "pax-oss", "paxl")
	s.Require().NoError(os.MkdirAll(projectPath, 0o700))
	encodedProject := strings.ReplaceAll(projectPath, string(os.PathSeparator), "-")
	projectDir := filepath.Join(claudeHome, "projects", encodedProject)
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","message":{"role":"user","content":"Claude title"}}`+"\n",
		),
		0o600,
	))

	resp, err := listClaudeSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal(projectPath, resp.Sessions[0].ProjectID)
}

func (s *LocalSessionsSuite) TestListClaudeSessionsSkipsCaveatTitleNoise() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","message":{"role":"user","content":"<local-command-caveat>Caveat: ignore this command context.</local-command-caveat>"}}`+"\n"+
				`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:01:00Z","message":{"role":"user","content":"<command-name>/usage</command-name>\n<command-message>usage</command-message>"}}`+"\n",
		),
		0o600,
	))

	resp, err := listClaudeSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("/usage", resp.Sessions[0].Title)
}

func (s *LocalSessionsSuite) TestListClaudeSessionsUsesLatestNonEmptyTimestamp() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"last-prompt","sessionId":"claude-session"}`+"\n"+
				`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","message":{"role":"user","content":"Review this repo"}}`+"\n"+
				`{"type":"assistant","sessionId":"claude-session","timestamp":"2026-06-20T02:05:00Z","message":{"role":"assistant","content":"Reviewed."}}`+"\n"+
				`{"type":"mode","mode":"normal","sessionId":"claude-session"}`+"\n",
		),
		0o600,
	))

	resp, err := listClaudeSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("Review this repo", resp.Sessions[0].Title)
	s.Equal("2026-06-20T02:05:00Z", resp.Sessions[0].UpdatedAt)
}

func (s *LocalSessionsSuite) TestClaudeSessionReadsLongJSONLLines() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	longText := strings.Repeat("x", 70*1024)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"assistant","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","message":{"role":"assistant","content":"`+longText+`"}}`+"\n",
		),
		0o600,
	))

	resp, err := getClaudeSession(
		context.Background(),
		&GetSessionRequest{NativeID: "claude-session"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Len(resp.Elements[0].ContentText, len(longText))
}

func (s *LocalSessionsSuite) TestClaudeMalformedContentKeepsRawContentText() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"assistant","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","message":{"role":"assistant","content":{"unexpected":"shape"}}}`+"\n",
		),
		0o600,
	))

	resp, err := getClaudeSession(
		context.Background(),
		&GetSessionRequest{NativeID: "claude-session"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Contains(resp.Elements[0].ContentText, `"unexpected":"shape"`)
}

func (s *LocalSessionsSuite) TestListPiSessionsReturnsEmptyWhenLocalRootIsMissing() {
	s.T().Setenv("PI_CODING_AGENT_DIR", filepath.Join(s.T().TempDir(), "missing"))
	s.T().Setenv("PAXL_PI_BRIDGE_DIR", filepath.Join(s.T().TempDir(), "missing-bridge"))

	resp, err := listPiSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Empty(resp.Sessions)
}

func (s *LocalSessionsSuite) TestListPiSessionsIncludesActiveBridge() {
	if runtime.GOOS == "windows" {
		s.T().Skip("unix sockets are unavailable on windows")
	}
	bridgeDir := s.shortTempDir()
	socketPath := filepath.Join(bridgeDir, "active.sock")
	s.T().Setenv("PAXL_PI_BRIDGE_DIR", bridgeDir)
	s.T().Setenv("PI_CODING_AGENT_DIR", filepath.Join(s.T().TempDir(), "missing"))
	listener := s.listenUnix(socketPath)
	defer closeListener(listener)
	s.writePiBridgeRecord(bridgeDir, piBridgeRecord{
		SessionID:  "pi-active",
		CWD:        "/tmp/project",
		Title:      "Active Pi",
		SocketPath: socketPath,
		PID:        os.Getpid(),
		UpdatedAt:  "2026-06-24T04:00:00Z",
	})

	resp, err := listPiSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("pi:pi-active", resp.Sessions[0].ID)
	s.Equal("online", resp.Sessions[0].Status)
	s.Equal("Active Pi", resp.Sessions[0].Title)
}

func (s *LocalSessionsSuite) TestPromptPiSessionUsesActiveBridgeBeforeCLI() {
	if runtime.GOOS == "windows" {
		s.T().Skip("unix sockets are unavailable on windows")
	}
	bridgeDir := s.shortTempDir()
	socketPath := filepath.Join(bridgeDir, "active.sock")
	s.T().Setenv("PAXL_PI_BRIDGE_DIR", bridgeDir)
	s.T().Setenv("PATH", s.T().TempDir())
	listener := s.listenUnix(socketPath)
	defer closeListener(listener)
	s.writePiBridgeRecord(bridgeDir, piBridgeRecord{
		SessionID:  "pi-active",
		CWD:        "/tmp/project",
		Title:      "Active Pi",
		SocketPath: socketPath,
		PID:        os.Getpid(),
		UpdatedAt:  "2026-06-24T04:00:00Z",
	})
	requests := make(chan map[string]any, 1)
	go serveOnePiBridgeRequest(listener, requests)

	resp, err := promptPiSession(
		context.Background(),
		&PromptRequest{NativeID: "pi-active", Text: "handoff text"},
		&Option{},
	)

	s.Require().NoError(err)
	s.Equal("pi_extension_steer", resp.DeliveryMethod)
	request := <-requests
	s.Equal("session/prompt", request["method"])
	params, ok := request["params"].(map[string]any)
	s.Require().True(ok)
	s.Equal("pi-active", params["sessionId"])
	s.Equal("steer", params["delivery"])
}

func (s *LocalSessionsSuite) TestInstallPiBridgeExtensionUsesDefaultPiExtensionPath() {
	piHome := s.T().TempDir()
	s.T().Setenv("PI_CODING_AGENT_DIR", piHome)

	installedPath, err := InstallPiBridgeExtension("")

	s.Require().NoError(err)
	s.Equal(filepath.Join(piHome, "extensions", "paxl-bridge.js"), installedPath)
	raw, err := os.ReadFile(installedPath)
	s.Require().NoError(err)
	s.Contains(string(raw), "paxl-pi-bridge")
}

func (s *LocalSessionsSuite) TestPiBridgeExtensionSourceReadsEmbeddedExtension() {
	source, err := PiBridgeExtensionSource()

	s.Require().NoError(err)
	s.Contains(source, "session/prompt")
}

func (s *LocalSessionsSuite) TestListPiBridgeSessionsSkipsInvalidAndStaleRecords() {
	bridgeDir := s.T().TempDir()
	s.T().Setenv("PAXL_PI_BRIDGE_DIR", bridgeDir)
	s.writePiBridgeRecord(bridgeDir, piBridgeRecord{
		SessionID:  "stale",
		SocketPath: filepath.Join(bridgeDir, "missing.sock"),
	})
	s.Require().NoError(os.WriteFile(
		filepath.Join(bridgeDir, "sessions", "invalid.json"),
		[]byte("{"),
		0o600,
	))

	sessions, err := listPiBridgeSessions(context.Background())

	s.Require().NoError(err)
	s.Empty(sessions)
}

func (s *LocalSessionsSuite) TestPiBridgeCallReturnsRPCError() {
	if runtime.GOOS == "windows" {
		s.T().Skip("unix sockets are unavailable on windows")
	}
	bridgeDir := s.shortTempDir()
	socketPath := filepath.Join(bridgeDir, "error.sock")
	listener := s.listenUnix(socketPath)
	defer closeListener(listener)
	go serveOnePiBridgeError(listener)

	err := piBridgeCall(
		context.Background(),
		socketPath,
		7,
		"session/prompt",
		map[string]any{},
		nil,
	)

	s.Require().Error(err)
	s.Contains(err.Error(), "pi bridge rpc error -32602")
}

func (s *LocalSessionsSuite) TestListKiroSessionsReturnsEmptyWhenLocalRootIsMissing() {
	s.T().Setenv("KIRO_HOME", filepath.Join(s.T().TempDir(), "missing"))

	resp, err := listKiroSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Empty(resp.Sessions)
}

func (s *LocalSessionsSuite) TestListGeminiSessionsReturnsEmptyWhenLocalRootIsMissing() {
	s.T().Setenv("GEMINI_HOME", filepath.Join(s.T().TempDir(), "missing"))

	resp, err := listGeminiSessions(context.Background(), &ListSessionsRequest{})

	s.Require().NoError(err)
	s.Empty(resp.Sessions)
}

func (s *LocalSessionsSuite) TestTrimOneLineCompactsWhitespaceAndLimitsRunes() {
	got := trimOneLine("one \n two three", 7)

	s.Equal("one two", got)
}

func (s *LocalSessionsSuite) TestSortedSessionsHandlesNilRequest() {
	resp := sortedSessions(map[string]*model.Session{
		"codex:sess": {ID: "codex:sess", UpdatedAt: "2026-06-20T01:00:00Z"},
	}, nil)

	s.Require().Len(resp.Sessions, 1)
}

func (s *LocalSessionsSuite) TestSortedSessionsParsesTimestampOffsets() {
	resp := sortedSessions(map[string]*model.Session{
		"codex:earlier": {
			ID:        "codex:earlier",
			UpdatedAt: "2026-06-20T07:30:00Z",
		},
		"codex:later": {
			ID:        "codex:later",
			UpdatedAt: "2026-06-20T01:00:00-07:00",
		},
	}, nil)

	s.Require().Len(resp.Sessions, 2)
	s.Equal("codex:later", resp.Sessions[0].ID)
}

func (s *LocalSessionsSuite) TestListClaudeSessionsHonorsCanceledContext() {
	claudeHome := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(claudeHome, "projects"), 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := listClaudeSessions(ctx, &ListSessionsRequest{})

	s.ErrorIs(err, context.Canceled)
}

func (s *LocalSessionsSuite) TestListPiSessionsHonorsCanceledContext() {
	piHome := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(piHome, "sessions"), 0o700))
	s.T().Setenv("PI_CODING_AGENT_DIR", piHome)
	s.T().Setenv("PAXL_PI_BRIDGE_DIR", filepath.Join(s.T().TempDir(), "missing-bridge"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := listPiSessions(ctx, &ListSessionsRequest{})

	s.ErrorIs(err, context.Canceled)
}

func (s *LocalSessionsSuite) writePiBridgeRecord(bridgeDir string, record piBridgeRecord) {
	sessionDir := filepath.Join(bridgeDir, "sessions")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	raw, err := json.Marshal(record)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, record.SessionID+".json"),
		append(raw, '\n'),
		0o600,
	))
}

func (s *LocalSessionsSuite) shortTempDir() string {
	dir, err := os.MkdirTemp("/tmp", "paxl-bridge-*")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (s *LocalSessionsSuite) listenUnix(socketPath string) net.Listener {
	listener, err := new(net.ListenConfig).Listen(context.Background(), "unix", socketPath)
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		s.T().Skipf("unix socket bind unavailable in this environment: %v", err)
	}
	s.Require().NoError(err)
	return listener
}

func serveOnePiBridgeRequest(listener net.Listener, requests chan<- map[string]any) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer closeConn(conn)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var request map[string]any
	if err := json.Unmarshal(line, &request); err != nil {
		return
	}
	requests <- request
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"result":  map[string]any{"delivery": "pi_extension_steer"},
	}
	raw, _ := json.Marshal(response)
	_, _ = conn.Write(append(raw, '\n'))
}

func serveOnePiBridgeError(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer closeConn(conn)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var request map[string]any
	if err := json.Unmarshal(line, &request); err != nil {
		return
	}
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"error":   map[string]any{"code": -32602, "message": "bad params"},
	}
	raw, _ := json.Marshal(response)
	_, _ = conn.Write(append(raw, '\n'))
}

func closeListener(listener net.Listener) {
	if listener != nil {
		_ = listener.Close()
	}
}

func (s *LocalSessionsSuite) TestListKiroSessionsHonorsCanceledContext() {
	kiroHome := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(kiroHome, "sessions", "cli"), 0o700))
	s.T().Setenv("KIRO_HOME", kiroHome)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := listKiroSessions(ctx, &ListSessionsRequest{})

	s.ErrorIs(err, context.Canceled)
}

func (s *LocalSessionsSuite) TestListGeminiSessionsHonorsCanceledContext() {
	geminiHome := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(geminiHome, "tmp"), 0o700))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := listGeminiSessions(ctx, &ListSessionsRequest{})

	s.ErrorIs(err, context.Canceled)
}
