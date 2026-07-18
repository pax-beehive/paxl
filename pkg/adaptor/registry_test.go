package adaptor_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func (s *RegistrySuite) TestDefaultRegistryContainsBuiltInAdapters() {
	registry := adaptor.NewDefaultRegistry()

	resp, err := registry.List(context.Background(), &adaptor.ListRequest{})
	s.Require().NoError(err)

	s.Len(resp.Agents, 8)
	s.Equal(model.AgentNameCodex, resp.Agents[0].Name)
	s.Equal(model.AgentNameClaude, resp.Agents[1].Name)
	s.Equal(model.AgentNamePi, resp.Agents[2].Name)
	s.Equal(model.AgentNameKiro, resp.Agents[3].Name)
	s.Equal(model.AgentNameOpenCode, resp.Agents[4].Name)
	s.Equal(model.AgentNameKimi, resp.Agents[5].Name)
	s.Equal(model.AgentNameHermes, resp.Agents[6].Name)
	s.Equal(model.AgentNameOpenClaw, resp.Agents[7].Name)
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
	s.Len(resp.Agents, 8)
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

func (s *RegistrySuite) TestLocalAdaptersResumeSessionsInNativeInteractiveCLIs() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	cases := []struct {
		name     string
		adapter  adaptor.Adapter
		command  string
		wantArgs string
	}{
		{
			name:     "codex",
			adapter:  adaptor.NewCodexAdapter(),
			command:  "codex",
			wantArgs: "resume\nsession_native\n",
		},
		{
			name:     "claude",
			adapter:  adaptor.NewClaudeAdapter(),
			command:  "claude",
			wantArgs: "--resume\nsession_native\n",
		},
		{
			name:     "pi",
			adapter:  adaptor.NewPiAdapter(),
			command:  "pi",
			wantArgs: "--session\nsession_native\n",
		},
		{
			name:     "kiro",
			adapter:  adaptor.NewKiroAdapter(),
			command:  "kiro-cli",
			wantArgs: "chat\n--resume-id\nsession_native\n",
		},
		{
			name:     "opencode",
			adapter:  adaptor.NewOpenCodeAdapter(),
			command:  "opencode",
			wantArgs: "--session\nsession_native\n",
		},
		{
			name:     "kimi",
			adapter:  adaptor.NewKimiAdapter(),
			command:  "kimi",
			wantArgs: "--session\nsession_native\n",
		},
		{
			name:     "hermes",
			adapter:  adaptor.NewHermesAdapter(),
			command:  "hermes",
			wantArgs: "--resume\nsession_native\n",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			argsPath := filepath.Join(s.T().TempDir(), "args.txt")
			stdinPath := filepath.Join(s.T().TempDir(), "stdin.txt")
			s.installInteractiveFakeCommand(tc.command, argsPath, stdinPath)
			resumer, ok := tc.adapter.(adaptor.SessionResumer)
			s.Require().True(ok)
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			_, err := resumer.Resume(
				context.Background(),
				&adaptor.ResumeSessionRequest{NativeID: "session_native"},
				adaptor.WithStreams(strings.NewReader("terminal input"), &stdout, &stderr),
			)

			s.Require().NoError(err)
			rawArgs, err := os.ReadFile(argsPath)
			s.Require().NoError(err)
			s.Equal(tc.wantArgs, string(rawArgs))
			rawStdin, err := os.ReadFile(stdinPath)
			s.Require().NoError(err)
			s.Equal("terminal input", string(rawStdin))
			s.Equal("interactive stdout\n", stdout.String())
			s.Equal("interactive stderr\n", stderr.String())
		})
	}
}

func (s *RegistrySuite) TestPiAdapterListsSessionsThroughPublicInterface() {
	piHome := s.T().TempDir()
	sessionDir := filepath.Join(piHome, "sessions", "--tmp-project--")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("PI_CODING_AGENT_DIR", piHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "2026-06-20T23-40-48-559Z_pi-session.jsonl"),
		[]byte(
			`{"type":"session","version":3,"id":"pi-session","timestamp":"2026-06-20T23:40:48.559Z","cwd":"/tmp/project"}`+"\n"+
				`{"type":"message","id":"msg-user","timestamp":"2026-06-20T23:41:55.752Z","message":{"role":"user","content":[{"type":"text","text":"Explain paxl"}]}}`+"\n"+
				`{"type":"message","id":"msg-assistant","timestamp":"2026-06-20T23:41:58.700Z","message":{"role":"assistant","content":[{"type":"text","text":"paxl moves context."}],"model":"z-ai/glm-5.2"}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewPiAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("pi:pi-session", resp.Sessions[0].ID)
	s.Equal(model.AgentNamePi, resp.Sessions[0].Agent)
	s.Equal("Explain paxl", resp.Sessions[0].Title)
	s.Equal("2026-06-20T23:41:58.700Z", resp.Sessions[0].UpdatedAt)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
}

func (s *RegistrySuite) TestPiAdapterGetsSessionThroughPublicInterface() {
	piHome := s.T().TempDir()
	sessionDir := filepath.Join(piHome, "sessions", "--tmp-project--")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("PI_CODING_AGENT_DIR", piHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "2026-06-20T23-40-48-559Z_pi-public.jsonl"),
		[]byte(
			`{"type":"session","version":3,"id":"pi-public","timestamp":"2026-06-20T23:40:48.559Z","cwd":"/tmp/project"}`+"\n"+
				`{"type":"message","id":"msg-user","timestamp":"2026-06-20T23:41:55.752Z","message":{"role":"user","content":[{"type":"text","text":"Hello Pi"}]}}`+"\n"+
				`{"type":"message","id":"msg-assistant","timestamp":"2026-06-20T23:41:58.700Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Private reasoning should stay out of portable context."},{"type":"text","text":"Hello from Pi"}],"model":"z-ai/glm-5.2"}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewPiAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "pi-public"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("Hello Pi", resp.Elements[0].ContentText)
	s.Equal("user", resp.Elements[0].Role)
	s.Equal("Hello from Pi", resp.Elements[1].ContentText)
	s.NotContains(resp.Elements[1].ContentText, "Private reasoning")
	s.Equal("assistant", resp.Elements[1].Role)
	s.Equal("z-ai/glm-5.2", resp.Elements[1].Model)
}

func (s *RegistrySuite) TestKiroAdapterListsSessionsThroughPublicInterface() {
	kiroHome := s.T().TempDir()
	sessionDir := filepath.Join(kiroHome, "sessions", "cli")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("KIRO_HOME", kiroHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "kiro-session.json"),
		[]byte(`{
			"session_id":"kiro-session",
			"cwd":"/tmp/project",
			"created_at":"2026-06-20T23:55:57.801723Z",
			"updated_at":"2026-06-20T23:59:07.433059Z",
			"title":"Kiro title"
		}`),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "kiro-session.jsonl"),
		[]byte(
			`{"version":"v1","kind":"Prompt","data":{"message_id":"prompt","content":[{"kind":"text","data":"hello kiro"}],"meta":{"timestamp":1781999813}}}`+"\n"+
				`{"version":"v1","kind":"AssistantMessage","data":{"message_id":"assistant","content":[{"kind":"text","data":"hello back"}]}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewKiroAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("kiro:kiro-session", resp.Sessions[0].ID)
	s.Equal(model.AgentNameKiro, resp.Sessions[0].Agent)
	s.Equal("Kiro title", resp.Sessions[0].Title)
	s.Equal("2026-06-20T23:59:07.433059Z", resp.Sessions[0].UpdatedAt)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
}

func (s *RegistrySuite) TestKiroAdapterGetsSessionThroughPublicInterface() {
	kiroHome := s.T().TempDir()
	sessionDir := filepath.Join(kiroHome, "sessions", "cli")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("KIRO_HOME", kiroHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "kiro-public.json"),
		[]byte(`{
			"session_id":"kiro-public",
			"cwd":"/tmp/project",
			"created_at":"2026-06-20T23:55:57.801723Z",
			"updated_at":"2026-06-20T23:59:07.433059Z",
			"title":"Kiro title"
		}`),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "kiro-public.jsonl"),
		[]byte(
			`{"version":"v1","kind":"Prompt","data":{"message_id":"prompt","content":[{"kind":"text","data":"Hello Kiro"}],"meta":{"timestamp":1781999813}}}`+"\n"+
				`{"version":"v1","kind":"AssistantMessage","data":{"message_id":"assistant","content":[{"kind":"text","data":"Hello back"}]}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewKiroAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "kiro-public"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("user", resp.Elements[0].Role)
	s.Equal("Hello Kiro", resp.Elements[0].ContentText)
	s.Equal("assistant", resp.Elements[1].Role)
	s.Equal("Hello back", resp.Elements[1].ContentText)
}

func (s *RegistrySuite) TestKiroAdapterGetsSessionThroughMetadataFallback() {
	kiroHome := s.T().TempDir()
	sessionDir := filepath.Join(kiroHome, "sessions", "cli")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("KIRO_HOME", kiroHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "metadata-name.json"),
		[]byte(`{
			"session_id":"kiro-public",
			"cwd":"/tmp/project",
			"created_at":"2026-06-20T23:55:57.801723Z",
			"updated_at":"2026-06-20T23:59:07.433059Z",
			"title":"Kiro title"
		}`),
		0o600,
	))
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "metadata-name.jsonl"),
		[]byte(
			`{"version":"v1","kind":"Prompt","data":{"message_id":"prompt","content":[{"kind":"text","data":"Hello Kiro"}],"meta":{"timestamp":1781999813}}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewKiroAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "kiro-public"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 1)
	s.Equal("Hello Kiro", resp.Elements[0].ContentText)
}

func (s *RegistrySuite) TestGeminiAdapterListsSessionsThroughPublicInterface() {
	geminiHome := s.T().TempDir()
	sessionDir := filepath.Join(geminiHome, "tmp", "sample-project", "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(geminiHome, "tmp", "sample-project", ".project_root"),
		[]byte("/tmp/project"),
		0o600,
	))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-gemini123.jsonl"),
		[]byte(
			`{"sessionId":"gemini-session","projectHash":"sample-project","startTime":"2026-06-20T05:31:20.160Z","lastUpdated":"2026-06-20T05:32:20.160Z","kind":"main"}`+"\n"+
				`{"$set":{"messages":[{"id":"u1","timestamp":"2026-06-20T05:31:30.160Z","type":"user","content":[{"text":"Explain paxl"}]},{"id":"a1","timestamp":"2026-06-20T05:32:20.160Z","type":"gemini","content":[{"text":"paxl moves context."}]}],"lastUpdated":"2026-06-20T05:32:20.160Z"}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("gemini:gemini-session", resp.Sessions[0].ID)
	s.Equal(model.AgentNameGemini, resp.Sessions[0].Agent)
	s.Equal("Explain paxl", resp.Sessions[0].Title)
	s.Equal("2026-06-20T05:32:20.160Z", resp.Sessions[0].UpdatedAt)
	s.Equal("/tmp/project", resp.Sessions[0].ProjectID)
}

func (s *RegistrySuite) TestGeminiAdapterListsSessionTitleFromPatchMessages() {
	geminiHome := s.T().TempDir()
	sessionDir := filepath.Join(geminiHome, "tmp", "sample-project", "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-00346688.jsonl"),
		[]byte(
			`{"sessionId":"00346688-3a4e-4309-ad11-9c34f25c4e29","projectHash":"sample-project","startTime":"2026-06-20T05:31:20.160Z","lastUpdated":"2026-06-20T05:31:20.160Z","kind":"main"}`+"\n"+
				`{"$set":{"messages":[{"id":"u1","timestamp":"2026-06-20T05:31:30.160Z","type":"user","content":[{"text":"Explain Gemini title extraction"}]}],"lastUpdated":"2026-06-20T05:32:20.160Z"}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("Explain Gemini title extraction", resp.Sessions[0].Title)
	s.Equal("2026-06-20T05:32:20.160Z", resp.Sessions[0].UpdatedAt)
}

func (s *RegistrySuite) TestGeminiAdapterUsesProjectNameWhenOnlyBootstrapContextExists() {
	geminiHome := s.T().TempDir()
	sessionDir := filepath.Join(geminiHome, "tmp", "sample-project", "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(geminiHome, "tmp", "sample-project", ".project_root"),
		[]byte("/tmp/pax-console"),
		0o600,
	))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-00346688.jsonl"),
		[]byte(
			`{"sessionId":"00346688-3a4e-4309-ad11-9c34f25c4e29","projectHash":"sample-project","startTime":"2026-06-20T05:31:20.160Z","lastUpdated":"2026-06-20T05:31:20.160Z","kind":"main"}`+"\n"+
				`{"$set":{"messages":[{"id":"u1","timestamp":"2026-06-20T05:31:20.160Z","type":"user","content":[{"text":"<session_context>\nGemini CLI bootstrap context.\n</session_context>"}]}]}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("pax-console", resp.Sessions[0].Title)
}

func (s *RegistrySuite) TestGeminiAdapterResolvesProjectNameFromProjectHash() {
	geminiHome := s.T().TempDir()
	projectRoot := "/tmp/pax-console"
	hashBytes := sha256.Sum256([]byte(projectRoot))
	projectHash := hex.EncodeToString(hashBytes[:])
	sessionDir := filepath.Join(geminiHome, "tmp", projectHash, "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.Require().NoError(os.WriteFile(
		filepath.Join(geminiHome, "projects.json"),
		[]byte(`{"projects":{"/tmp/pax-console":"pax-console"}}`),
		0o600,
	))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-e89d8200.json"),
		[]byte(`{
			"sessionId":"e89d8200-6c10-4abd-9d25-4a6641ee8667",
			"projectHash":"`+projectHash+`",
			"startTime":"2026-06-20T05:31:20.160Z",
			"lastUpdated":"2026-06-20T05:31:20.160Z",
			"messages":[{"id":"i1","timestamp":"2026-06-20T05:31:20.160Z","type":"info","content":"Loaded project."}]
		}`),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().ListSessions(
		context.Background(),
		&adaptor.ListSessionsRequest{},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("pax-console", resp.Sessions[0].Title)
	s.Equal(projectRoot, resp.Sessions[0].ProjectID)
}

func (s *RegistrySuite) TestGeminiAdapterGetsJSONLSessionThroughPublicInterface() {
	geminiHome := s.T().TempDir()
	sessionDir := filepath.Join(geminiHome, "tmp", "sample-project", "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-gemini-public.jsonl"),
		[]byte(
			`{"sessionId":"gemini-public","projectHash":"sample-project","startTime":"2026-06-20T05:31:20.160Z","lastUpdated":"2026-06-20T05:32:20.160Z","kind":"main"}`+"\n"+
				`{"$set":{"messages":[{"id":"u1","timestamp":"2026-06-20T05:31:30.160Z","type":"user","content":[{"text":"Hello Gemini"}]},{"id":"a1","timestamp":"2026-06-20T05:32:20.160Z","type":"gemini","content":[{"text":"Hello back"}]}]}}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "gemini-public"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("user", resp.Elements[0].Role)
	s.Equal("Hello Gemini", resp.Elements[0].ContentText)
	s.Equal("assistant", resp.Elements[1].Role)
	s.Equal("Hello back", resp.Elements[1].ContentText)
}

func (s *RegistrySuite) TestGeminiAdapterGetsImportedJSONLSession() {
	geminiHome := s.T().TempDir()
	sessionDir := filepath.Join(geminiHome, "tmp", "sample-project", "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-gemini-import.jsonl"),
		[]byte(
			`{"sessionId":"gemini-import","projectHash":"sample-project","startTime":"2026-06-20T05:31:20.160Z","lastUpdated":"2026-06-20T05:32:20.160Z","kind":"main"}`+"\n"+
				`{"id":"u1","timestamp":"2026-06-20T05:31:30.160Z","type":"user","content":"Hello import"}`+"\n"+
				`{"id":"a1","timestamp":"2026-06-20T05:32:20.160Z","type":"gemini","content":"Imported reply"}`+"\n",
		),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "gemini-import"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("Hello import", resp.Elements[0].ContentText)
	s.Equal("Imported reply", resp.Elements[1].ContentText)
	s.Contains(resp.Elements[1].RawJSON, `"type":"gemini"`)
}

func (s *RegistrySuite) TestGeminiAdapterGetsJSONSessionThroughPublicInterface() {
	geminiHome := s.T().TempDir()
	sessionDir := filepath.Join(geminiHome, "tmp", "sample-project", "chats")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("GEMINI_HOME", geminiHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "session-2026-06-20T05-31-gemini-json.json"),
		[]byte(`{
			"sessionId":"gemini-json",
			"projectHash":"sample-project",
			"startTime":"2026-06-20T05:31:20.160Z",
			"lastUpdated":"2026-06-20T05:32:20.160Z",
			"messages":[
				{"id":"u1","timestamp":"2026-06-20T05:31:30.160Z","type":"user","content":[{"text":"Hello JSON"}]},
				{"id":"a1","timestamp":"2026-06-20T05:32:20.160Z","type":"gemini","content":[{"text":"JSON reply"}]}
			]
		}`),
		0o600,
	))

	resp, err := adaptor.NewGeminiAdapter().GetSession(
		context.Background(),
		&adaptor.GetSessionRequest{NativeID: "gemini-json"},
	)

	s.Require().NoError(err)
	s.Require().Len(resp.Elements, 2)
	s.Equal("Hello JSON", resp.Elements[0].ContentText)
	s.Equal("JSON reply", resp.Elements[1].ContentText)
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

func (s *RegistrySuite) TestCodexAdapterSteersAppSessionThroughAppServer() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-app-session.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"app-session","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project","originator":"Codex Desktop","source":"vscode","thread_source":"user"}}`+"\n",
		),
		0o600,
	))
	appCapturePath := filepath.Join(s.T().TempDir(), "app-server.jsonl")
	cliCapturePath := filepath.Join(s.T().TempDir(), "cli-prompt.txt")
	s.installCodexAppServerFakeCommand(appCapturePath, cliCapturePath, false)

	resp, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "app-session", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.Require().NotNil(resp)
	s.Equal("app_server_steer", resp.DeliveryMethod)
	rawApp, err := os.ReadFile(appCapturePath)
	s.Require().NoError(err)
	s.Contains(string(rawApp), `"method":"thread/resume"`)
	s.Contains(string(rawApp), `"threadId":"app-session"`)
	s.Contains(string(rawApp), `"method":"turn/steer"`)
	s.Contains(string(rawApp), `"expectedTurnId":"turn-active"`)
	s.Contains(string(rawApp), `"text":"handoff text"`)
	_, err = os.Stat(cliCapturePath)
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *RegistrySuite) TestCodexAdapterStartsTurnWhenAppSessionIsIdle() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-app-session.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"app-session","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project","originator":"Codex Desktop","source":"vscode","thread_source":"user"}}`+"\n",
		),
		0o600,
	))
	appCapturePath := filepath.Join(s.T().TempDir(), "app-server.jsonl")
	cliCapturePath := filepath.Join(s.T().TempDir(), "cli-prompt.txt")
	s.installCodexIdleAppServerFakeCommand(appCapturePath, cliCapturePath)

	resp, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "app-session", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.Require().NotNil(resp)
	s.Equal("app_server_turn", resp.DeliveryMethod)
	rawApp, err := os.ReadFile(appCapturePath)
	s.Require().NoError(err)
	s.Contains(string(rawApp), `"method":"turn/start"`)
	s.NotContains(string(rawApp), `"method":"turn/steer"`)
	_, err = os.Stat(cliCapturePath)
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *RegistrySuite) TestCodexAdapterStartsTurnWhenSteerFails() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-app-session.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"app-session","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project","originator":"Codex Desktop","source":"vscode","thread_source":"user"}}`+"\n",
		),
		0o600,
	))
	appCapturePath := filepath.Join(s.T().TempDir(), "app-server.jsonl")
	cliCapturePath := filepath.Join(s.T().TempDir(), "cli-prompt.txt")
	s.installCodexSteerFailingAppServerFakeCommand(appCapturePath, cliCapturePath)

	resp, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "app-session", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.Require().NotNil(resp)
	s.Equal("app_server_turn", resp.DeliveryMethod)
	rawApp, err := os.ReadFile(appCapturePath)
	s.Require().NoError(err)
	s.Contains(string(rawApp), `"method":"turn/steer"`)
	s.Contains(string(rawApp), `"expectedTurnId":"turn-active"`)
	s.Contains(string(rawApp), `"method":"turn/start"`)
	_, err = os.Stat(cliCapturePath)
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *RegistrySuite) TestCodexAdapterFallsBackToCLIResumeWhenAppServerFails() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-app-session.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"app-session","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project","originator":"Codex Desktop","source":"vscode","thread_source":"user"}}`+"\n",
		),
		0o600,
	))
	appCapturePath := filepath.Join(s.T().TempDir(), "app-server.jsonl")
	cliCapturePath := filepath.Join(s.T().TempDir(), "cli-prompt.txt")
	s.installCodexAppServerFakeCommand(appCapturePath, cliCapturePath, true)

	resp, err := adaptor.NewCodexAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "app-session", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.Require().NotNil(resp)
	s.Equal("cli_resume", resp.DeliveryMethod)
	rawPrompt, err := os.ReadFile(cliCapturePath)
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

func (s *RegistrySuite) TestPiAdapterPromptsThroughCLIResume() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("pi", capturePath, argsPath)

	resp, err := adaptor.NewPiAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "pi-public", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Equal("handoff text", string(rawPrompt))
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("--session\npi-public\n-p\n", string(rawArgs))
}

func (s *RegistrySuite) TestKiroAdapterPromptsThroughCLIResume() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("kiro-cli", capturePath, argsPath)

	resp, err := adaptor.NewKiroAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "kiro-public", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("chat\n--resume-id\nkiro-public\n--no-interactive\nhandoff text\n", string(rawArgs))
}

func (s *RegistrySuite) TestGeminiAdapterPromptsThroughCLIResume() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("gemini", capturePath, argsPath)

	resp, err := adaptor.NewGeminiAdapter().Prompt(
		context.Background(),
		&adaptor.PromptRequest{NativeID: "gemini-public", Text: "handoff text"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("--resume\ngemini-public\n-p\nhandoff text\n", string(rawArgs))
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
		{name: "pi", command: "pi", adapter: adaptor.NewPiAdapter()},
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

func (s *RegistrySuite) TestKiroAdapterStartsSessionThroughCLI() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("kiro-cli", capturePath, argsPath)

	resp, err := adaptor.NewKiroAdapter().StartSession(
		context.Background(),
		&adaptor.StartSessionRequest{Text: "new handoff"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("chat\n--no-interactive\nnew handoff\n", string(rawArgs))
}

func (s *RegistrySuite) TestGeminiAdapterStartsSessionThroughCLI() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("gemini", capturePath, argsPath)

	resp, err := adaptor.NewGeminiAdapter().StartSession(
		context.Background(),
		&adaptor.StartSessionRequest{Text: "new handoff"},
	)

	s.Require().NoError(err)
	s.NotNil(resp)
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Equal("-p\nnew handoff\n", string(rawArgs))
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
		{
			name:    "pi",
			adapter: adaptor.NewPiAdapter(),
			req:     &adaptor.PromptRequest{Text: "handoff"},
		},
		{
			name:    "kiro",
			adapter: adaptor.NewKiroAdapter(),
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
		{
			name:    "pi empty",
			adapter: adaptor.NewPiAdapter(),
			req:     &adaptor.StartSessionRequest{},
		},
		{
			name:    "kiro empty",
			adapter: adaptor.NewKiroAdapter(),
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

func (s *RegistrySuite) installArgCapturingFakeCommand(
	name string,
	capturePath string,
	argsPath string,
) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte(
			"#!/bin/sh\n"+
				"printf '%s\\n' \"$@\" > "+argsPath+"\n"+
				"cat > "+capturePath+"\n",
		),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *RegistrySuite) installInteractiveFakeCommand(
	name string,
	argsPath string,
	stdinPath string,
) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte(
			"#!/bin/sh\n"+
				"printf '%s\\n' \"$@\" > "+argsPath+"\n"+
				"cat > "+stdinPath+"\n"+
				"printf 'interactive stdout\\n'\n"+
				"printf 'interactive stderr\\n' >&2\n",
		),
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

func (s *RegistrySuite) installCodexAppServerFakeCommand(
	appCapturePath string,
	cliCapturePath string,
	failAppServer bool,
) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	failValue := "0"
	if failAppServer {
		failValue = "1"
	}
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte(
			"#!/bin/sh\n"+
				"if [ \"$1\" = \"app-server\" ]; then\n"+
				"  if [ \""+failValue+"\" = \"1\" ]; then\n"+
				"    printf 'app server unavailable\\n' >&2\n"+
				"    exit 42\n"+
				"  fi\n"+
				"  while IFS= read -r line; do\n"+
				"    printf '%s\\n' \"$line\" >> "+appCapturePath+"\n"+
				"    case \"$line\" in\n"+
				"      *'\"id\":1'*) printf '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\\n' ;;\n"+
				"      *'\"id\":2'*) printf '{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"thread\":{\"id\":\"app-session\",\"turns\":[{\"id\":\"turn-active\",\"status\":\"inProgress\"}]}}}\\n' ;;\n"+
				"      *'\"id\":3'*) printf '{\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"turnId\":\"turn-active\"}}\\n' ;;\n"+
				"    esac\n"+
				"  done\n"+
				"  exit 0\n"+
				"fi\n"+
				"cat > "+cliCapturePath+"\n",
		),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *RegistrySuite) installCodexIdleAppServerFakeCommand(
	appCapturePath string,
	cliCapturePath string,
) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte(
			"#!/bin/sh\n"+
				"if [ \"$1\" = \"app-server\" ]; then\n"+
				"  while IFS= read -r line; do\n"+
				"    printf '%s\\n' \"$line\" >> "+appCapturePath+"\n"+
				"    case \"$line\" in\n"+
				"      *'\"id\":1'*) printf '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\\n' ;;\n"+
				"      *'\"id\":2'*) printf '{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"thread\":{\"id\":\"app-session\",\"turns\":[{\"id\":\"turn-done\",\"status\":\"completed\"}]}}}\\n' ;;\n"+
				"      *'\"id\":3'*) printf '{\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"turn\":{\"id\":\"turn-test\"}}}\\n'; printf '{\"jsonrpc\":\"2.0\",\"method\":\"turn/completed\",\"params\":{\"turn\":{\"id\":\"turn-test\"}}}\\n' ;;\n"+
				"    esac\n"+
				"  done\n"+
				"  exit 0\n"+
				"fi\n"+
				"cat > "+cliCapturePath+"\n",
		),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *RegistrySuite) installCodexSteerFailingAppServerFakeCommand(
	appCapturePath string,
	cliCapturePath string,
) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte(
			"#!/bin/sh\n"+
				"if [ \"$1\" = \"app-server\" ]; then\n"+
				"  while IFS= read -r line; do\n"+
				"    printf '%s\\n' \"$line\" >> "+appCapturePath+"\n"+
				"    case \"$line\" in\n"+
				"      *'\"id\":1'*) printf '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\\n' ;;\n"+
				"      *'\"id\":2'*) printf '{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"thread\":{\"id\":\"app-session\",\"turns\":[{\"id\":\"turn-active\",\"status\":\"inProgress\"}]}}}\\n' ;;\n"+
				"      *'\"id\":3'*) printf '{\"jsonrpc\":\"2.0\",\"id\":3,\"error\":{\"code\":-32000,\"message\":\"steer rejected\"}}\\n' ;;\n"+
				"      *'\"id\":4'*) printf '{\"jsonrpc\":\"2.0\",\"id\":4,\"result\":{\"turn\":{\"id\":\"turn-test\"}}}\\n'; printf '{\"jsonrpc\":\"2.0\",\"method\":\"turn/completed\",\"params\":{\"turn\":{\"id\":\"turn-test\"}}}\\n' ;;\n"+
				"    esac\n"+
				"  done\n"+
				"  exit 0\n"+
				"fi\n"+
				"cat > "+cliCapturePath+"\n",
		),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
