package adaptor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type OpenClawSuite struct {
	suite.Suite
}

func TestOpenClawSuite(t *testing.T) {
	suite.Run(t, new(OpenClawSuite))
}

func (s *OpenClawSuite) TestAdapterInfoShowsOnlineWhenACPInitializes() {
	s.installOpenClawACPFake()

	resp, err := NewOpenClawAdapter().Info(context.Background(), &InfoRequest{})

	s.Require().NoError(err)
	s.Equal(model.AgentNameOpenClaw, resp.Agent.Name)
	s.True(resp.Agent.Available)
	s.True(resp.Agent.CLIAvailable)
	s.True(resp.Agent.SessionsAvailable)
}

func (s *OpenClawSuite) TestAdapterListsSessionsThroughACP() {
	logPath := s.installOpenClawACPFake()

	resp, err := NewOpenClawAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("openclaw:agent:work:main", resp.Sessions[0].ID)
	s.Equal(model.AgentNameOpenClaw, resp.Sessions[0].Agent)
	s.Equal("OpenClaw Work", resp.Sessions[0].Title)
	s.Equal("/tmp/openclaw", resp.Sessions[0].ProjectID)
	s.Contains(s.readOpenClawFile(logPath), `"method":"session/list"`)
}

func (s *OpenClawSuite) TestAdapterGetsSessionFromACPMetadata() {
	s.installOpenClawACPFake()

	resp, err := NewOpenClawAdapter().GetSession(
		context.Background(),
		&GetSessionRequest{NativeID: "agent:work:main"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("openclaw:agent:work:main", resp.Elements[0].SessionID)
	s.Contains(resp.Elements[0].ContentText, "OpenClaw Work")
	s.Contains(resp.Elements[0].ContentText, "reviewing")
}

func (s *OpenClawSuite) TestAdapterPromptsExistingSessionThroughACP() {
	logPath := s.installOpenClawACPFake()

	resp, err := NewOpenClawAdapter().Prompt(
		context.Background(),
		&PromptRequest{NativeID: "agent:work:main", Text: "handoff"},
	)

	s.Require().NoError(err)
	s.Equal("acp_session_prompt", resp.DeliveryMethod)
	logText := s.readOpenClawFile(logPath)
	s.Contains(logText, `"method":"session/prompt"`)
	s.Contains(logText, `"sessionId":"agent:work:main"`)
	s.Contains(logText, "handoff")
}

func (s *OpenClawSuite) TestAdapterListsEmptyWhenACPCommandIsMissing() {
	s.T().Setenv("PAXL_OPENCLAW_ACP_COMMAND", "definitely-missing-openclaw-acp")

	resp, err := NewOpenClawAdapter().ListSessions(
		context.Background(),
		&ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Empty(resp.Sessions)
}

func (s *OpenClawSuite) installOpenClawACPFake() string {
	dir := s.T().TempDir()
	logPath := filepath.Join(dir, "openclaw-acp.jsonl")
	scriptPath := filepath.Join(dir, "openclaw-acp")
	s.Require().NoError(os.WriteFile(
		scriptPath,
		[]byte(`#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$PAXL_TEST_OPENCLAW_ACP_LOG"
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":1,"result":{"authMethods":[]}}\n'
      ;;
    *'"method":"session/list"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{"sessions":[{"sessionId":"agent:work:main","agentType":"openclaw","title":"OpenClaw Work","cwd":"/tmp/openclaw","status":"running","currentTask":"reviewing","preview":"Use ACP","updatedAt":"2026-06-23T22:00:00Z"}]}}\n'
      ;;
    *'"method":"session/prompt"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{}}\n'
      ;;
  esac
done
`),
		0o700,
	))
	s.T().Setenv("PAXL_OPENCLAW_ACP_COMMAND", scriptPath)
	s.T().Setenv("PAXL_TEST_OPENCLAW_ACP_LOG", logPath)
	return logPath
}

func (s *OpenClawSuite) readOpenClawFile(path string) string {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	return string(raw)
}
