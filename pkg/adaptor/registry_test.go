package adaptor_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/suite"
)

type RegistrySuite struct {
	suite.Suite
}

func TestRegistrySuite(t *testing.T) {
	suite.Run(t, new(RegistrySuite))
}

func (s *RegistrySuite) TestDefaultRegistryOnlyContainsCodexAndClaude() {
	registry := adaptor.NewDefaultRegistry()

	resp, err := registry.List(context.Background(), &adaptor.ListRequest{})
	s.Require().NoError(err)

	s.Len(resp.Agents, 2)
	s.Equal(model.AgentNameCodex, resp.Agents[0].Name)
	s.Equal(model.AgentNameClaude, resp.Agents[1].Name)
}

func (s *RegistrySuite) TestListAcceptsVerboseWriterOption() {
	registry := adaptor.NewDefaultRegistry()
	var verbose bytes.Buffer

	resp, err := registry.List(
		context.Background(),
		&adaptor.ListRequest{},
		adaptor.WithVerboseWriter(&verbose),
	)

	s.Require().NoError(err)
	s.Len(resp.Agents, 2)
}

func (s *RegistrySuite) TestLookupRejectsUnsupportedAgent() {
	registry := adaptor.NewDefaultRegistry()

	_, err := registry.Lookup(
		context.Background(),
		&adaptor.LookupRequest{Name: model.AgentName("qwen")},
	)

	s.Error(err)
}

func (s *RegistrySuite) TestLookupReturnsSupportedAdapter() {
	registry := adaptor.NewDefaultRegistry()

	resp, err := registry.Lookup(
		context.Background(),
		&adaptor.LookupRequest{Name: model.AgentNameCodex},
	)

	s.Require().NoError(err)
	s.NotNil(resp.Adapter)
}

func (s *RegistrySuite) TestLookupRequiresRequest() {
	registry := adaptor.NewDefaultRegistry()

	_, err := registry.Lookup(context.Background(), nil)

	s.Error(err)
}

func (s *RegistrySuite) TestCodexAdapterListsSessionsThroughPublicInterface() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(
			`{"id":"sess-public","thread_name":"Public","updated_at":"2026-06-20T01:00:00Z"}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewCodexAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("codex:sess-public", resp.Sessions[0].ID)
}

func (s *RegistrySuite) TestCodexAdapterGetsSessionThroughPublicInterface() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-public.jsonl"),
		[]byte(
			`{"timestamp":"2026-06-20T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewCodexAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "sess-public"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Hello", resp.Elements[0].ContentText)
}

func (s *RegistrySuite) TestClaudeAdapterGetsSessionThroughPublicInterface() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-public.jsonl"),
		[]byte(
			`{"type":"assistant","sessionId":"claude-public","timestamp":"2026-06-20T01:00:00Z","message":{"role":"assistant","content":"Hello"}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewClaudeAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "claude-public"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Hello", resp.Elements[0].ContentText)
}

func (s *RegistrySuite) TestCodexAdapterGetSessionRequiresNativeID() {
	_, err := adaptor.NewCodexAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{},
	)

	s.Error(err)
}

func (s *RegistrySuite) TestCodexAdapterPromptsThroughCLIResume() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCommand("codex", capturePath)

	resp, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "sess-public", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Equal("handoff text", string(rawPrompt))
}

func (s *RegistrySuite) TestClaudeAdapterPromptsThroughCLIResume() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCommand("claude", capturePath)

	resp, err := adaptor.NewClaudeAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "claude-public", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Equal("handoff text", string(rawPrompt))
}

func (s *RegistrySuite) TestAdapterPromptWritesBufferedOutputWhenVerbose() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installVerboseFakeCommand("codex", capturePath)
	var verbose bytes.Buffer

	_, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "sess-public", Text: "handoff text"},
		adaptor.WithVerboseWriter(&verbose),
	)

	s.Require().NoError(err)
	s.Contains(verbose.String(), "Command stdout: fake stdout.")
	s.Contains(verbose.String(), "Command stderr: fake stderr.")
}

func (s *RegistrySuite) TestAdapterPromptRejectsFlagLikeNativeID() {
	_, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "-bad", Text: "handoff text"},
	)

	s.Require().Error(err)
	s.Contains(err.Error(), "must not start")
}

func (s *RegistrySuite) TestAdaptersStartSessionsThroughCLI() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	cases := []struct {
		name    string
		command string
		adapter adaptor.Adapter
	}{
		{name: "codex", command: "codex", adapter: adaptor.NewCodexAdapter()},
		{name: "claude", command: "claude", adapter: adaptor.NewClaudeAdapter()},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
			s.installFakeCommand(tc.command, capturePath)

			resp, err := tc.adapter.StartSession(
				context.Background(),
				&adaptor.StartSessionRequest{Text: "new handoff"},
			)

			s.Require().NoError(err)
			s.NotNil(resp)
			rawPrompt, err := os.ReadFile(capturePath)
			s.Require().NoError(err)
			s.Equal("new handoff", string(rawPrompt))
		})
	}
}

func (s *RegistrySuite) TestAdapterPromptRequiresNativeIDAndText() {
	cases := []struct {
		name    string
		adapter adaptor.Adapter
		req     *adaptor.PromptRequest
	}{
		{
			name:    "codex",
			adapter: adaptor.NewCodexAdapter(),
			req:     &adaptor.PromptRequest{Text: "handoff"},
		},
		{
			name:    "claude",
			adapter: adaptor.NewClaudeAdapter(),
			req:     &adaptor.PromptRequest{NativeID: "session"},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := tc.adapter.Prompt(context.Background(), tc.req)
			s.Error(err)
		})
	}
}

func (s *RegistrySuite) TestAdapterStartSessionRequiresText() {
	cases := []struct {
		name    string
		adapter adaptor.Adapter
		req     *adaptor.StartSessionRequest
	}{
		{name: "codex nil", adapter: adaptor.NewCodexAdapter()},
		{
			name:    "claude empty",
			adapter: adaptor.NewClaudeAdapter(),
			req:     &adaptor.StartSessionRequest{},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := tc.adapter.StartSession(context.Background(), tc.req)
			s.Error(err)
		})
	}
}

func (s *RegistrySuite) installFakeCommand(name string, capturePath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte("#!/bin/sh\ncat > "+capturePath+"\n"),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *RegistrySuite) installVerboseFakeCommand(name string, capturePath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte(
			"#!/bin/sh\n"+
				"cat > "+capturePath+"\n"+
				"printf 'fake stdout.\\n'\n"+
				"printf 'fake stderr.\\n' >&2\n",
		),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
