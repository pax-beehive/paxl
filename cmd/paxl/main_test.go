package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
	"github.com/urfave/cli/v3"
)

type CommandSuite struct {
	suite.Suite
	stdout bytes.Buffer
	stderr bytes.Buffer
}

type fakeLocalCollaborationFacade struct {
	req  *facade.MoveSessionContextRequest
	resp *facade.MoveSessionContextResponse
	err  error
}

func (f *fakeLocalCollaborationFacade) MoveSessionContext(
	ctx context.Context,
	req *facade.MoveSessionContextRequest,
	opts ...func(*facade.Option),
) (*facade.MoveSessionContextResponse, error) {
	_ = ctx
	_ = opts
	f.req = req
	return f.resp, f.err
}

func TestCommandSuite(t *testing.T) {
	suite.Run(t, new(CommandSuite))
}

func TestRenderUpdateCheckTextSuggestsPaxlUpdate(t *testing.T) {
	var stdout bytes.Buffer
	err := renderUpdateCheck(&stdout, &facade.CheckUpdateResponse{
		CurrentVersion:  "0.1.0",
		LatestVersion:   "0.1.1",
		Status:          facade.UpdateStatusAvailable,
		UpdateAvailable: true,
	}, "text")

	if err != nil {
		t.Fatalf("renderUpdateCheck() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Run `paxl update` to upgrade.") {
		t.Fatalf("rendered update check = %q", got)
	}
}

func TestRenderApplyUpdateJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := renderApplyUpdate(&stdout, &applyUpdateResponse{
		CurrentVersion:  "0.1.0",
		LatestVersion:   "0.1.1",
		Status:          facade.UpdateStatusAvailable,
		UpdateAvailable: true,
		Updated:         true,
		Path:            "/tmp/paxl",
		Platform:        "test/os",
		SHA256:          "abc123",
		SizeBytes:       42,
	}, "json")

	if err != nil {
		t.Fatalf("renderApplyUpdate() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"updated":true`) {
		t.Fatalf("rendered update result = %q", got)
	}
}

func TestRenderSearchResultsTableEmpty(t *testing.T) {
	var stdout bytes.Buffer

	err := renderSearchResults(&stdout, &facade.SearchResponse{}, "table")

	if err != nil {
		t.Fatalf("renderSearchResults() error = %v", err)
	}
	if got := stdout.String(); got != "No matching sessions found.\n" {
		t.Fatalf("rendered empty search = %q", got)
	}
}

func TestRenderSearchResultsTableSnippets(t *testing.T) {
	var stdout bytes.Buffer
	longContent := strings.Repeat("x", 90)

	err := renderSearchResults(&stdout, &facade.SearchResponse{
		Results: []*store.SearchResult{
			{
				SessionID: "codex:first",
				Title:     "First",
				Snippet:   "matched snippet",
			},
			{
				SessionID:   "claude:second",
				Title:       "Second",
				ContentText: longContent,
			},
		},
	}, "table")

	if err != nil {
		t.Fatalf("renderSearchResults() error = %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `[1] codex:first - "First"`) {
		t.Fatalf("rendered search missing first header: %q", got)
	}
	if !strings.Contains(got, "matched snippet") {
		t.Fatalf("rendered search missing snippet: %q", got)
	}
	if !strings.Contains(got, strings.Repeat("x", 80)+"...") {
		t.Fatalf("rendered search missing truncated fallback: %q", got)
	}
}

func TestDownloadUpdateBinaryRejectsSizeMismatch(t *testing.T) {
	client := commandRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("short")),
		}, nil
	})

	_, err := downloadUpdateBinary(context.Background(), client, "https://example.test/paxl", 10)

	if err == nil || !strings.Contains(err.Error(), "download size") {
		t.Fatalf("downloadUpdateBinary() error = %v, want size mismatch", err)
	}
}

func (s *CommandSuite) SetupTest() {
	s.stdout.Reset()
	s.stderr.Reset()
	s.T().Setenv("HOME", s.T().TempDir())
	oldScheduler := scheduleSessionQueryBackgroundSync
	scheduleSessionQueryBackgroundSync = func(*sessionQueryBackgroundSyncRequest) error {
		return nil
	}
	s.T().Cleanup(func() {
		scheduleSessionQueryBackgroundSync = oldScheduler
	})
}

func (s *CommandSuite) TestRunWritesExecutionLogUnderPaxHome() {
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)

	err := run(context.Background(), []string{"version"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	entries, err := os.ReadDir(filepath.Join(home, ".pax", "paxl", "logs"))
	s.Require().NoError(err)
	s.Len(entries, 1)
	raw, err := os.ReadFile(filepath.Join(home, ".pax", "paxl", "logs", entries[0].Name()))
	s.Require().NoError(err)
	s.Contains(string(raw), `"event":"command_start"`)
	s.Contains(string(raw), `"event":"command_finish"`)
	s.Contains(string(raw), `"args":["version"]`)
}

func (s *CommandSuite) TestRunWritesCallerAgentFlagToExecutionLog() {
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)

	err := run(
		context.Background(),
		[]string{"--caller-agent", "codex", "version"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	raw := s.readExecutionLogs(home)
	s.Contains(raw, `"callerAgent":"codex"`)
}

func (s *CommandSuite) TestRunWritesCallerAgentEnvToExecutionLog() {
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)
	s.T().Setenv("PAXL_CALLER_AGENT", "hermes")

	err := run(context.Background(), []string{"version"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	raw := s.readExecutionLogs(home)
	s.Contains(raw, `"callerAgent":"hermes"`)
}

func (s *CommandSuite) TestAgentEnvPrintsHookEnvironmentPayload() {
	err := run(
		context.Background(),
		[]string{"__agent-env", "--agent", "hermes", "--event", "pre_tool_call"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal(s.stdout.Bytes(), &payload))
	s.Equal("paxl.agent_environment.v1", payload["schemaVersion"])
	s.Equal("hermes", payload["agent"])
	s.Equal("pre_tool_call", payload["event"])
	env, ok := payload["env"].(map[string]any)
	s.Require().True(ok)
	s.Equal("hermes", env["PAXL_CALLER_AGENT"])
	s.Equal("hermes", env["PAXL_AGENT"])
	s.Contains(payload["additionalContext"], "paxl caller agent: hermes")
}

func (s *CommandSuite) TestRunWritesCommandErrorsToExecutionLog() {
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)

	err := run(context.Background(), []string{"version", "--format", "xml"}, &s.stdout, &s.stderr)

	s.Require().Error(err)
	entries, err := os.ReadDir(filepath.Join(home, ".pax", "paxl", "logs"))
	s.Require().NoError(err)
	s.Len(entries, 1)
	raw, err := os.ReadFile(filepath.Join(home, ".pax", "paxl", "logs", entries[0].Name()))
	s.Require().NoError(err)
	s.Contains(string(raw), `"event":"command_finish"`)
	s.Contains(string(raw), `"status":"error"`)
	s.Contains(string(raw), "unsupported format")
}

func (s *CommandSuite) TestRunContinuesWhenExecutionLogDirectoryCannotBeCreated() {
	homeFile := filepath.Join(s.T().TempDir(), "home")
	s.Require().NoError(os.WriteFile(homeFile, []byte("not a directory"), 0o600))
	s.T().Setenv("HOME", homeFile)

	err := run(context.Background(), []string{"version"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "paxl")
}

func (s *CommandSuite) TestRunWritesBufferedAdapterDiagnosticsToExecutionLog() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installVerboseFakeCommand("claude", capturePath)

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "inject", capsuleID, "--new", "--agent", "claude"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Empty(s.stderr.String())
	raw := s.readExecutionLogs(home)
	s.Contains(raw, `"event":"diagnostic"`)
	s.Contains(raw, "Command stdout: fake stdout.")
	s.Contains(raw, "Command stderr: fake stderr.")
}

func (s *CommandSuite) TestAgentListUsesSingularCommandAndOnlyShowsSupportedAgents() {
	err := run(context.Background(), []string{"agent", "list"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "codex")
	s.Contains(s.stdout.String(), "claude")
	s.Contains(s.stdout.String(), "pi")
	s.Contains(s.stdout.String(), "kiro")
	s.NotContains(s.stdout.String(), "gemini")
	s.NotContains(s.stdout.String(), "qwen")
	s.Empty(s.stderr.String())
}

func (s *CommandSuite) TestAgentListShowsCLIAndSessionAvailabilitySeparately() {
	s.T().Setenv("PATH", s.T().TempDir())

	err := run(context.Background(), []string{"agent", "list"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "CLI")
	s.Contains(s.stdout.String(), "SESSIONS")
	s.Contains(s.stdout.String(), "missing")
}

func (s *CommandSuite) TestAgentListShowsHermesOfflineWithLocalSessions() {
	s.T().Setenv("PATH", s.T().TempDir())
	hermesHome := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(hermesHome, "sessions"), 0o700))
	s.T().Setenv("HERMES_HOME", hermesHome)

	err := run(context.Background(), []string{"agent", "list"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "STATUS")
	s.Contains(s.stdout.String(), "hermes")
	s.Contains(s.stdout.String(), "offline")
}

func (s *CommandSuite) TestAgentListSupportsJSONLFormat() {
	err := run(
		context.Background(),
		[]string{"agent", "list", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)

	var first model.AgentInfo
	s.Require().NoError(json.Unmarshal(firstLine(s.stdout.Bytes()), &first))
	s.Equal(model.AgentNameCodex, first.Name)
}

func (s *CommandSuite) TestAgentListAcceptsVerboseFlag() {
	err := run(context.Background(), []string{"agent", "list", "--verbose"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "codex")
}

func (s *CommandSuite) TestAgentListAcceptsProbeFlagForCompatibility() {
	err := run(context.Background(), []string{"agent", "list", "--probe"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "codex")
}

func (s *CommandSuite) TestAgentListRejectsUnknownFormatBeforeFacadeCall() {
	err := run(
		context.Background(),
		[]string{"agent", "list", "--format", "yaml"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestAgentCommandDoesNotExposeSetup() {
	err := run(context.Background(), []string{"agent", "--help"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "list")
	s.NotContains(s.stdout.String(), "setup")
}

func (s *CommandSuite) TestSetupInstallsClaudeHook() {
	claudeHome := filepath.Join(s.T().TempDir(), ".claude")
	s.T().Setenv("CLAUDE_HOME", claudeHome)

	err := run(
		context.Background(),
		[]string{"setup", "--agent", "claude", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"agent":"claude"`)
	s.Contains(s.stdout.String(), `"status":"installed"`)
	raw, err := os.ReadFile(filepath.Join(claudeHome, "settings.json"))
	s.Require().NoError(err)
	s.Contains(string(raw), "UserPromptSubmit")
	s.Contains(string(raw), "paxl __agent-hook --agent claude --event user-prompt")
}

func (s *CommandSuite) TestSetupInstallsDescriptorForGenericAgentHook() {
	piHome := filepath.Join(s.T().TempDir(), ".pi")
	s.T().Setenv("PI_HOME", piHome)

	err := run(
		context.Background(),
		[]string{"setup", "--agent", "pi", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"agent":"pi"`)
	s.Contains(s.stdout.String(), `"status":"installed"`)
	raw, err := os.ReadFile(filepath.Join(piHome, "paxl", "hooks", "user-prompt.json"))
	s.Require().NoError(err)
	s.Contains(string(raw), `"agent": "pi"`)
	s.Contains(string(raw), "__agent-hook --agent pi --event user-prompt")
}

func (s *CommandSuite) TestSetupWithDaemonDryRunRendersDaemonPlan() {
	codexHome := filepath.Join(s.T().TempDir(), ".codex")
	s.T().Setenv("CODEX_HOME", codexHome)

	err := run(
		context.Background(),
		[]string{"setup", "--agent", "codex", "--with-daemon", "--dry-run", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.setup.daemon.v1"`)
	s.Contains(s.stdout.String(), `"binary":"paxd"`)
	s.Contains(s.stdout.String(), "Would set up paxd")
	s.NoFileExists(filepath.Join(codexHome, "paxl", "hooks", "user-prompt.json"))
}

func (s *CommandSuite) TestHiddenAgentHookConsumesMatchingInjectionOnce() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID,
			"--match", "keyword",
			"--keyword", "handoff",
			"--agent", "claude",
		},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Queued")

	s.stdout.Reset()
	s.stderr.Reset()
	s.withStdinJSON(map[string]string{
		"session_id": "claude-session-1",
		"prompt":     "please use the handoff context",
		"cwd":        "/tmp/paxl",
	}, func() {
		err = run(
			context.Background(),
			[]string{"--db", dbPath, "__agent-hook", "--agent", "claude", "--event", "user_prompt"},
			&s.stdout,
			&s.stderr,
		)
	})

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "system_handoff")
	s.Contains(s.stdout.String(), "Bridge context")
	s.Empty(s.stderr.String())

	s.stdout.Reset()
	s.stderr.Reset()
	s.withStdinJSON(map[string]string{
		"session_id": "claude-session-1",
		"prompt":     "please use the handoff context again",
		"cwd":        "/tmp/paxl",
	}, func() {
		err = run(
			context.Background(),
			[]string{"--db", dbPath, "__agent-hook", "--agent", "claude", "--event", "user-prompt"},
			&s.stdout,
			&s.stderr,
		)
	})

	s.Require().NoError(err)
	s.Empty(s.stdout.String())
	s.Empty(s.stderr.String())

	s.stdout.Reset()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "injection", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"status":"consumed"`)
	s.Contains(s.stdout.String(), `"targetSessionId":"claude:claude-session-1"`)
	s.Contains(s.stdout.String(), `"routeMatchType":"keyword"`)
	s.Contains(s.stdout.String(), `"routeMatchValue":"handoff"`)
}

func (s *CommandSuite) TestHiddenAgentHookConsumesProjectRouteFromCodexStyleInput() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID,
			"--match", "project",
			"--project", "pax-manager",
			"--agent", "codex",
		},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)

	s.stdout.Reset()
	s.stderr.Reset()
	s.withStdinJSON(map[string]string{
		"sessionId":  "019efd02-b216-7412-b091-fbf8e1647364",
		"userPrompt": "start planning",
		"projectId":  "/Users/toddzheng/Workspace/golang/pax-mono/pax-manager",
	}, func() {
		err = run(
			context.Background(),
			[]string{"--db", dbPath, "__agent-hook", "--agent", "codex", "--event", "user-prompt"},
			&s.stdout,
			&s.stderr,
		)
	})

	s.Require().NoError(err)
	var hookOutput map[string]any
	s.Require().NoError(json.Unmarshal(s.stdout.Bytes(), &hookOutput))
	s.Equal(true, hookOutput["continue"])
	s.Equal(true, hookOutput["suppressOutput"])
	specific, ok := hookOutput["hookSpecificOutput"].(map[string]any)
	s.Require().True(ok)
	s.Equal("UserPromptSubmit", specific["hookEventName"])
	additionalContext, ok := specific["additionalContext"].(string)
	s.Require().True(ok)
	s.Contains(additionalContext, "system_handoff")
	s.Contains(additionalContext, "Bridge context")

	s.stdout.Reset()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "injection", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"status":"consumed"`)
	s.Contains(s.stdout.String(), `"targetSessionId":"codex:019efd02-b216-7412-b091-fbf8e1647364"`)
}

func (s *CommandSuite) TestHiddenAgentHookNoopsWhenNoInjectionMatchesAndIsHiddenFromHelp() {
	err := run(context.Background(), []string{"--help"}, &s.stdout, &s.stderr)
	s.Require().NoError(err)
	s.NotContains(s.stdout.String(), "__agent-hook")

	s.stdout.Reset()
	s.stderr.Reset()
	err = run(
		context.Background(),
		[]string{"__agent-hook", "--agent", "claude", "--event", "user-prompt"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Empty(s.stdout.String())
	s.Empty(s.stderr.String())
}

func (s *CommandSuite) TestSessionListWithCleanHomeReturnsEmptyList() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--limit", "5"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "ID")
}

func (s *CommandSuite) TestParseDurationSupportsDaySuffix() {
	duration, err := parseDuration("2d")

	s.Require().NoError(err)
	s.Equal(48*time.Hour, duration)
}

func (s *CommandSuite) TestDefaultHTMLPathSanitizesSessionID() {
	path := defaultHTMLPath(&model.Session{ID: "codex:sess/one"})

	s.Contains(path, "paxl-codex_sess_one.html")
}

func (s *CommandSuite) TestVersionPrintsBinaryNameAndVersion() {
	err := run(context.Background(), []string{"version"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "paxl")
	s.Contains(s.stdout.String(), "commit")
}

func (s *CommandSuite) TestVersionSupportsJSONFormat() {
	err := run(context.Background(), []string{"version", "--format", "json"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"version"`)
}

func (s *CommandSuite) TestVersionRejectsUnknownFormat() {
	err := run(context.Background(), []string{"version", "--format", "yaml"}, &s.stdout, &s.stderr)

	s.Error(err)
}

func (s *CommandSuite) TestUpdateCommandExposesCheck() {
	command := findCommand(newCommand(&s.stdout, &s.stderr), "update")
	s.Require().NotNil(command)

	s.True(hasCommand(command, "check"))
}

func (s *CommandSuite) TestNodeListUsesManagerNodes() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedManagerCredential(dbPath, "https://manager.example")
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/nodes", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return commandJSONResponse(`{
			"data":{
				"nodes":[{
					"node_id":"node_paxl",
					"kind":"paxl",
					"name":"paxl-mac",
					"hostname":"paxl-mac",
					"status":"offline",
					"registered_at":"2026-06-24T00:00:00Z"
				},{
					"node_id":"node_paxd",
					"kind":"paxd",
					"name":"workstation",
					"hostname":"workstation",
					"status":"online",
					"registered_at":"2026-06-23T00:00:00Z"
				}]
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(context.Background(), []string{"--db", dbPath, "node", "list"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "node_paxl")
	s.Contains(s.stdout.String(), "paxl")
	s.Contains(s.stdout.String(), "*")
	s.Contains(s.stdout.String(), "node_paxd")
}

func (s *CommandSuite) TestNodeListSupportsJSONL() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedManagerCredential(dbPath, "https://manager.example")
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return commandJSONResponse(`{
			"data":{"nodes":[{"node_id":"node_paxl","kind":"paxl","status":"offline"}]},
			"code":200,
			"message":"ok"
		}`), nil
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "node", "list", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.node.v1"`)
	s.Contains(s.stdout.String(), `"nodeId":"node_paxl"`)
	s.Contains(s.stdout.String(), `"current":true`)
}

func (s *CommandSuite) TestNodeAgentListUsesManagerNodeAgents() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedManagerCredential(dbPath, "https://manager.example")
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/nodes/node_1/agents", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return commandJSONResponse(`{
			"data":{"agents":[{
				"agent_id":"agent_1",
				"node_id":"node_1",
				"name":"hermes",
				"agent_type":"hermes",
				"status":"online",
				"registered_at":"2026-06-24T00:00:00Z"
			}]},
			"code":200,
			"message":"ok"
		}`), nil
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "node", "agent", "list", "node_1"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "agent_1")
	s.Contains(s.stdout.String(), "hermes")
	s.Contains(s.stdout.String(), "online")
}

func (s *CommandSuite) TestRenderNodeAgentListSupportsJSONLAndRejectsUnknownFormat() {
	resp := &facade.ListNodeAgentsResponse{
		NodeID: "node_1",
		Agents: []*model.NodeAgent{
			{
				AgentID:      "agent_1",
				NodeID:       "node_1",
				Name:         "codex",
				AgentType:    "codex",
				Status:       "online",
				RegisteredAt: "2026-06-24T00:00:00Z",
			},
		},
	}

	err := renderNodeAgentList(&s.stdout, resp, "jsonl")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.node_agent.v1"`)
	s.Contains(s.stdout.String(), `"agentId":"agent_1"`)

	s.SetupTest()
	err = renderNodeAgentList(&s.stdout, resp, "xml")
	s.Error(err)
}

func (s *CommandSuite) TestNodeSessionListUsesManagerNodeSessions() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedManagerCredential(dbPath, "https://manager.example")
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/nodes/node_1/agents/agent_1/sessions", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return commandJSONResponse(`{
			"data":{"sessions":[{
				"node_id":"node_1",
				"agent_id":"agent_1",
				"session_id":"sess_1",
				"name":"Debugging",
				"status":"active",
				"preview":"Investigating",
				"updated_at":"2026-06-24T00:00:00Z"
			}]},
			"code":200,
			"message":"ok"
		}`), nil
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "node", "session", "list", "node_1", "agent_1"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "sess_1")
	s.Contains(s.stdout.String(), "Debugging")
	s.Contains(s.stdout.String(), "Investigating")
}

func (s *CommandSuite) TestNodeSessionListSupportsJSONL() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedManagerCredential(dbPath, "https://manager.example")
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return commandJSONResponse(`{
			"data":{"sessions":[{"session_id":"sess_1","node_id":"node_1","agent_id":"agent_1"}]},
			"code":200,
			"message":"ok"
		}`), nil
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath, "node", "session", "list", "node_1", "agent_1",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.node_session.v1"`)
	s.Contains(s.stdout.String(), `"sessionId":"sess_1"`)
}

func (s *CommandSuite) TestAuthCommandsLoginWhoamiAndLogout() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	var deleteCalled bool
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/paxl/device-login/start":
			s.Equal("https://manager.example", req.URL.Scheme+"://"+req.URL.Host)
			var body map[string]string
			s.Require().NoError(json.NewDecoder(req.Body).Decode(&body))
			s.Contains(body["client_name"], "paxl-")
			return commandJSONResponse(`{
				"data":{
					"login_id":"login-1",
					"user_code":"ABC123",
					"poll_token":"poll-1",
					"verification_uri":"https://manager.example/paxl-login.html",
					"verification_uri_complete":"https://manager.example/paxl-login.html?code=ABC123",
					"interval":0
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/paxl/device-login/poll":
			return commandJSONResponse(`{
				"data":{
					"status":"approved",
					"api_key":"paxu_test",
					"node_id":"node_paxl",
					"api_key_meta":{"key_id":"key-1"},
					"user":{"user_id":"usr_1","email":"cli@example.com","display_name":"CLI","role":"user"}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/self/me":
			s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
			return commandJSONResponse(`{
				"data":{
					"user":{"user_id":"usr_1","email":"cli@example.com","display_name":"CLI","role":"user"}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v1/user/self/api-keys/key-1":
			s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
			deleteCalled = true
			return commandJSONResponse(`{"data":{"ok":true},"code":200,"message":"ok"}`), nil
		default:
			return nil, fmt.Errorf(
				"unexpected manager request: %s %s",
				req.Method,
				req.URL.String(),
			)
		}
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"login",
			"--manager-url", "https://manager.example",
			"--timeout", "1s",
		},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "logged in cli@example.com")
	s.Contains(s.stdout.String(), "ABC123")
	s.stdout.Reset()

	err = run(context.Background(), []string{"--db", dbPath, "whoami"}, &s.stdout, &s.stderr)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "user cli@example.com")
	s.Contains(s.stdout.String(), "node_id node_paxl")
	s.stdout.Reset()

	err = run(context.Background(), []string{"--db", dbPath, "logout"}, &s.stdout, &s.stderr)
	s.Require().NoError(err)
	s.True(deleteCalled)
	s.Contains(s.stdout.String(), "logged out cli@example.com")
	s.stdout.Reset()

	err = run(context.Background(), []string{"--db", dbPath, "whoami"}, &s.stdout, &s.stderr)
	s.Error(err)
	s.Contains(err.Error(), "not logged in")
}

func (s *CommandSuite) TestLoginPrintsVerificationBeforeWaitingForApproval() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	oldClient := authHTTPClient
	authHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/paxl/device-login/start":
			return commandJSONResponse(`{
				"data":{
					"login_id":"login-1",
					"user_code":"ABC123",
					"poll_token":"poll-1",
					"verification_uri":"https://manager.example/paxl-login.html",
					"verification_uri_complete":"https://manager.example/paxl-login.html?code=ABC123",
					"interval":0
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/paxl/device-login/poll":
			return commandJSONResponse(
				`{"data":{"status":"pending"},"code":200,"message":"ok"}`,
			), nil
		default:
			return nil, fmt.Errorf(
				"unexpected manager request: %s %s",
				req.Method,
				req.URL.String(),
			)
		}
	})
	s.T().Cleanup(func() { authHTTPClient = oldClient })

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"login",
			"--manager-url", "https://manager.example",
			"--timeout", "25ms",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().Error(err)
	s.Contains(err.Error(), "timed out")
	s.Contains(
		s.stdout.String(),
		"verification https://manager.example/paxl-login.html?code=ABC123",
	)
	s.Contains(s.stdout.String(), "code ABC123")
	s.Contains(s.stdout.String(), "waiting for browser approval")
	s.NotContains(s.stdout.String(), "logged in")
}

func (s *CommandSuite) TestLoginHelpHidesAdvancedFlags() {
	err := run(context.Background(), []string{"login", "--help"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.NotContains(s.stdout.String(), "manager-url")
	s.NotContains(s.stdout.String(), "client-name")
	s.NotContains(s.stdout.String(), "timeout")
	s.NotContains(s.stdout.String(), "format")
}

func (s *CommandSuite) TestLoginClientNameUsesHashedMACAddress() {
	name := loginClientName("aa:bb:cc:dd:ee:ff")

	s.Contains(name, "paxl-")
	s.NotContains(name, "aa:bb:cc:dd:ee:ff")
	s.Regexp(`^paxl-[a-z0-9]+-[a-z0-9]+-[a-f0-9]{8}$`, name)
}

func (s *CommandSuite) TestAuthRenderersSupportJSONAndRejectUnknownFormats() {
	login := &facade.LoginResponse{
		ManagerURL:              "https://manager.example",
		UserCode:                "ABC123",
		VerificationURIComplete: "https://manager.example/paxl-login.html?code=ABC123",
		Credential: &model.AuthCredential{
			ManagerURL: "https://manager.example",
			APIKey:     "paxu_secret",
			Email:      "cli@example.com",
		},
	}
	s.Require().NoError(renderLogin(&s.stdout, login, "json"))
	s.Contains(s.stdout.String(), `"user_code":"ABC123"`)
	s.Contains(s.stdout.String(), `"manager_url":"https://manager.example"`)
	s.NotContains(s.stdout.String(), "paxu_secret")
	s.NotContains(s.stdout.String(), "APIKey")
	s.stdout.Reset()
	s.Require().NoError(renderLogin(&s.stdout, login, "text"))
	s.Contains(s.stdout.String(), "manager https://manager.example")
	s.Contains(
		s.stdout.String(),
		"verification https://manager.example/paxl-login.html?code=ABC123",
	)
	s.Contains(s.stdout.String(), "code ABC123")
	s.Contains(s.stdout.String(), "waiting for browser approval")
	s.Contains(s.stdout.String(), "logged in cli@example.com")
	s.stdout.Reset()

	whoami := &facade.WhoamiResponse{
		ManagerURL: "https://manager.example",
		Credential: &model.AuthCredential{
			ManagerURL: "https://manager.example",
			APIKey:     "paxu_secret",
			Email:      "cli@example.com",
		},
		User: &facade.AuthUser{UserID: "usr_1", Email: "cli@example.com"},
	}
	s.Require().NoError(renderWhoami(&s.stdout, whoami, "json"))
	s.Contains(s.stdout.String(), `"email":"cli@example.com"`)
	s.NotContains(s.stdout.String(), "paxu_secret")
	s.NotContains(s.stdout.String(), "APIKey")
	s.stdout.Reset()

	s.Require().NoError(renderLogout(&s.stdout, &facade.LogoutResponse{}, "text"))
	s.Contains(s.stdout.String(), "logged out")
	s.Error(renderLogin(&s.stdout, login, "yaml"))
	s.Error(renderWhoami(&s.stdout, whoami, "yaml"))
	s.Error(renderLogout(&s.stdout, &facade.LogoutResponse{}, "yaml"))
}

func (s *CommandSuite) TestUpdateCheckReportsAvailableUpdateAsJSON() {
	oldClient := updateHTTPClient
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal("https://example.test/manifest.json", req.URL.String())
		return commandJSONResponse(`{
			"product": "paxl",
			"version": "0.1.1",
			"artifacts": [
				{
					"platform": "test/os",
					"sha256": "abc123",
					"size": 42,
					"storage_url": "https://example.test/paxl"
				}
			]
		}`), nil
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
	})

	err := run(
		context.Background(),
		[]string{
			"update",
			"check",
			"--format",
			"json",
			"--manifest-url",
			"https://example.test/manifest.json",
			"--platform",
			"test/os",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"latest_version":"0.1.1"`)
	s.Contains(s.stdout.String(), `"update_available":true`)
	s.Empty(s.stderr.String())
}

func (s *CommandSuite) TestUpdateCheckUsesResolverByDefault() {
	oldClient := updateHTTPClient
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal("/api/v1/public/artifacts/download", req.URL.Path)
		s.Equal("paxl", req.URL.Query().Get("product"))
		s.NotEmpty(req.URL.Query().Get("platform"))
		s.Equal("stable", req.URL.Query().Get("tags"))
		return commandJSONResponse(`{
			"data": {
				"url": "https://example.test/paxl",
				"sha256": "abc123",
				"size_bytes": 42,
				"version": "0.1.1",
				"product": "paxl",
				"platform": "test/os"
			}
		}`), nil
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
	})

	err := run(
		context.Background(),
		[]string{"update", "check", "--format", "json"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"update_available":true`)
}

func (s *CommandSuite) TestVersionCheckUsesResolver() {
	oldClient := updateHTTPClient
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal("/api/v1/public/artifacts/download", req.URL.Path)
		s.Equal("paxl", req.URL.Query().Get("product"))
		return commandJSONResponse(`{
			"data": {
				"url": "https://example.test/paxl",
				"sha256": "abc123",
				"size_bytes": 42,
				"version": "0.1.1",
				"product": "paxl",
				"platform": "test/os"
			}
		}`), nil
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
	})

	err := run(
		context.Background(),
		[]string{"version", "--check", "--format", "json"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"latest_version":"0.1.1"`)
	s.Contains(s.stdout.String(), `"update_available":true`)
	s.NotContains(s.stdout.String(), `"commit"`)
}

func (s *CommandSuite) TestUpdateDownloadsAndReplacesCurrentBinary() {
	oldClient := updateHTTPClient
	oldExecutablePath := executablePath
	oldVersion := version
	newBinary := []byte("#!/bin/sh\necho paxl 0.1.1\n")
	sha := testSHA256(newBinary)
	exe := filepath.Join(s.T().TempDir(), "paxl")
	s.Require().NoError(os.WriteFile(exe, []byte("old paxl"), 0o755))
	version = "0.1.0"
	executablePath = func() (string, error) {
		return exe, nil
	}
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://example.test/api?platform=test%2Fos&product=paxl&tags=stable":
			return commandJSONResponse(fmt.Sprintf(`{
				"data": {
					"url": "https://example.test/download/paxl",
					"sha256": %q,
					"size_bytes": %d,
					"version": "0.1.1",
					"product": "paxl",
					"platform": "test/os"
				}
			}`, sha, len(newBinary))), nil
		case "https://example.test/download/paxl":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(newBinary)),
				Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected update request %s", req.URL.String())
		}
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
		executablePath = oldExecutablePath
		version = oldVersion
	})

	err := run(
		context.Background(),
		[]string{
			"update",
			"--resolver-url",
			"https://example.test/api",
			"--platform",
			"test/os",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	raw, err := os.ReadFile(exe)
	s.Require().NoError(err)
	s.Equal(newBinary, raw)
	s.Contains(s.stdout.String(), "Updated paxl 0.1.0 -> 0.1.1")
	s.Contains(s.stdout.String(), "Path: "+exe)
}

func (s *CommandSuite) TestUpdateReplacesDevelopmentBuildWithLatestStable() {
	oldClient := updateHTTPClient
	oldExecutablePath := executablePath
	oldVersion := version
	newBinary := []byte("#!/bin/sh\necho paxl 0.1.18\n")
	sha := testSHA256(newBinary)
	exe := filepath.Join(s.T().TempDir(), "paxl")
	s.Require().NoError(os.WriteFile(exe, []byte("old paxl"), 0o755))
	version = "0.1.17-dev"
	executablePath = func() (string, error) {
		return exe, nil
	}
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://example.test/api?platform=test%2Fos&product=paxl&tags=stable":
			return commandJSONResponse(fmt.Sprintf(`{
				"data": {
					"url": "https://example.test/download/paxl",
					"sha256": %q,
					"size_bytes": %d,
					"version": "0.1.18",
					"product": "paxl",
					"platform": "test/os"
				}
			}`, sha, len(newBinary))), nil
		case "https://example.test/download/paxl":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(newBinary)),
				Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected update request %s", req.URL.String())
		}
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
		executablePath = oldExecutablePath
		version = oldVersion
	})

	err := run(
		context.Background(),
		[]string{
			"update",
			"--resolver-url",
			"https://example.test/api",
			"--platform",
			"test/os",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	raw, err := os.ReadFile(exe)
	s.Require().NoError(err)
	s.Equal(newBinary, raw)
	s.Contains(s.stdout.String(), "Updated paxl 0.1.17-dev -> 0.1.18")
}

func (s *CommandSuite) TestUpdateReportsUpToDateWithoutReplacingBinary() {
	oldClient := updateHTTPClient
	oldExecutablePath := executablePath
	oldVersion := version
	version = "0.1.0"
	executablePath = func() (string, error) {
		return "", fmt.Errorf("executable path should not be needed")
	}
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(
			"https://example.test/api?platform=test%2Fos&product=paxl&tags=stable",
			req.URL.String(),
		)
		return commandJSONResponse(`{
			"data": {
				"url": "https://example.test/download/paxl",
				"sha256": "abc123",
				"size_bytes": 42,
				"version": "0.1.0",
				"product": "paxl",
				"platform": "test/os"
			}
		}`), nil
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
		executablePath = oldExecutablePath
		version = oldVersion
	})

	err := run(
		context.Background(),
		[]string{
			"update",
			"--resolver-url",
			"https://example.test/api",
			"--platform",
			"test/os",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Current: 0.1.0")
	s.Contains(s.stdout.String(), "Status:  Up to date")
}

func (s *CommandSuite) TestUpdateRejectsBadDownloadSHA() {
	oldClient := updateHTTPClient
	oldVersion := version
	version = "0.1.0"
	updateHTTPClient = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://example.test/api?platform=test%2Fos&product=paxl&tags=stable":
			return commandJSONResponse(`{
				"data": {
					"url": "https://example.test/download/paxl",
					"sha256": "abc123",
					"size_bytes": 3,
					"version": "0.1.1",
					"product": "paxl",
					"platform": "test/os"
				}
			}`), nil
		case "https://example.test/download/paxl":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("new")),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected update request %s", req.URL.String())
		}
	})
	s.T().Cleanup(func() {
		updateHTTPClient = oldClient
		version = oldVersion
	})

	err := run(
		context.Background(),
		[]string{
			"update",
			"--resolver-url",
			"https://example.test/api",
			"--platform",
			"test/os",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().Error(err)
	s.Contains(err.Error(), "verify update")
}

func (s *CommandSuite) TestUpdateStatusTextCoversKnownStatuses() {
	cases := map[facade.UpdateStatus]string{
		facade.UpdateStatusUnknown:     "Unknown",
		facade.UpdateStatusAvailable:   "Update available",
		facade.UpdateStatusUpToDate:    "Up to date",
		facade.UpdateStatusAhead:       "Current build is newer than latest stable",
		facade.UpdateStatusDevelopment: "Development build; latest stable shown",
		facade.UpdateStatus("other"):   "Unknown",
	}

	for status, want := range cases {
		s.Equal(want, updateStatusText(status))
	}
}

func (s *CommandSuite) TestUpdateCheckRejectsUnknownFormat() {
	err := run(
		context.Background(),
		[]string{"update", "check", "--format", "yaml"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestPluralTopLevelCommandsAreNotSupported() {
	command := newCommand(&s.stdout, &s.stderr)

	for _, name := range []string{"agents", "sessions", "capsules"} {
		s.Run(name, func() {
			s.False(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestSingularSessionCommandExposesMigratedSubcommands() {
	cases := []string{"list", "get", "mirror", "query"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "session")
	s.Require().NotNil(command)

	for _, name := range cases {
		s.Run(name, func() {
			s.True(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestSessionListSyncsCodexLocalSessionsToSQLite() {
	codexHome := s.T().TempDir()
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(
			`{"id":"sess-1","thread_name":"Codex session","updated_at":"2026-06-20T01:00:00Z"}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "codex", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"id":"codex:sess-1"`)
	s.Contains(s.stdout.String(), `"agent":"codex"`)
}

func (s *CommandSuite) TestSessionListSyncsClaudeLocalSessionsToSQLite() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","cwd":"/tmp/project","message":{"role":"user","content":"Claude session title"}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "claude", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"id":"claude:claude-session"`)
	s.Contains(s.stdout.String(), `"agent":"claude"`)
}

func (s *CommandSuite) TestSessionListSyncsPiLocalSessionsToSQLite() {
	piHome := s.T().TempDir()
	sessionDir := filepath.Join(piHome, "sessions", "--tmp-project--")
	s.Require().NoError(os.MkdirAll(sessionDir, 0o700))
	s.T().Setenv("PI_CODING_AGENT_DIR", piHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(sessionDir, "2026-06-20T23-40-48-559Z_pi-session.jsonl"),
		[]byte(
			`{"type":"session","version":3,"id":"pi-session","timestamp":"2026-06-20T23:40:48.559Z","cwd":"/tmp/project"}`+"\n"+
				`{"type":"message","id":"msg-user","timestamp":"2026-06-20T23:41:55.752Z","message":{"role":"user","content":[{"type":"text","text":"Pi session title"}]}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "pi", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"id":"pi:pi-session"`)
	s.Contains(s.stdout.String(), `"title":"Pi session title"`)
}

func (s *CommandSuite) TestSessionListSyncsKiroLocalSessionsToSQLite() {
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
			"title":"Kiro session title"
		}`),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "kiro", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"id":"kiro:kiro-session"`)
	s.Contains(s.stdout.String(), `"title":"Kiro session title"`)
}

func (s *CommandSuite) TestSessionListRejectsGeminiAgent() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "gemini", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().Error(err)
	s.Contains(err.Error(), `agent "gemini" is no longer supported`)
}

func (s *CommandSuite) TestSessionListAcceptsCommaSeparatedAgents() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedStoredSessions(dbPath, []*model.Session{
		{NativeID: "codex-one", Title: "Codex"},
	})
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	_, err = opened.Store.UpsertSessions(context.Background(), &store.UpsertSessionsRequest{
		Agent: model.AgentNameClaude,
		Sessions: []*model.Session{
			{NativeID: "claude-one", Title: "Claude"},
		},
	})
	s.Require().NoError(err)
	s.Require().NoError(opened.Store.Close())

	err = run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "list",
			"--no-sync",
			"--agent", "codex,claude",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"id":"codex:codex-one"`)
	s.Contains(s.stdout.String(), `"id":"claude:claude-one"`)
}

func (s *CommandSuite) TestSessionQuerySearchesCachedSessionElements() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedStoredSessions(dbPath, []*model.Session{
		{NativeID: "query-one", Title: "Searchable session"},
	})
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	_, err = opened.Store.ReplaceSessionElements(
		context.Background(),
		&store.ReplaceSessionElementsRequest{
			SessionID: "codex:query-one",
			Elements: []*model.Element{
				{Seq: 1, Type: "message", Role: "user", ContentText: "docker rollout plan"},
			},
		},
	)
	s.Require().NoError(err)
	s.Require().NoError(opened.Store.Close())

	err = run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "query", "docker",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"session_id":"codex:query-one"`)
	s.Contains(s.stdout.String(), `"agent":"codex"`)
	s.Contains(s.stdout.String(), `"content_text":"docker rollout plan"`)
}

func (s *CommandSuite) TestSessionQuerySchedulesBackgroundSyncByDefault() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	_, err = opened.Store.UpsertSessions(context.Background(), &store.UpsertSessionsRequest{
		Agent: model.AgentNameHermes,
		Sessions: []*model.Session{
			{NativeID: "query-background", Title: "Background query"},
		},
	})
	s.Require().NoError(err)
	_, err = opened.Store.ReplaceSessionElements(
		context.Background(),
		&store.ReplaceSessionElementsRequest{
			SessionID: "hermes:query-background",
			Elements: []*model.Element{
				{Seq: 1, Type: "message", Role: "user", ContentText: "background refresh token"},
			},
		},
	)
	s.Require().NoError(err)
	s.Require().NoError(opened.Store.Close())
	var scheduled *sessionQueryBackgroundSyncRequest
	scheduleSessionQueryBackgroundSync = func(req *sessionQueryBackgroundSyncRequest) error {
		copied := *req
		scheduled = &copied
		return nil
	}

	err = run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "query", "background",
			"--agent", "hermes",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Require().NotNil(scheduled)
	s.Equal(dbPath, scheduled.DBPath)
	s.Equal(model.AgentNameHermes, scheduled.Agent)
	s.Equal(10, scheduled.Limit)
	s.Contains(s.stdout.String(), `"session_id":"hermes:query-background"`)
}

func (s *CommandSuite) TestSessionQueryCanDisableBackgroundSync() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.seedStoredSessions(dbPath, []*model.Session{
		{NativeID: "query-no-background", Title: "No background query"},
	})
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	_, err = opened.Store.ReplaceSessionElements(
		context.Background(),
		&store.ReplaceSessionElementsRequest{
			SessionID: "codex:query-no-background",
			Elements: []*model.Element{
				{Seq: 1, Type: "message", Role: "user", ContentText: "no background token"},
			},
		},
	)
	s.Require().NoError(err)
	s.Require().NoError(opened.Store.Close())
	called := false
	scheduleSessionQueryBackgroundSync = func(*sessionQueryBackgroundSyncRequest) error {
		called = true
		return nil
	}

	err = run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "query", "background",
			"--no-background-sync",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.False(called)
	s.Contains(s.stdout.String(), `"session_id":"codex:query-no-background"`)
}

func (s *CommandSuite) TestSessionQueryFiltersCachedResultsByAgent() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	for _, sess := range []struct {
		agent  model.AgentName
		native string
	}{
		{model.AgentNameCodex, "query-filter-codex"},
		{model.AgentNameHermes, "query-filter-hermes"},
	} {
		_, err = opened.Store.UpsertSessions(context.Background(), &store.UpsertSessionsRequest{
			Agent: sess.agent,
			Sessions: []*model.Session{
				{NativeID: sess.native, Title: "Query filter"},
			},
		})
		s.Require().NoError(err)
		_, err = opened.Store.ReplaceSessionElements(
			context.Background(),
			&store.ReplaceSessionElementsRequest{
				SessionID: string(sess.agent) + ":" + sess.native,
				Elements: []*model.Element{
					{
						Seq:         1,
						Type:        "message",
						Role:        "user",
						ContentText: "sharedtoken query content",
					},
				},
			},
		)
		s.Require().NoError(err)
	}
	s.Require().NoError(opened.Store.Close())

	err = run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "query", "sharedtoken",
			"--agent", "hermes",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"session_id":"hermes:query-filter-hermes"`)
	s.NotContains(s.stdout.String(), `"session_id":"codex:query-filter-codex"`)
}

func (s *CommandSuite) TestSessionQuerySyncsCodexBeforeSearching() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "29")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-smoke-query-sync.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"smoke-query-sync","timestamp":"2026-06-29T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-29T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"needlequery sync transcript"}]}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	called := false
	scheduleSessionQueryBackgroundSync = func(*sessionQueryBackgroundSyncRequest) error {
		called = true
		return nil
	}

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "query", "needlequery",
			"--sync",
			"--format", "jsonl",
			"--timeout", "10s",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.False(called)
	s.Contains(s.stdout.String(), `"session_id":"codex:smoke-query-sync"`)
	s.Contains(s.stdout.String(), `"content_text":"needlequery sync transcript"`)
}

func (s *CommandSuite) TestSessionListFiltersUpdatedSinceAndRendersHTML() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	now := time.Now().UTC()
	s.seedStoredSessions(dbPath, []*model.Session{
		{
			NativeID:  "fresh",
			Title:     "Fresh <session>",
			UpdatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
		},
		{
			NativeID:  "old",
			Title:     "Old",
			UpdatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339),
		},
	})

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "list",
			"--no-sync",
			"--updated-since", "24h",
			"--format", "html",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "codex:fresh")
	s.Contains(s.stdout.String(), "Fresh &lt;session&gt;")
	s.NotContains(s.stdout.String(), "codex:old")
}

func (s *CommandSuite) TestSessionGetRendersCodexTranscript() {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(rolloutDir, "rollout-test-sess-1.jsonl"),
		[]byte(
			`{"type":"session_meta","payload":{"id":"sess-1","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello Codex"}]}}`+"\n"+
				`{"timestamp":"2026-06-20T01:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello back"}]}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.Require().NoError(run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "codex"},
		&s.stdout,
		&s.stderr,
	))
	s.SetupTest()

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "get", "codex:sess-1"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "[user]")
	s.Contains(s.stdout.String(), "Hello Codex")
	s.Contains(s.stdout.String(), "[assistant]")
	s.Contains(s.stdout.String(), "Hello back")
}

func (s *CommandSuite) TestSessionGetWritesHTMLToOutputPath() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	outputPath := filepath.Join(s.T().TempDir(), "session.html")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "get", "codex:sess-1",
			"--format", "html",
			"--output", outputPath,
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Wrote "+outputPath)
	rawHTML, err := os.ReadFile(outputPath)
	s.Require().NoError(err)
	s.Contains(string(rawHTML), "<!doctype html>")
	s.Contains(string(rawHTML), "Bridge context")
}

func (s *CommandSuite) TestSessionGetRendersClaudeJSONL() {
	claudeHome := s.T().TempDir()
	projectDir := filepath.Join(claudeHome, "projects", "sample")
	s.Require().NoError(os.MkdirAll(projectDir, 0o700))
	s.T().Setenv("CLAUDE_HOME", claudeHome)
	s.Require().NoError(os.WriteFile(
		filepath.Join(projectDir, "claude-session.jsonl"),
		[]byte(
			`{"type":"user","sessionId":"claude-session","timestamp":"2026-06-20T02:00:00Z","message":{"role":"user","content":"Hello Claude"}}`+"\n"+
				`{"type":"assistant","sessionId":"claude-session","timestamp":"2026-06-20T02:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi there"}]}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.Require().NoError(run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "claude"},
		&s.stdout,
		&s.stderr,
	))
	s.SetupTest()

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "get", "claude:claude-session", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"role":"user"`)
	s.Contains(s.stdout.String(), `"contentText":"Hello Claude"`)
	s.Contains(s.stdout.String(), `"role":"assistant"`)
	s.Contains(s.stdout.String(), `"contentText":"Hi there"`)
}

func (s *CommandSuite) TestSessionGetJSONLStartsWithSnapshotMetadata() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "get", "codex:sess-1", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	records := decodeJSONLLines(s.T(), s.stdout.String())
	s.Require().GreaterOrEqual(len(records), 2)
	s.Equal("paxl.session.snapshot.v1", records[0]["schemaVersion"])
	s.Equal("codex:sess-1", records[0]["sessionId"])
	s.Equal("codex", records[0]["agent"])
	s.Equal("sess-1", records[0]["nativeId"])
	s.NotZero(records[0]["currentSyncVersion"])
	s.Equal("paxl.session.element.v1", records[1]["schemaVersion"])
	s.Equal(records[0]["currentSyncVersion"], records[1]["syncVersion"])
}

func (s *CommandSuite) TestSessionListJSONLIncludesCurrentSyncVersion() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	s.Require().NoError(run(
		context.Background(),
		[]string{"--db", dbPath, "session", "get", "codex:sess-1", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	))
	s.SetupTest()

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "list",
			"--agent", "codex",
			"--no-sync",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	records := decodeJSONLLines(s.T(), s.stdout.String())
	s.Require().NotEmpty(records)
	s.Equal("paxl.session.metadata.v1", records[0]["schemaVersion"])
	s.Equal("codex:sess-1", records[0]["id"])
	s.NotZero(records[0]["currentSyncVersion"])
}

func (s *CommandSuite) TestSingularCapsuleCommandExposesMigratedSubcommands() {
	cases := []string{"create", "list", "get", "archive", "send", "inject", "injection"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "capsule")
	s.Require().NotNil(command)

	for _, name := range cases {
		s.Run(name, func() {
			s.True(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestInboxCommandExposesEnvelopeSubcommands() {
	cases := []string{"list", "get", "accept", "sync", "watch", "archive"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "inbox")
	s.Require().NotNil(command)

	for _, name := range cases {
		s.Run(name, func() {
			s.True(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestOutboxCommandExposesEnvelopeSubcommands() {
	cases := []string{"list", "get"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "outbox")
	s.Require().NotNil(command)

	for _, name := range cases {
		s.Run(name, func() {
			s.True(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestFriendCommandExposesSubcommands() {
	cases := []string{"request", "list", "accept", "alias", "remove", "block"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "friend")
	s.Require().NotNil(command)

	for _, name := range cases {
		s.Run(name, func() {
			s.True(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestTeamCommandExposesSubcommands() {
	cases := []string{"list", "get", "agents"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "team")
	s.Require().NotNil(command)

	for _, name := range cases {
		s.Run(name, func() {
			s.True(hasCommand(command, name))
		})
	}
}

func (s *CommandSuite) TestCapsuleLocalLifecycleUsesSingularCommands() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "create", "codex:sess-1",
			"--keyword", "bridge",
			"--local",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"keyword":"bridge"`)
	s.Contains(s.stdout.String(), "Bridge context")
	var created map[string]any
	s.Require().NoError(json.Unmarshal(firstLine(s.stdout.Bytes()), &created))
	capsuleID, ok := created["capsuleId"].(string)
	s.Require().True(ok)
	s.NotEmpty(capsuleID)

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "list", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), capsuleID)

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "get", capsuleID},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Knowledge capsule: bridge")
	s.Contains(s.stdout.String(), "Bridge context")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "archive", capsuleID},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Archived "+capsuleID)
}

func (s *CommandSuite) TestCapsuleSendRejectsDirectEmailRecipient() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "send", capsuleID,
			"--to", "other@example.com",
			"--message", "please review",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().Error(err)
	s.Contains(err.Error(), "recipient must be an accepted friend alias like @alice")
}

func (s *CommandSuite) TestCapsuleSendResolvesFriendAlias() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/friends":
			s.Equal("accepted", req.URL.Query().Get("status"))
			s.Equal("bob", req.URL.Query().Get("alias"))
			return commandJSONResponse(`{
				"data":{
					"friends":[{
						"friend_id":"fr_1",
						"requester_user_id":"usr_1",
						"requester_email":"me@example.com",
						"requester_alias":"bob",
						"recipient_user_id":"usr_bob",
						"recipient_email":"bob@example.com",
						"status":"accepted",
						"created_at":"2026-06-22T00:00:00Z"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			body, err := io.ReadAll(req.Body)
			s.Require().NoError(err)
			s.Contains(string(body), `"recipient_email":"bob@example.com"`)
			s.Contains(string(body), `"message":"please review"`)
			s.Contains(
				string(body),
				`"schema_version":"paxl.envelope_payload.knowledge_capsule.v2"`,
			)
			s.Contains(string(body), `"match_type":"project"`)
			s.Contains(string(body), `"match_value":"pax-manager"`)
			s.Contains(string(body), `"target_agent":"codex"`)
			return commandJSONResponse(`{
					"data":{
						"envelope":{
							"envelope_id":"env_1",
							"sender_email":"me@example.com",
							"recipient_email":"bob@example.com",
							"payload_type":"knowledge_capsule",
							"payload_json":{},
							"message":"please review",
							"status":"pending",
							"created_at":"2026-06-22T00:00:00Z"
						}
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "send", capsuleID,
			"--to", "@bob",
			"--message", "please review",
			"--match", "project",
			"--project", "pax-manager",
			"--agent", "codex",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "env_1")
	s.Contains(s.stdout.String(), "please review")
}

func (s *CommandSuite) TestCapsuleSendFromCallerAgentToTeamAgent() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet &&
			req.URL.Path == "/api/v1/user/usr_1/nodes/node_paxl/agents":
			return commandJSONResponse(`{
				"data":{
					"agents":[{
						"agent_id":"agent_from",
						"node_id":"node_paxl",
						"name":"local-codex",
						"agent_type":"codex",
						"status":"active"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			body, err := io.ReadAll(req.Body)
			s.Require().NoError(err)
			s.Contains(string(body), `"from_agent_id":"agent_from"`)
			s.Contains(string(body), `"to_agent_id":"agent_to"`)
			s.NotContains(string(body), "recipient_email")
			return commandJSONResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"from_agent_id":"agent_from",
						"to_agent_id":"agent_to",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"pending",
						"created_at":"2026-06-22T00:00:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"--caller-agent", "codex",
			"capsule", "send", capsuleID,
			"--to-agent-id", "agent_to",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"fromAgentId":"agent_from"`)
	s.Contains(s.stdout.String(), `"toAgentId":"agent_to"`)
}

func (s *CommandSuite) TestInboxCommandsUseManagerEnvelopes() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v1",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"handoff",
			"title":"Remote handoff",
			"summary":"summary",
			"content":"content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		}
	}`, "\n", "")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			s.Equal("pending", req.URL.Query().Get("status"))
			return commandJSONResponse(`{
				"data":{
					"envelopes":[{
						"envelope_id":"env_1",
						"sender_email":"sender@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"message":"please review",
						"status":"pending",
						"created_at":"2026-06-22T00:00:00Z"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1":
			return commandJSONResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"sender_email":"sender@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":%s,
						"message":"please review",
						"status":"pending",
						"created_at":"2026-06-22T00:00:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`, payload)), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/accept":
			return commandJSONResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"accepted",
						"accepted_at":"2026-06-22T00:01:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/archive":
			return commandJSONResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"archived",
						"archived_at":"2026-06-22T00:02:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "inbox", "list"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "env_1")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "inbox", "get", "env_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Remote handoff")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "inbox", "accept", "env_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Accepted env_1 as local capsule")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "inbox", "archive", "env_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Archived env_1")
}

func (s *CommandSuite) TestInboxAcceptAllUsesPendingManagerEnvelopes() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v1",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"handoff",
			"title":"Remote handoff",
			"summary":"summary",
			"content":"content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		}
	}`, "\n", "")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			s.Equal("pending", req.URL.Query().Get("status"))
			return commandJSONResponse(`{
				"data":{
					"envelopes":[
						{"envelope_id":"env_1","payload_type":"knowledge_capsule","status":"pending"},
						{"envelope_id":"env_2","payload_type":"knowledge_capsule","status":"pending"}
					]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet &&
			(req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1" ||
				req.URL.Path == "/api/v1/user/usr_1/envelopes/env_2"):
			envelopeID := strings.TrimPrefix(req.URL.Path, "/api/v1/user/usr_1/envelopes/")
			return commandJSONResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"%s",
						"sender_email":"sender@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":%s,
						"status":"pending"
					}
				},
				"code":200,
				"message":"ok"
			}`, envelopeID, payload)), nil
		case req.Method == http.MethodPost &&
			(req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/accept" ||
				req.URL.Path == "/api/v1/user/usr_1/envelopes/env_2/accept"):
			envelopeID := strings.TrimSuffix(
				strings.TrimPrefix(req.URL.Path, "/api/v1/user/usr_1/envelopes/"),
				"/accept",
			)
			return commandJSONResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"%s",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"accepted"
					}
				},
				"code":200,
				"message":"ok"
			}`, envelopeID)), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "inbox", "accept", "--all"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Accepted env_1 as local capsule")
	s.Contains(s.stdout.String(), "Accepted env_2 as local capsule")
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	listed, err := opened.Store.ListKnowledgeCapsules(
		context.Background(),
		&store.ListKnowledgeCapsulesRequest{},
	)
	s.Require().NoError(err)
	s.Len(listed.Capsules, 2)
}

func (s *CommandSuite) TestInboxSyncUsesAcceptedManagerEnvelopes() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"monitor",
			"title":"Monitor audit frontend integration",
			"summary":"summary",
			"content":"content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		},
		"route":{
			"match_type":"keyword",
			"match_value":"monitor",
			"target_agent":"codex"
		}
	}`, "\n", "")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			s.Equal("accepted", req.URL.Query().Get("status"))
			return commandJSONResponse(`{
				"data":{
					"envelopes":[
						{"envelope_id":"env_1","payload_type":"knowledge_capsule","status":"accepted"}
					]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1":
			return commandJSONResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"sender_email":"sender@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":%s,
						"status":"accepted"
					}
				},
				"code":200,
				"message":"ok"
			}`, payload)), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "inbox", "sync"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Synced env_1 as local capsule")
	s.Contains(s.stdout.String(), "Hook injection route")
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	listed, err := opened.Store.ListKnowledgeCapsules(
		context.Background(),
		&store.ListKnowledgeCapsulesRequest{SourceSessionID: "remote_envelope:env_1"},
	)
	s.Require().NoError(err)
	s.Require().Len(listed.Capsules, 1)
	s.Equal("Monitor audit frontend integration", listed.Capsules[0].Title)
	injections, err := opened.Store.ListKnowledgeInjections(
		context.Background(),
		&store.ListKnowledgeInjectionsRequest{},
	)
	s.Require().NoError(err)
	s.Require().Len(injections.Injections, 1)
}

func (s *CommandSuite) TestInboxWatchAcceptsPendingEnvelopesUntilContextCanceled() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v1",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"handoff",
			"title":"Remote handoff",
			"summary":"summary",
			"content":"content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		}
	}`, "\n", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			s.Equal("pending", req.URL.Query().Get("status"))
			return commandJSONResponse(`{
				"data":{
					"envelopes":[
						{"envelope_id":"env_1","payload_type":"knowledge_capsule","status":"pending"}
					]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet &&
			req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1":
			return commandJSONResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"sender_email":"sender@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":%s,
						"status":"pending"
					}
				},
				"code":200,
				"message":"ok"
			}`, payload)), nil
		case req.Method == http.MethodPost &&
			req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/accept":
			cancel()
			return commandJSONResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"accepted"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		ctx,
		[]string{"--db", dbPath, "inbox", "watch", "--interval", "1h"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Watching inbox every 1h0m0s")
	s.Contains(s.stdout.String(), "Received 1 pending envelope(s)")
	s.Contains(s.stdout.String(), "Accepted env_1 as local capsule")
	s.Contains(s.stdout.String(), "Auto accepted 1 envelope(s)")
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	listed, err := opened.Store.ListKnowledgeCapsules(
		context.Background(),
		&store.ListKnowledgeCapsulesRequest{},
	)
	s.Require().NoError(err)
	s.Len(listed.Capsules, 1)
}

func (s *CommandSuite) TestOutboxCommandsUseManagerEnvelopes() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			s.Equal("sent", req.URL.Query().Get("direction"))
			s.Equal("accepted", req.URL.Query().Get("status"))
			return commandJSONResponse(`{
				"data":{
					"envelopes":[{
						"envelope_id":"env_1",
						"sender_email":"me@example.com",
						"recipient_email":"recipient@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"message":"please review",
						"status":"accepted",
						"created_at":"2026-06-22T00:00:00Z",
						"accepted_at":"2026-06-22T00:01:00Z"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1":
			return commandJSONResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"sender_email":"me@example.com",
						"recipient_email":"recipient@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"message":"please review",
						"status":"accepted",
						"created_at":"2026-06-22T00:00:00Z",
						"accepted_at":"2026-06-22T00:01:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "outbox", "list", "--status", "accepted"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "env_1")
	s.Contains(s.stdout.String(), "recipient@example.com")
	s.Contains(s.stdout.String(), "accepted")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "outbox", "get", "env_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "recipient@example.com")
	s.Contains(s.stdout.String(), "accepted")
}

func (s *CommandSuite) TestFriendCommandsUseManagerFriends() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/friends":
			body, err := io.ReadAll(req.Body)
			s.Require().NoError(err)
			s.Contains(string(body), `"email":"bob@example.com"`)
			s.Contains(string(body), `"alias":"bob"`)
			return commandJSONResponse(`{
				"data":{
					"friend":{
						"friend_id":"fr_1",
						"requester_user_id":"usr_1",
						"requester_email":"me@example.com",
						"requester_alias":"bob",
						"recipient_email":"bob@example.com",
						"status":"pending",
						"created_at":"2026-06-22T00:00:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/friends":
			s.Equal("accepted", req.URL.Query().Get("status"))
			return commandJSONResponse(`{
				"data":{
					"friends":[{
						"friend_id":"fr_1",
						"requester_user_id":"usr_1",
						"requester_email":"me@example.com",
						"requester_alias":"bob",
						"recipient_user_id":"usr_bob",
						"recipient_email":"bob@example.com",
						"status":"accepted",
						"created_at":"2026-06-22T00:00:00Z"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/friends/fr_1/accept":
			body, err := io.ReadAll(req.Body)
			s.Require().NoError(err)
			s.Contains(string(body), `"alias":"bob"`)
			return commandJSONResponse(friendCommandResponse("accepted")), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/friends/fr_1/alias":
			body, err := io.ReadAll(req.Body)
			s.Require().NoError(err)
			s.Contains(string(body), `"alias":"teammate"`)
			return commandJSONResponse(friendCommandResponseWithAliases(
				"accepted",
				"alice",
				"teammate",
			)), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/friends/fr_1/remove":
			return commandJSONResponse(friendCommandResponse("removed")), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/friends/fr_1/block":
			return commandJSONResponse(friendCommandResponse("blocked")), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "friend", "request", "bob@example.com", "--alias", "bob"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "fr_1")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "friend", "list"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "bob@example.com")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "friend", "accept", "fr_1", "--alias", "bob"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "accepted")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "friend", "alias", "fr_1", "@teammate"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "teammate")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "friend", "remove", "fr_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "removed")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "friend", "block", "fr_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "blocked")
}

func (s *CommandSuite) TestTeamCommandsUseManagerTeams() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	restoreHTTPClient := s.stubDefaultHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/teams":
			return commandJSONResponse(`{
				"data":{
					"teams":[{
						"team_id":"team_1",
						"name":"Alpha",
						"owner_user_id":"usr_1",
						"my_role":"owner",
						"member_count":2,
						"agent_count":1,
						"status":"active",
						"created_at":"2026-06-22T00:00:00Z"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/teams/team_1":
			return commandJSONResponse(`{
				"data":{
					"team":{
						"team_id":"team_1",
						"name":"Alpha",
						"owner_user_id":"usr_1",
						"status":"active",
						"created_at":"2026-06-22T00:00:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/teams/team_1/agents":
			return commandJSONResponse(`{
				"data":{
					"agents":[{
						"team_id":"team_1",
						"agent_id":"agent_mate",
						"agent_owner_user_id":"usr_mate",
						"added_by_user_id":"usr_1",
						"added_at":"2026-06-23T00:00:00Z",
						"agent":{"agent_id":"agent_mate","name":"mate-claude","online":true}
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("unexpected request")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		}
	})
	defer restoreHTTPClient()
	s.seedManagerCredential(dbPath, "https://manager.example")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "team", "list"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "team_1")
	s.Contains(s.stdout.String(), "Alpha")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "team", "get", "team_1"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "team_1")
	s.Contains(s.stdout.String(), "Alpha")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "team", "agents", "--all"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "agent_mate")
	s.Contains(s.stdout.String(), "Alpha")
}

func (s *CommandSuite) TestCapsuleCreateSupportsContentFlag() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "create", "codex:sess-1",
			"--keyword", "installer hosting",
			"--title", "paxl installer hosting",
			"--summary", "Installer upload and hosting requirement.",
			"--content", "The paxl installer should be uploaded and hosted at GCS.",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"title":"paxl installer hosting"`)
	s.Contains(s.stdout.String(), "The paxl installer should be uploaded and hosted at GCS.")
	s.NotContains(s.stdout.String(), "Bridge context")
}

func (s *CommandSuite) TestCapsuleCreateSupportsManualStdinContent() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := runWithInput(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "create",
			"--manual",
			"--keyword", "routed envelopes",
			"--format", "jsonl",
		},
		strings.NewReader("Pax-manager must preserve routed envelope metadata on accept."),
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"sourceSessionId":"manual"`)
	s.Contains(s.stdout.String(), `"sourceAgent":"paxl"`)
	s.Contains(s.stdout.String(), `"title":"Knowledge capsule: routed envelopes"`)
	s.Contains(s.stdout.String(), "Pax-manager must preserve routed envelope metadata on accept.")
}

func (s *CommandSuite) TestCapsuleCreateHelpDoesNotExposeContentFile() {
	err := run(context.Background(), []string{"capsule", "create", "--help"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "--content")
	s.NotContains(s.stdout.String(), "--content-file")
}

func (s *CommandSuite) TestCapsuleCreateUsesSourceAgentGenerationByDefault() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath, rolloutPath := s.seedCodexSessionWithKeywordAndRollout("bridge")
	s.installFakeCodexCapsuleGenerator(rolloutPath)

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "create", "codex:sess-1",
			"--keyword", "bridge",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"title":"Generated bridge"`)
	s.Contains(s.stdout.String(), "Generated bridge content")
}

func (s *CommandSuite) TestSessionMirrorDeliversHandoffToExistingSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	s.seedCodexTargetSession(dbPath)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "mirror", "codex:sess-1",
			"--to-session", "codex:target",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.session.mirror.v1"`)
	s.Contains(s.stdout.String(), `"sourceNodeId":"local-node"`)
	s.Contains(s.stdout.String(), `"targetNodeId":"local-node"`)
	s.Contains(s.stdout.String(), `"targetSessionId":"codex:target"`)
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(string(rawPrompt), "system_handoff")
	s.Contains(string(rawPrompt), "This context was mirrored by paxl")
	s.Contains(string(rawPrompt), "From:\nNode: local-node\nAgent: codex\nSession: codex:sess-1")
	s.Contains(string(rawPrompt), "To:\nNode: local-node\nAgent: codex\nSession: codex:target")
	s.Contains(string(rawPrompt), "Bridge context")
	s.Contains(string(rawPrompt), "Bridge answer")
}

func (s *CommandSuite) TestSessionMirrorUsesLocalCollaborationFacade() {
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	oldFactory := newLocalCollaborationFacade
	fakeFacade := &fakeLocalCollaborationFacade{
		resp: &facade.MoveSessionContextResponse{
			Capsule: &model.KnowledgeCapsule{
				CapsuleID:       "kcap_1",
				SourceNodeID:    "node_source",
				SourceSessionID: "codex:sess-1",
				SourceAgent:     model.AgentNameCodex,
				Title:           "Session mirror: codex:sess-1",
			},
			Injection: &model.KnowledgeInjection{
				InjectionID:     "mir_1",
				TargetNodeID:    "node_target",
				TargetAgent:     model.AgentNameClaude,
				TargetSessionID: "claude:target",
				DeliveryMethod:  "cli_resume",
			},
			Message: "system_handoff",
		},
	}
	newLocalCollaborationFacade = func(_ *store.Store) localCollaborationFacade {
		return fakeFacade
	}
	s.T().Cleanup(func() { newLocalCollaborationFacade = oldFactory })

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "mirror", "codex:sess-1",
			"--to-session", "claude:target",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Require().NotNil(fakeFacade.req)
	s.Equal("codex:sess-1", fakeFacade.req.SourceSessionID)
	s.Equal("claude:target", fakeFacade.req.TargetSessionID)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.session.mirror.v1"`)
	s.Contains(s.stdout.String(), `"mirrorId":"mir_1"`)
}

func (s *CommandSuite) TestSessionMirrorCollaborationConversionHelpers() {
	req := moveSessionContextRequestFromMirror(&facade.MirrorSessionRequest{
		SourceSessionID: "codex:sess-1",
		Agent:           model.AgentNameCodex,
		TargetSessionID: "claude:target",
		TargetAgent:     model.AgentNameClaude,
	})

	s.Equal("codex:sess-1", req.SourceSessionID)
	s.Equal(model.AgentNameCodex, req.SourceAgent)
	s.Equal("claude:target", req.TargetSessionID)
	s.Equal(model.AgentNameClaude, req.TargetAgent)
	s.Nil(moveSessionContextRequestFromMirror(nil))

	resp := mirrorSessionResponseFromMoveContext(&facade.MoveSessionContextResponse{
		Capsule:   &model.KnowledgeCapsule{CapsuleID: "kcap_1"},
		Injection: &model.KnowledgeInjection{InjectionID: "mir_1"},
		Message:   "system_handoff",
	})

	s.Equal("kcap_1", resp.Capsule.CapsuleID)
	s.Equal("mir_1", resp.Injection.InjectionID)
	s.Equal("system_handoff", resp.Message)
	s.Nil(mirrorSessionResponseFromMoveContext(nil))
}

func (s *CommandSuite) TestSessionMirrorStartsNewTargetSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeClaude(capturePath)

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"session", "mirror", "codex:sess-1",
			"--to", "claude",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "cli_new_session")
	s.Contains(s.stdout.String(), "new claude session")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(
		string(rawPrompt),
		"To:\nNode: local-node\nAgent: claude\nSession: (new claude session)",
	)
	s.Contains(string(rawPrompt), "Bridge context")
	s.Contains(string(rawPrompt), "Bridge answer")
}

func (s *CommandSuite) TestSessionMirrorHelpDoesNotExposeKeyword() {
	err := run(context.Background(), []string{"session", "mirror", "--help"}, &s.stdout, &s.stderr)

	s.Require().NoError(err)
	s.NotContains(s.stdout.String(), "--keyword")
}

func (s *CommandSuite) TestSessionMirrorRejectsInvalidRequests() {
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing source", args: []string{"session", "mirror", "--to", "codex"}},
		{name: "missing target", args: []string{"session", "mirror", "codex:sess"}},
		{
			name: "unknown target agent",
			args: []string{"session", "mirror", "codex:sess", "--to", "qwen"},
		},
		{
			name: "unknown format",
			args: []string{"session", "mirror", "codex:sess", "--to", "codex", "--format", "xml"},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := run(context.Background(), tc.args, &s.stdout, &s.stderr)
			s.Error(err)
		})
	}
}

func (s *CommandSuite) TestTimeoutFlagsRejectInvalidDurations() {
	cases := []struct {
		name string
		args []string
	}{
		{name: "session list", args: []string{"session", "list", "--timeout", "bad"}},
		{
			name: "session mirror",
			args: []string{"session", "mirror", "codex:sess", "--to", "codex", "--timeout", "bad"},
		},
		{
			name: "capsule create",
			args: []string{
				"capsule",
				"create",
				"codex:sess",
				"--keyword",
				"bridge",
				"--timeout",
				"bad",
			},
		},
		{
			name: "capsule inject",
			args: []string{"capsule", "inject", "kcap_1", "codex:target", "--timeout", "bad"},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := run(context.Background(), tc.args, &s.stdout, &s.stderr)
			s.Error(err)
		})
	}
}

func (s *CommandSuite) TestCapsuleCreateRejectsInvalidDebugStackAfter() {
	err := run(
		context.Background(),
		[]string{
			"capsule", "create", "codex:sess",
			"--keyword", "bridge",
			"--debug-stack-after", "bad",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleInjectQueuesTargetSessionAndHookDeliversHandoff() {
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "inject", capsuleID, "codex:target"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Queued")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{
			"--db",
			dbPath,
			"capsule",
			"injection",
			"--target-session",
			"codex:target",
			"--format",
			"jsonl",
		},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"targetSessionId":"codex:target"`)
	s.Contains(s.stdout.String(), `"sourceNodeId":"local-node"`)
	s.Contains(s.stdout.String(), `"targetNodeId":"local-node"`)
	s.Contains(s.stdout.String(), `"sourceSessionId":"codex:sess-1"`)
	s.Contains(s.stdout.String(), `"capsuleId":"`+capsuleID+`"`)
	s.Contains(s.stdout.String(), `"actionItems":null`)
	s.Contains(s.stdout.String(), `"deliveryMethod":"hook"`)
	s.Contains(s.stdout.String(), `"status":"pending"`)
	s.Contains(s.stdout.String(), `"routeMatchType":"session"`)
	s.Contains(s.stdout.String(), `"routeMatchValue":"codex:target"`)

	s.SetupTest()
	s.withStdinJSON(map[string]string{
		"sessionId":  "target",
		"userPrompt": "continue",
		"projectId":  "/tmp/project",
	}, func() {
		err = run(
			context.Background(),
			[]string{"--db", dbPath, "__agent-hook", "--agent", "codex", "--event", "user-prompt"},
			&s.stdout,
			&s.stderr,
		)
	})
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "system_handoff")
	s.Contains(s.stdout.String(), "Bridge context")
}

func (s *CommandSuite) TestCapsuleInjectIncludesActionItemsFlag() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID, "codex:target",
			"--action-items", "run go test ./...",
			"--action-items", "open a PR",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.SetupTest()
	s.withStdinJSON(map[string]string{
		"sessionId":  "target",
		"userPrompt": "continue",
	}, func() {
		err = run(
			context.Background(),
			[]string{"--db", dbPath, "__agent-hook", "--agent", "codex", "--event", "user-prompt"},
			&s.stdout,
			&s.stderr,
		)
	})
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "ACTION ITEMS")
	s.Contains(s.stdout.String(), "1. run go test ./...")
	s.Contains(s.stdout.String(), "2. open a PR")
	s.NotContains(s.stdout.String(), "NO ACTIONABLE ITEMS")
}

func (s *CommandSuite) TestCapsuleInjectQueuesMatchedActionItems() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID,
			"--match", "any",
			"--action-items", "run hook tests",
			"--action-items", "open the hook PR",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Queued")
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	listed, err := opened.Store.ListKnowledgeInjections(
		context.Background(),
		&store.ListKnowledgeInjectionsRequest{},
	)
	s.Require().NoError(err)
	s.Require().Len(listed.Injections, 1)
	s.Equal("hook", listed.Injections[0].DeliveryMethod)
	s.JSONEq(`["run hook tests","open the hook PR"]`, listed.Injections[0].ActionItemsJSON)
}

func (s *CommandSuite) TestCapsuleInjectStartsNewTargetSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeClaude(capturePath)

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID,
			"--new",
			"--agent", "claude",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Injected")
	s.Contains(s.stdout.String(), "new claude session")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(string(rawPrompt), "system_handoff")
	s.Contains(string(rawPrompt), "Bridge context")

	s.SetupTest()
	err = run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "injection", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"deliveryMethod":"cli_new_session"`)
	s.Contains(s.stdout.String(), `"targetAgent":"claude"`)
}

func (s *CommandSuite) TestCapsuleInjectStartsNewKiroTargetSession() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	argsPath := filepath.Join(s.T().TempDir(), "args.txt")
	s.installArgCapturingFakeCommand("kiro-cli", argsPath)

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID,
			"--new",
			"--agent", "kiro",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "new kiro session")
	rawArgs, err := os.ReadFile(argsPath)
	s.Require().NoError(err)
	s.Contains(string(rawArgs), "chat\n--no-interactive\nsystem_handoff")
	s.Contains(string(rawArgs), "Bridge context")
}

func (s *CommandSuite) TestCapsuleInjectRejectsOutputPathForQueuedTargetSession() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)
	outputPath := filepath.Join(s.T().TempDir(), "handoff.txt")

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "inject", capsuleID, "codex:target",
			"--output", outputPath,
		},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
	s.NoFileExists(outputPath)
}

func (s *CommandSuite) TestCapsuleInjectionListSupportsTableFormat() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)
	s.Require().NoError(run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "inject", capsuleID, "codex:target"},
		&s.stdout,
		&s.stderr,
	))
	s.SetupTest()

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "injection", "--format", "table"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "codex:target")
}

func (s *CommandSuite) TestCapsuleCreateRejectsMissingKeyword() {
	err := run(
		context.Background(),
		[]string{"capsule", "create", "codex:sess", "--local"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleCreateRejectsMissingSourceSession() {
	err := run(
		context.Background(),
		[]string{"capsule", "create", "--keyword", "bridge"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleCreateRejectsManualWithSourceSession() {
	err := run(
		context.Background(),
		[]string{
			"capsule", "create", "codex:sess",
			"--manual",
			"--keyword", "bridge",
			"--content", "manual content",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleCreateRejectsManualWithoutContent() {
	err := run(
		context.Background(),
		[]string{"capsule", "create", "--manual", "--keyword", "bridge"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleCreateRejectsUnknownAgent() {
	err := run(
		context.Background(),
		[]string{"capsule", "create", "sess", "--agent", "qwen", "--keyword", "bridge"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleCreateRejectsUnknownFormat() {
	err := run(
		context.Background(),
		[]string{
			"capsule",
			"create",
			"codex:sess",
			"--keyword",
			"bridge",
			"--local",
			"--format",
			"xml",
		},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleGetRejectsMissingCapsuleID() {
	err := run(context.Background(), []string{"capsule", "get"}, &s.stdout, &s.stderr)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleArchiveRejectsMissingCapsuleID() {
	err := run(context.Background(), []string{"capsule", "archive"}, &s.stdout, &s.stderr)

	s.Error(err)
}

func (s *CommandSuite) TestCapsuleInjectRejectsMissingArguments() {
	cases := []struct {
		name string
		args []string
	}{
		{name: "empty", args: []string{"capsule", "inject"}},
		{name: "only capsule", args: []string{"capsule", "inject", "kcap_1"}},
		{name: "new without agent", args: []string{"capsule", "inject", "kcap_1", "--new"}},
		{
			name: "new with target",
			args: []string{
				"capsule", "inject", "kcap_1", "target", "--new", "--agent", "codex",
			},
		},
		{
			name: "match with target",
			args: []string{"capsule", "inject", "kcap_1", "target", "--match", "any"},
		},
		{
			name: "match project missing value",
			args: []string{"capsule", "inject", "kcap_1", "--match", "project"},
		},
		{
			name: "match keyword missing value",
			args: []string{"capsule", "inject", "kcap_1", "--match", "keyword"},
		},
		{
			name: "match unsupported",
			args: []string{"capsule", "inject", "kcap_1", "--match", "session"},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := run(context.Background(), tc.args, &s.stdout, &s.stderr)
			s.Error(err)
		})
	}
}

func (s *CommandSuite) TestCapsuleInjectRejectsUnknownAgent() {
	err := run(
		context.Background(),
		[]string{"capsule", "inject", "kcap_1", "target", "--agent", "qwen"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestEnvelopeCommandsRejectInvalidRequestsBeforeIO() {
	cases := []struct {
		name string
		args []string
	}{
		{name: "send missing capsule", args: []string{"capsule", "send"}},
		{name: "send missing recipient", args: []string{"capsule", "send", "kcap_1"}},
		{
			name: "send unknown format",
			args: []string{
				"capsule",
				"send",
				"kcap_1",
				"--to",
				"you@example.com",
				"--format",
				"xml",
			},
		},
		{name: "get missing envelope", args: []string{"inbox", "get"}},
		{name: "get unknown format", args: []string{"inbox", "get", "env_1", "--format", "xml"}},
		{name: "accept missing envelope", args: []string{"inbox", "accept"}},
		{
			name: "accept unknown format",
			args: []string{"inbox", "accept", "env_1", "--format", "xml"},
		},
		{name: "sync unknown format", args: []string{"inbox", "sync", "--format", "xml"}},
		{name: "archive missing envelope", args: []string{"inbox", "archive"}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := run(context.Background(), tc.args, &s.stdout, &s.stderr)
			s.Error(err)
		})
	}
}

func (s *CommandSuite) TestFriendCommandsRejectInvalidRequestsBeforeIO() {
	cases := []struct {
		name string
		args []string
	}{
		{name: "request missing email", args: []string{"friend", "request"}},
		{
			name: "request unknown format",
			args: []string{"friend", "request", "bob@example.com", "--format", "xml"},
		},
		{name: "list unknown direction", args: []string{"friend", "list", "--direction", "both"}},
		{name: "accept missing friend", args: []string{"friend", "accept"}},
		{
			name: "accept unknown format",
			args: []string{"friend", "accept", "fr_1", "--format", "xml"},
		},
		{name: "alias missing friend", args: []string{"friend", "alias"}},
		{name: "alias missing alias", args: []string{"friend", "alias", "fr_1"}},
		{
			name: "alias unknown format",
			args: []string{"friend", "alias", "fr_1", "bob", "--format", "xml"},
		},
		{name: "remove missing friend", args: []string{"friend", "remove"}},
		{name: "block missing friend", args: []string{"friend", "block"}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := run(context.Background(), tc.args, &s.stdout, &s.stderr)
			s.Error(err)
		})
	}
}

func (s *CommandSuite) TestTeamCommandsRejectInvalidRequestsBeforeIO() {
	cases := []struct {
		name string
		args []string
	}{
		{name: "get missing team", args: []string{"team", "get"}},
		{
			name: "get unknown format",
			args: []string{"team", "get", "team_1", "--format", "xml"},
		},
		{name: "agents neither arg nor all", args: []string{"team", "agents"}},
		{
			name: "agents unknown format",
			args: []string{"team", "agents", "team_1", "--format", "xml"},
		},
		{
			name: "agents agent without all",
			args: []string{"team", "agents", "team_1", "--agent", "foo"},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			err := run(context.Background(), tc.args, &s.stdout, &s.stderr)
			s.Error(err)
		})
	}
}

func (s *CommandSuite) TestRenderCapsuleListSupportsTableFormat() {
	err := renderCapsuleList(&s.stdout, &facade.ListCapsulesResponse{
		Capsules: []*model.KnowledgeCapsule{
			{
				CapsuleID:       "kcap_1",
				Status:          "active",
				SourceSessionID: "codex:sess",
				Keyword:         "bridge",
				Title:           "Bridge",
				CreatedAt:       "2026-06-20T01:00:00Z",
			},
		},
	}, "table")

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "kcap_1")
}

func (s *CommandSuite) TestRenderCapsuleListRejectsUnknownFormat() {
	err := renderCapsuleList(&s.stdout, &facade.ListCapsulesResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderCapsuleSupportsJSONLFormat() {
	err := renderCapsule(&s.stdout, &facade.GetCapsuleResponse{
		Capsule: &model.KnowledgeCapsule{
			CapsuleID:    "kcap_1",
			SourceNodeID: "source-node",
			Keyword:      "bridge",
			Content:      "Bridge context",
		},
	}, "jsonl")

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"capsuleId":"kcap_1"`)
	s.Contains(s.stdout.String(), `"sourceNodeId":"source-node"`)
}

func (s *CommandSuite) TestRenderCapsuleRejectsUnknownFormat() {
	err := renderCapsule(&s.stdout, &facade.GetCapsuleResponse{
		Capsule: &model.KnowledgeCapsule{CapsuleID: "kcap_1"},
	}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderEnvelopeListSupportsTableFormat() {
	err := renderEnvelopeList(&s.stdout, &facade.ListInboxResponse{
		Envelopes: []*model.Envelope{
			{
				EnvelopeID:  "env_1",
				SenderEmail: "sender@example.com",
				Message:     "please review",
				Status:      "pending",
				CreatedAt:   "2026-06-22T00:00:00Z",
			},
		},
	}, "table")

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "env_1")
	s.Contains(s.stdout.String(), "sender@example.com")
	s.Contains(s.stdout.String(), "please review")
}

func (s *CommandSuite) TestRenderOutboxEnvelopeListSupportsTableFormat() {
	err := renderOutboxEnvelopeList(&s.stdout, &facade.ListOutboxResponse{
		Envelopes: []*model.Envelope{
			{
				EnvelopeID:     "env_1",
				RecipientEmail: "recipient@example.com",
				Message:        "please review",
				Status:         "accepted",
				CreatedAt:      "2026-06-22T00:00:00Z",
			},
		},
	}, "table")

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "TO")
	s.Contains(s.stdout.String(), "env_1")
	s.Contains(s.stdout.String(), "recipient@example.com")
	s.Contains(s.stdout.String(), "accepted")
}

func (s *CommandSuite) TestRenderEnvelopeListSupportsJSONLFormat() {
	err := renderEnvelopeList(&s.stdout, &facade.ListInboxResponse{
		Envelopes: []*model.Envelope{
			{
				EnvelopeID:  "env_1",
				PayloadType: "knowledge_capsule",
				PayloadJSON: json.RawMessage(`{"capsule":{}}`),
				Status:      "pending",
			},
		},
	}, "jsonl")

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.envelope.v1"`)
	s.Contains(s.stdout.String(), `"envelopeId":"env_1"`)
}

func (s *CommandSuite) TestRenderEnvelopeListRejectsUnknownFormat() {
	err := renderEnvelopeList(&s.stdout, &facade.ListInboxResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderEnvelopeSupportsTextAndJSONLFormats() {
	envelope := &model.Envelope{
		EnvelopeID:     "env_1",
		SenderUserID:   "usr_sender",
		RecipientEmail: "me@example.com",
		PayloadType:    "knowledge_capsule",
		PayloadJSON:    json.RawMessage(`{"capsule":{"title":"Bridge"}}`),
		Message:        "please review",
		Status:         "pending",
		CreatedAt:      "2026-06-22T00:00:00Z",
		AcceptedAt:     "2026-06-22T00:01:00Z",
		ArchivedAt:     "2026-06-22T00:02:00Z",
	}

	err := renderEnvelope(&s.stdout, envelope, "text")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Envelope: env_1")
	s.Contains(s.stdout.String(), "Payload JSON:")

	s.SetupTest()
	err = renderEnvelope(&s.stdout, envelope, "jsonl")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"envelopeId":"env_1"`)
	s.Contains(s.stdout.String(), `"acceptedAt":"2026-06-22T00:01:00Z"`)
}

func (s *CommandSuite) TestRenderEnvelopeRejectsUnknownFormat() {
	err := renderEnvelope(&s.stdout, &model.Envelope{EnvelopeID: "env_1"}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderAcceptEnvelopeSupportsFormats() {
	resp := &facade.AcceptEnvelopeResponse{
		Envelope: &model.Envelope{EnvelopeID: "env_1", Status: "accepted"},
		Capsule:  &model.KnowledgeCapsule{CapsuleID: "kcap_1", Title: "Bridge"},
		Injection: &model.KnowledgeInjection{
			InjectionID:     "kci_1",
			Status:          "pending",
			TargetAgent:     model.AgentNameCodex,
			RouteMatchType:  "project",
			RouteMatchValue: "pax-manager",
		},
	}

	err := renderAcceptEnvelope(&s.stdout, resp, "table")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Accepted env_1 as local capsule kcap_1")
	s.Contains(s.stdout.String(), "Queued hook injection route kci_1")

	s.SetupTest()
	err = renderAcceptEnvelope(&s.stdout, resp, "jsonl")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"envelopeId":"env_1"`)
	s.Contains(s.stdout.String(), `"capsuleId":"kcap_1"`)
	s.Contains(s.stdout.String(), `"injectionId":"kci_1"`)
}

func (s *CommandSuite) TestRenderAcceptEnvelopeRejectsUnknownFormat() {
	err := renderAcceptEnvelope(&s.stdout, &facade.AcceptEnvelopeResponse{
		Envelope: &model.Envelope{EnvelopeID: "env_1"},
		Capsule:  &model.KnowledgeCapsule{CapsuleID: "kcap_1"},
	}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderAcceptAllEnvelopesSupportsFormats() {
	resp := &facade.AcceptAllEnvelopesResponse{
		Accepted: []*facade.AcceptEnvelopeResponse{
			{
				Envelope: &model.Envelope{EnvelopeID: "env_1", Status: "accepted"},
				Capsule:  &model.KnowledgeCapsule{CapsuleID: "kcap_1", Title: "Bridge"},
			},
		},
		Failures: []*facade.AcceptEnvelopeFailure{
			{EnvelopeID: "env_2", Error: "network unavailable"},
		},
	}

	err := renderAcceptAllEnvelopes(&s.stdout, resp, "table")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Accepted env_1 as local capsule kcap_1")
	s.Contains(s.stdout.String(), "Failed env_2: network unavailable")

	s.SetupTest()
	err = renderAcceptAllEnvelopes(&s.stdout, resp, "jsonl")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"envelopeId":"env_1"`)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.accept_failure.v1"`)
	s.Contains(s.stdout.String(), `"error":"network unavailable"`)
}

func (s *CommandSuite) TestRenderAcceptAllEnvelopesHandlesEmptyTable() {
	err := renderAcceptAllEnvelopes(
		&s.stdout,
		&facade.AcceptAllEnvelopesResponse{},
		"table",
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "No pending envelopes to accept")
}

func (s *CommandSuite) TestRenderAcceptAllEnvelopesRejectsUnknownFormat() {
	err := renderAcceptAllEnvelopes(
		&s.stdout,
		&facade.AcceptAllEnvelopesResponse{},
		"xml",
	)

	s.Error(err)
}

func (s *CommandSuite) TestRenderInboxWatchEventSupportsFormats() {
	err := renderInboxWatchEvent(&s.stdout, "table", "started", "30s", 0)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Watching inbox every 30s")

	s.SetupTest()
	err = renderInboxWatchEvent(&s.stdout, "table", "received", "", 3)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Received 3 pending envelope(s)")

	s.SetupTest()
	err = renderInboxWatchEvent(&s.stdout, "table", "auto_accepted", "", 2)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Auto accepted 2 envelope(s)")

	s.SetupTest()
	err = renderInboxWatchEvent(&s.stdout, "table", "failed", "", 1)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Failed to accept 1 envelope(s)")

	s.SetupTest()
	err = renderInboxWatchEvent(&s.stdout, "table", "custom_event", "", 0)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "custom_event")

	s.SetupTest()
	err = renderInboxWatchEvent(&s.stdout, "jsonl", "started", "30s", 0)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.inbox_watch_event.v1"`)
	s.Contains(s.stdout.String(), `"event":"started"`)
	s.Contains(s.stdout.String(), `"detail":"30s"`)

	s.SetupTest()
	err = renderInboxWatchEvent(&s.stdout, "jsonl", "received", "", 4)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"count":4`)
}

func (s *CommandSuite) TestRenderInboxWatchEventRejectsUnknownFormat() {
	err := renderInboxWatchEvent(&s.stdout, "xml", "started", "30s", 0)

	s.Error(err)
}

func (s *CommandSuite) TestParseWatchIntervalRejectsInvalidValues() {
	interval, err := parseWatchInterval(" 250ms ")
	s.Require().NoError(err)
	s.Equal(250*time.Millisecond, interval)

	_, err = parseWatchInterval("0s")
	s.Error(err)

	_, err = parseWatchInterval("not-a-duration")
	s.Error(err)
}

func (s *CommandSuite) TestInjectionActionItemsCleansJSON() {
	items := injectionActionItems(&model.KnowledgeInjection{
		ActionItemsJSON: `[" ship it ","","write tests"]`,
	})

	s.Equal([]string{"ship it", "write tests"}, items)
	s.Nil(injectionActionItems(nil))
	s.Nil(injectionActionItems(&model.KnowledgeInjection{}))
	s.Nil(injectionActionItems(&model.KnowledgeInjection{ActionItemsJSON: `{"bad":true}`}))
}

func (s *CommandSuite) TestRenderFriendListSupportsFormats() {
	resp := &facade.ListFriendsResponse{
		UserID: "usr_1",
		Friends: []*model.Friend{
			{
				FriendID:        "fr_1",
				RequesterUserID: "usr_1",
				RequesterEmail:  "me@example.com",
				RequesterAlias:  "bob",
				RecipientEmail:  "bob@example.com",
				Status:          "accepted",
				CreatedAt:       "2026-06-22T00:00:00Z",
			},
		},
	}

	err := renderFriendList(&s.stdout, resp, "table")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "fr_1")
	s.Contains(s.stdout.String(), "bob@example.com")

	s.SetupTest()
	err = renderFriendList(&s.stdout, resp, "jsonl")
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.friend.v1"`)
	s.Contains(s.stdout.String(), `"alias":"bob"`)
}

func (s *CommandSuite) TestRenderFriendListRejectsUnknownFormat() {
	err := renderFriendList(&s.stdout, &facade.ListFriendsResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderInjectionListSupportsFormats() {
	resp := &facade.ListInjectionsResponse{
		Injections: []*model.KnowledgeInjection{
			{
				InjectionID:         "kci_1",
				CapsuleID:           "kcap_1",
				SourceNodeID:        "source-node",
				SourceAgent:         model.AgentNameCodex,
				SourceSessionID:     "codex:sess",
				TargetNodeID:        "target-node",
				TargetSessionID:     "codex:target",
				Status:              "delivered",
				DeliveryMessageType: "system_handoff",
				CreatedAt:           "2026-06-20T01:00:00Z",
			},
		},
	}

	s.Require().NoError(renderInjectionList(&s.stdout, resp, "table"))
	s.Contains(s.stdout.String(), "kci_1")

	s.SetupTest()
	s.Require().NoError(renderInjectionList(&s.stdout, resp, "jsonl"))
	s.Contains(s.stdout.String(), `"injectionId":"kci_1"`)
	s.Contains(s.stdout.String(), `"sourceNodeId":"source-node"`)
	s.Contains(s.stdout.String(), `"targetNodeId":"target-node"`)
}

func (s *CommandSuite) TestRenderInjectionListRejectsUnknownFormat() {
	err := renderInjectionList(&s.stdout, &facade.ListInjectionsResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderSetupSupportsFormats() {
	resp := &facade.SetupResponse{
		Adapters: []*facade.SetupAdapterResult{
			{
				Agent:   model.AgentNameClaude,
				Status:  facade.SetupStatusInstalled,
				Path:    "/tmp/settings.json",
				Message: "Installed Claude Code UserPromptSubmit hook.",
			},
		},
	}

	s.Require().NoError(renderSetup(&s.stdout, resp, "table"))
	s.Contains(s.stdout.String(), "claude")
	s.Contains(s.stdout.String(), "installed")

	s.SetupTest()
	s.Require().NoError(renderSetup(&s.stdout, resp, "jsonl"))
	s.Contains(s.stdout.String(), `"schemaVersion":"paxl.setup.adapter.v1"`)
	s.Contains(s.stdout.String(), `"agent":"claude"`)
}

func (s *CommandSuite) TestRenderSetupRejectsUnknownFormat() {
	err := renderSetup(&s.stdout, &facade.SetupResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderMirrorResultRejectsUnknownFormat() {
	err := renderMirrorResult(&s.stdout, &facade.MirrorSessionResponse{
		Capsule:   &model.KnowledgeCapsule{CapsuleID: "kcap_1"},
		Injection: &model.KnowledgeInjection{InjectionID: "mir_1"},
	}, "xml")

	s.Error(err)
}

func (s *CommandSuite) seedCodexSessionWithKeyword(keyword string) string {
	dbPath, _ := s.seedCodexSessionWithKeywordAndRollout(keyword)
	return dbPath
}

func (s *CommandSuite) seedCodexSessionWithKeywordAndRollout(keyword string) (string, string) {
	codexHome := s.T().TempDir()
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "06", "20")
	s.Require().NoError(os.MkdirAll(rolloutDir, 0o700))
	s.T().Setenv("CODEX_HOME", codexHome)
	rolloutPath := filepath.Join(rolloutDir, "rollout-test-sess-1.jsonl")
	s.Require().NoError(os.WriteFile(
		rolloutPath,
		[]byte(
			`{"type":"session_meta","payload":{"id":"sess-1","timestamp":"2026-06-20T01:00:00Z","cwd":"/tmp/project"}}`+"\n"+
				`{"timestamp":"2026-06-20T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Bridge context for `+keyword+`"}]}}`+"\n"+
				`{"timestamp":"2026-06-20T01:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Bridge answer"}]}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")
	s.Require().NoError(run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "codex"},
		&s.stdout,
		&s.stderr,
	))
	s.SetupTest()
	return dbPath, rolloutPath
}

func (s *CommandSuite) createLocalCapsule(dbPath string, keyword string) string {
	s.Require().NoError(run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "create", "codex:sess-1",
			"--keyword", keyword,
			"--local",
			"--format", "jsonl",
		},
		&s.stdout,
		&s.stderr,
	))
	var created map[string]any
	s.Require().NoError(json.Unmarshal(firstLine(s.stdout.Bytes()), &created))
	capsuleID, ok := created["capsuleId"].(string)
	s.Require().True(ok)
	s.SetupTest()
	return capsuleID
}

func (s *CommandSuite) withStdinJSON(payload any, fn func()) {
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	s.Require().NoError(json.NewEncoder(writer).Encode(payload))
	s.Require().NoError(writer.Close())
	original := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = original
		s.Require().NoError(reader.Close())
	}()
	fn()
}

func (s *CommandSuite) seedManagerCredential(dbPath string, managerURL string) {
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	_, err = opened.Store.SaveAuthCredential(context.Background(), &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:  managerURL,
			APIKey:      "paxu_test",
			NodeID:      "node_paxl",
			UserID:      "usr_1",
			Email:       "me@example.com",
			DisplayName: "Me",
			Role:        "user",
		},
	})
	s.Require().NoError(err)
}

func (s *CommandSuite) stubDefaultHTTPClient(
	handler func(*http.Request) (*http.Response, error),
) func() {
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: commandRoundTripFunc(handler)}
	return func() {
		http.DefaultClient = previous
	}
}

func (s *CommandSuite) seedCodexTargetSession(dbPath string) {
	s.Require().NoError(run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "codex"},
		&s.stdout,
		&s.stderr,
	))
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	_, err = opened.Store.UpsertSessions(context.Background(), &store.UpsertSessionsRequest{
		Agent: model.AgentNameCodex,
		Sessions: []*model.Session{
			{NativeID: "target", Title: "Target"},
		},
	})
	s.Require().NoError(err)
	s.SetupTest()
}

func (s *CommandSuite) seedStoredSessions(dbPath string, sessions []*model.Session) {
	opened, err := store.Open(context.Background(), &store.OpenRequest{Path: dbPath})
	s.Require().NoError(err)
	defer closeStore(opened.Store)
	_, err = opened.Store.UpsertSessions(context.Background(), &store.UpsertSessionsRequest{
		Agent:    model.AgentNameCodex,
		Sessions: sessions,
	})
	s.Require().NoError(err)
}

func (s *CommandSuite) installFakeCodex(capturePath string) {
	s.installFakeCommand("codex", capturePath)
}

func (s *CommandSuite) installFakeClaude(capturePath string) {
	s.installFakeCommand("claude", capturePath)
}

func (s *CommandSuite) installVerboseFakeCommand(name string, capturePath string) {
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

func (s *CommandSuite) installFakeCommand(name string, capturePath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte("#!/bin/sh\ncat > "+capturePath+"\n"),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CommandSuite) installArgCapturingFakeCommand(name string, argsPath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, name)
	s.Require().NoError(os.WriteFile(
		fakePath,
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsPath+"\n"),
		0o700,
	))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CommandSuite) readExecutionLogs(home string) string {
	logDir := filepath.Join(home, ".pax", "paxl", "logs")
	entries, err := os.ReadDir(logDir)
	s.Require().NoError(err)
	var out strings.Builder
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(logDir, entry.Name()))
		s.Require().NoError(err)
		out.Write(raw)
	}
	return out.String()
}

func (s *CommandSuite) installFakeCodexCapsuleGenerator(rolloutPath string) {
	binDir := s.T().TempDir()
	fakePath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"prompt=$(cat)\n" +
		"capsule_id=$(printf '%s\\n' \"$prompt\" | sed -n 's/^Capsule id: //p' | head -n 1)\n" +
		"printf '%s\\n' \"{\\\"timestamp\\\":\\\"2026-06-20T01:03:00Z\\\",\\\"type\\\":\\\"response_item\\\",\\\"payload\\\":{\\\"type\\\":\\\"message\\\",\\\"role\\\":\\\"assistant\\\",\\\"content\\\":[{\\\"type\\\":\\\"output_text\\\",\\\"text\\\":\\\"PAX_KNOWLEDGE_CAPSULE_START ${capsule_id}\\\\n{\\\\\\\"title\\\\\\\":\\\\\\\"Generated bridge\\\\\\\",\\\\\\\"summary\\\\\\\":\\\\\\\"Generated summary\\\\\\\",\\\\\\\"content\\\\\\\":\\\\\\\"Generated bridge content\\\\\\\"}\\\\nPAX_KNOWLEDGE_CAPSULE_END ${capsule_id}\\\"}]}}\" >> \"" + rolloutPath + "\"\n"
	s.Require().NoError(os.WriteFile(fakePath, []byte(script), 0o700))
	s.T().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CommandSuite) TestRenderAgentListRejectsUnknownFormat() {
	err := renderAgentList(&s.stdout, &facade.ListAgentsResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderSessionListSupportsTableFormat() {
	err := renderSessionList(&s.stdout, &facade.ListSessionsResponse{
		Sessions: []*model.Session{
			{
				ID:        "codex:sess",
				Agent:     model.AgentNameCodex,
				NativeID:  "sess",
				Title:     "Session",
				UpdatedAt: "2026-06-20T01:00:00Z",
			},
		},
	}, "table")

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "codex:sess")
}

func (s *CommandSuite) TestRenderSessionListRejectsUnknownFormat() {
	err := renderSessionList(&s.stdout, &facade.ListSessionsResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestSessionListRejectsUnknownAgentBeforeFacadeCall() {
	err := run(
		context.Background(),
		[]string{"session", "list", "--agent", "qwen"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestSessionGetRejectsMissingSessionID() {
	err := run(context.Background(), []string{"session", "get"}, &s.stdout, &s.stderr)

	s.Error(err)
}

func (s *CommandSuite) TestSessionGetRejectsUnknownFormat() {
	err := run(
		context.Background(),
		[]string{"session", "get", "codex:sess", "--format", "xml"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestSessionGetRejectsUnknownAgent() {
	err := run(
		context.Background(),
		[]string{"session", "get", "sess", "--agent", "qwen"},
		&s.stdout,
		&s.stderr,
	)

	s.Error(err)
}

func (s *CommandSuite) TestRenderSessionTimelineRejectsUnknownFormat() {
	err := renderSessionTimeline(&s.stdout, &facade.GetSessionResponse{}, "xml")

	s.Error(err)
}

func (s *CommandSuite) TestRenderSessionTimelineSupportsEmptyTranscript() {
	err := renderSessionTimeline(&s.stdout, &facade.GetSessionResponse{}, "transcript")

	s.NoError(err)
	s.Empty(s.stdout.String())
}

func firstLine(raw []byte) []byte {
	for i, value := range raw {
		if value == '\n' {
			return raw[:i]
		}
	}
	return raw
}

func decodeJSONLLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode jsonl line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

type commandRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f commandRoundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (f commandRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func commandJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func testSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func friendCommandResponse(status string) string {
	return friendCommandResponseWithAliases(status, "alice", "bob")
}

func friendCommandResponseWithAliases(
	status string,
	requesterAlias string,
	recipientAlias string,
) string {
	return fmt.Sprintf(`{
		"data":{
			"friend":{
				"friend_id":"fr_1",
				"requester_user_id":"usr_sender",
				"requester_email":"alice@example.com",
				"requester_alias":%q,
				"recipient_user_id":"usr_1",
				"recipient_email":"me@example.com",
				"recipient_alias":%q,
				"status":%q,
				"created_at":"2026-06-22T00:00:00Z"
			}
		},
		"code":200,
		"message":"ok"
	}`, requesterAlias, recipientAlias, status)
}

func hasCommand(command *cli.Command, name string) bool {
	return findCommand(command, name) != nil
}

func findCommand(command *cli.Command, name string) *cli.Command {
	for _, subcommand := range command.Commands {
		if subcommand.Name == name {
			return subcommand
		}
	}
	return nil
}

func TestRenderTeamListTable(t *testing.T) {
	var buf bytes.Buffer
	resp := &facade.ListTeamsResponse{
		UserID: "usr_1",
		Teams: []*model.TeamSummary{
			{Team: model.Team{TeamID: "team_1", Name: "Core", Status: "active"},
				MyRole: "owner", MemberCount: 2, AgentCount: 3},
		},
	}
	if err := renderTeamList(&buf, resp, "table"); err != nil {
		t.Fatalf("render team list: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "team_1") || !strings.Contains(out, "Core") ||
		!strings.Contains(out, "owner") {
		t.Errorf("unexpected table output:\n%s", out)
	}
}

func TestRenderTeamAgentsAggregatedShowsTeamsColumn(t *testing.T) {
	var buf bytes.Buffer
	resp := &facade.ListAllTeamAgentsResponse{
		UserID: "usr_1",
		Agents: []*facade.AggregatedTeamAgent{
			{
				Agent: &model.TeamAgent{
					AgentID:         "agent_mate",
					AgentOwnerEmail: "mate@example.com",
					Agent: &model.NodeAgent{
						AgentID:   "agent_mate",
						Name:      "mate-claude",
						Hostname:  "mate-host.local",
						AgentType: "claude",
					},
				},
				Teams: []facade.TeamRef{
					{TeamID: "team_a", Name: "Alpha"},
					{TeamID: "team_b", Name: "Beta"},
				},
			},
		},
	}
	if err := renderAggregatedTeamAgents(&buf, resp, "table"); err != nil {
		t.Fatalf("render aggregated team agents: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "mate-claude") || !strings.Contains(out, "Alpha") ||
		!strings.Contains(out, "Beta") || !strings.Contains(out, "mate@example.com") ||
		!strings.Contains(out, "mate-host.local") || !strings.Contains(out, "claude") {
		t.Errorf("unexpected aggregated output:\n%s", out)
	}
}

func TestTeamAgentDisplayFallsBackToAgentID(t *testing.T) {
	withName := &model.TeamAgent{AgentID: "a1", Agent: &model.NodeAgent{Name: "named"}}
	if got := teamAgentDisplay(withName); got != "named" {
		t.Errorf("display = %q, want named", got)
	}
	noName := &model.TeamAgent{AgentID: "a2"}
	if got := teamAgentDisplay(noName); got != "a2" {
		t.Errorf("display = %q, want a2", got)
	}
}

func TestRenderTeamListJSONLAndRejectsUnknownFormat(t *testing.T) {
	resp := &facade.ListTeamsResponse{
		UserID: "usr_1",
		Teams: []*model.TeamSummary{
			{
				Team: model.Team{
					TeamID:     "team_1",
					Name:       "Core",
					Status:     "archived",
					ArchivedAt: "2026-06-27T00:00:00Z",
				},
				MyRole: "owner",
			},
		},
	}
	var buf bytes.Buffer
	if err := renderTeamList(&buf, resp, "jsonl"); err != nil {
		t.Fatalf("render jsonl: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"schemaVersion":"paxl.team.v1"`) {
		t.Errorf("missing schemaVersion: %s", out)
	}
	if !strings.Contains(out, `"archivedAt":"2026-06-27T00:00:00Z"`) {
		t.Errorf("missing archivedAt: %s", out)
	}
	if err := renderTeamList(&buf, resp, "yaml"); err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestEncodeTeamAgentJSONLIncludesProvenanceAndTeams(t *testing.T) {
	resp := &facade.ListAllTeamAgentsResponse{
		UserID: "usr_1",
		Agents: []*facade.AggregatedTeamAgent{{
			Agent: &model.TeamAgent{
				AgentID:          "agent_mate",
				AgentOwnerUserID: "usr_mate",
				AgentOwnerEmail:  "mate@example.com",
				AddedByUserID:    "usr_1",
				Agent: &model.NodeAgent{
					Name:      "mate-claude",
					Hostname:  "mate-host.local",
					AgentType: "claude",
				},
			},
			Teams: []facade.TeamRef{{TeamID: "team_a", Name: "Alpha"}},
		}},
	}
	var buf bytes.Buffer
	if err := renderAggregatedTeamAgents(&buf, resp, "jsonl"); err != nil {
		t.Fatalf("render jsonl: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`"schemaVersion":"paxl.teamAgent.v1"`,
		`"addedByUserId":"usr_1"`,
		`"agentOwnerEmail":"mate@example.com"`,
		`"hostname":"mate-host.local"`,
		`"agentType":"claude"`,
		`"removedAt":""`,
		`"teams"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in jsonl: %s", want, out)
		}
	}
}

func newTeamAgentsTestCommand(t *testing.T, args []string) *cli.Command {
	t.Helper()
	cmd := &cli.Command{
		Name: "agents",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all"},
			&cli.BoolFlag{Name: "include-self"},
			&cli.BoolFlag{Name: "online"},
			&cli.StringFlag{Name: "agent"},
			&cli.StringFlag{Name: "format", Value: "table"},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	if err := cmd.Run(context.Background(), append([]string{"agents"}, args...)); err != nil {
		t.Fatalf("run test command: %v", err)
	}
	return cmd
}

func TestParseListAllTeamAgentsRejectsBothTeamIDAndAll(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"team_1", "--all"})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when both team id and --all are set")
	}
}

func TestParseListAllTeamAgentsRejectsNeither(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when neither team id nor --all is set")
	}
}

func TestParseTeamAgentsAllRequest(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"--all", "--include-self", "--agent", "agent_x"})
	single, all, err := parseTeamAgentsRequest(cmd)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if single != nil {
		t.Fatalf("expected no single-team request, got %+v", single)
	}
	if all == nil || !all.IncludeSelf || all.AgentID != "agent_x" {
		t.Fatalf("unexpected aggregate request: %+v", all)
	}
}

func TestParseTeamAgentsSingleTeamRequest(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"team_1"})
	single, all, err := parseTeamAgentsRequest(cmd)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if all != nil {
		t.Fatalf("expected no aggregate request, got %+v", all)
	}
	if single == nil || single.TeamID != "team_1" {
		t.Fatalf("unexpected single request: %+v", single)
	}
}

func TestParseTeamAgentsRejectsAgentWithoutAll(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"team_1", "--agent", "agent_x"})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when --agent is used without --all")
	}
}

func TestParseTeamAgentsRejectsIncludeSelfWithoutAll(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"team_1", "--include-self"})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when --include-self is used without --all")
	}
}

func TestParseTeamAgentsRejectsOnlineWithoutAll(t *testing.T) {
	cmd := newTeamAgentsTestCommand(t, []string{"team_1", "--online"})
	if _, _, err := parseTeamAgentsRequest(cmd); err == nil {
		t.Fatal("expected error when --online is used without --all")
	}
}

func TestRenderTeamAgentsTableAndJSONL(t *testing.T) {
	resp := &facade.ListTeamAgentsResponse{
		TeamID: "team_1",
		UserID: "usr_1",
		Agents: []*model.TeamAgent{
			{
				TeamID:           "team_1",
				AgentID:          "agent_9",
				AgentOwnerUserID: "usr_mate",
				AgentOwnerEmail:  "mate@example.com",
				AddedAt:          "2026-06-27T00:00:00Z",
				Agent: &model.NodeAgent{
					AgentID:   "agent_9",
					Name:      "codex-laptop",
					Hostname:  "mate-host.local",
					AgentType: "codex",
					Online:    true,
				},
			},
		},
	}
	var table bytes.Buffer
	if err := renderTeamAgents(&table, resp, "table"); err != nil {
		t.Fatalf("render table: %v", err)
	}
	if !strings.Contains(table.String(), "codex-laptop") ||
		!strings.Contains(table.String(), "agent_9") ||
		!strings.Contains(table.String(), "mate@example.com") ||
		!strings.Contains(table.String(), "codex") ||
		!strings.Contains(table.String(), "mate-host.local") ||
		!strings.Contains(table.String(), "yes") {
		t.Errorf("unexpected table:\n%s", table.String())
	}

	var jsonl bytes.Buffer
	if err := renderTeamAgents(&jsonl, resp, "jsonl"); err != nil {
		t.Fatalf("render jsonl: %v", err)
	}
	out := jsonl.String()
	if !strings.Contains(out, `"schemaVersion":"paxl.teamAgent.v1"`) ||
		!strings.Contains(out, `"teamId":"team_1"`) ||
		!strings.Contains(out, `"agentOwnerEmail":"mate@example.com"`) ||
		!strings.Contains(out, `"hostname":"mate-host.local"`) ||
		!strings.Contains(out, `"agentType":"codex"`) ||
		!strings.Contains(out, `"online":true`) {
		t.Errorf("unexpected jsonl:\n%s", out)
	}
	if err := renderTeamAgents(&jsonl, resp, "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}
