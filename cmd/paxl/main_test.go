package main

import (
	"bytes"
	"context"
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

func TestCommandSuite(t *testing.T) {
	suite.Run(t, new(CommandSuite))
}

func (s *CommandSuite) SetupTest() {
	s.stdout.Reset()
	s.stderr.Reset()
	s.T().Setenv("HOME", s.T().TempDir())
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
	s.Contains(s.stdout.String(), "gemini")
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
	cases := []string{"list", "get", "mirror"}
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

func (s *CommandSuite) TestSessionListSyncsGeminiLocalSessionsToSQLite() {
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
		filepath.Join(sessionDir, "session-2026-06-20T05-31-gemini-session.jsonl"),
		[]byte(
			`{"sessionId":"gemini-session","projectHash":"sample-project","startTime":"2026-06-20T05:31:20.160Z","lastUpdated":"2026-06-20T05:32:20.160Z","kind":"main"}`+"\n"+
				`{"$set":{"messages":[{"id":"u1","timestamp":"2026-06-20T05:31:30.160Z","type":"user","content":[{"text":"Gemini session title"}]}],"lastUpdated":"2026-06-20T05:32:20.160Z"}}`+"\n",
		),
		0o600,
	))
	dbPath := filepath.Join(s.T().TempDir(), "paxl.sqlite")

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "session", "list", "--agent", "gemini", "--format", "jsonl"},
		&s.stdout,
		&s.stderr,
	)

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), `"id":"gemini:gemini-session"`)
	s.Contains(s.stdout.String(), `"title":"Gemini session title"`)
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

func (s *CommandSuite) TestSingularCapsuleCommandExposesMigratedSubcommands() {
	cases := []string{"create", "list", "get", "archive", "inject", "injection"}
	command := findCommand(newCommand(&s.stdout, &s.stderr), "capsule")
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

func (s *CommandSuite) TestCapsuleCreateSupportsContentFile() {
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	contentPath := filepath.Join(s.T().TempDir(), "capsule.md")
	s.Require().NoError(os.WriteFile(
		contentPath,
		[]byte("The paxl installer should be uploaded and hosted at GCS."),
		0o600,
	))

	err := run(
		context.Background(),
		[]string{
			"--db", dbPath,
			"capsule", "create", "codex:sess-1",
			"--keyword", "installer hosting",
			"--title", "paxl installer hosting",
			"--summary", "Installer upload and hosting requirement.",
			"--content-file", contentPath,
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

func (s *CommandSuite) TestCapsuleInjectDeliversHandoffAndListsInjection() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	s.T().Setenv("PAXL_NODE_ID", "local-node")
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)

	err := run(
		context.Background(),
		[]string{"--db", dbPath, "capsule", "inject", capsuleID, "codex:target"},
		&s.stdout,
		&s.stderr,
	)
	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "Injected")
	rawPrompt, err := os.ReadFile(capturePath)
	s.Require().NoError(err)
	s.Contains(string(rawPrompt), "system_handoff")
	s.Contains(string(rawPrompt), "Bridge context")

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

func (s *CommandSuite) TestCapsuleInjectWritesDeliveredHandoffToOutputPath() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	outputPath := filepath.Join(s.T().TempDir(), "handoff.txt")
	s.installFakeCodex(capturePath)

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

	s.Require().NoError(err)
	s.Contains(s.stdout.String(), "and wrote "+outputPath)
	rawOutput, err := os.ReadFile(outputPath)
	s.Require().NoError(err)
	s.Contains(string(rawOutput), "system_handoff")
	s.Contains(string(rawOutput), "Bridge context")
}

func (s *CommandSuite) TestCapsuleInjectionListSupportsTableFormat() {
	if runtime.GOOS == "windows" {
		s.T().Skip("The fake CLI script uses POSIX shell syntax.")
	}
	dbPath := s.seedCodexSessionWithKeyword("bridge")
	capsuleID := s.createLocalCapsule(dbPath, "bridge")
	s.seedCodexTargetSession(dbPath)
	capturePath := filepath.Join(s.T().TempDir(), "prompt.txt")
	s.installFakeCodex(capturePath)
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

type commandRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f commandRoundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func commandJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
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
