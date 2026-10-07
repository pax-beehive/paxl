package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/stretchr/testify/require"
)

type authHTTPFunc func(*http.Request) (*http.Response, error)

func (f authHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthStatusUsesHarnessFlagAndHidesInternalLoginHandle(t *testing.T) {
	old := newHarnessAuthFacade
	t.Cleanup(func() { newHarnessAuthFacade = old })
	newHarnessAuthFacade = func(socket string) *facade.DaemonHarnessAuthFacade {
		require.Equal(t, "/tmp/target.sock", socket)
		client := authHTTPFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/v1/harnesses/claude/auth/status", r.URL.Path)
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(
					strings.NewReader(
						`{"harness_auth":{"harness":"claude","state":"awaiting_code","session_id":"private-handle","authorization_url":"https://claude.com/cai/oauth/authorize?state=test"}}`,
					),
				),
			}, nil
		})
		return facade.NewDaemonHarnessAuthFacade(
			facade.NewDaemonHTTPClient("http://paxd.test", client),
		)
	}
	var stdout, stderr bytes.Buffer
	err := runWithInput(
		context.Background(),
		[]string{
			"auth",
			"status",
			"--harness",
			"claude-code",
			"--socket",
			"/tmp/target.sock",
			"--format",
			"json",
		},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	require.Contains(t, stdout.String(), `"state":"awaiting_code"`)
	require.NotContains(t, stdout.String(), "private-handle")
	require.NotContains(t, stdout.String(), "session_id")
}

func TestAuthLoginSubmitsCodeFromStdinWithoutSessionArgument(t *testing.T) {
	old := newHarnessAuthFacade
	t.Cleanup(func() { newHarnessAuthFacade = old })
	posts, queries := 0, 0
	newHarnessAuthFacade = func(_ string) *facade.DaemonHarnessAuthFacade {
		client := authHTTPFunc(func(r *http.Request) (*http.Response, error) {
			body := `{"harness_auth":{"harness":"claude","state":"succeeded","session_id":"internal-only","logged_in":true}}`
			if r.Method == http.MethodPost {
				posts++
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.JSONEq(
					t,
					`{"harness":"claude","operation":"submit","session_id":"internal-only","code":"secret-code#state"}`,
					string(raw),
				)
				require.NotEmpty(t, r.Header.Get("X-Pax-Command-ID"))
				body = `{"ok":true,"result":{"harness_auth":{"harness":"claude","state":"exchanging","session_id":"internal-only"}}}`
			} else {
				queries++
				if posts == 0 {
					body = `{"harness_auth":{"harness":"claude","state":"awaiting_code","session_id":"internal-only"}}`
				} else {
					require.Equal(t, "internal-only", r.URL.Query().Get("session_id"))
				}
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		return facade.NewDaemonHarnessAuthFacade(
			facade.NewDaemonHTTPClient("http://paxd.test", client),
		)
	}
	var stdout, stderr bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{"auth", "login", "--harness", "claude", "--code-stdin", "--verbose"},
		strings.NewReader("secret-code#state\r\n"),
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	require.Contains(t, stdout.String(), "succeeded")
	require.NotContains(t, stdout.String()+stderr.String(), "secret-code")
	require.NotContains(t, stdout.String()+stderr.String(), "internal-only")
	require.Equal(t, 1, posts)
	require.Equal(t, 2, queries)
}

func TestAuthInvalidInputNeverContactsDaemon(t *testing.T) {
	old := newHarnessAuthFacade
	t.Cleanup(func() { newHarnessAuthFacade = old })
	newHarnessAuthFacade = func(_ string) *facade.DaemonHarnessAuthFacade { t.Fatal("invalid input contacted daemon"); return nil }
	for _, tc := range []struct {
		name  string
		args  []string
		input string
	}{
		{name: "missing harness", args: []string{"auth", "status"}},
		{name: "positional harness", args: []string{"auth", "status", "--harness", "claude", "claude"}},
		{name: "unsupported harness", args: []string{"auth", "login", "--harness", "unsupported"}},
		{name: "invalid format", args: []string{"auth", "login", "--harness", "claude", "--format", "xml"}},
		{name: "empty code", args: []string{"auth", "login", "--harness", "claude", "--code-stdin"}, input: "\n"},
		{name: "oversized code", args: []string{"auth", "login", "--harness", "claude", "--code-stdin"}, input: strings.Repeat("x", 4097) + "\n"},
		{name: "device code is entered in browser", args: []string{"auth", "login", "--harness", "codex", "--code-stdin"}, input: "ABCD-EFGHJ\n"},
		{name: "key requires stdin", args: []string{"auth", "login", "--harness", "codex", "--method", "api-key"}},
		{name: "secret needs a secret method", args: []string{"auth", "login", "--harness", "codex", "--secret-stdin"}, input: "secret\n"},
		{name: "mixed secret flags", args: []string{"auth", "login", "--harness", "claude", "--secret-stdin", "--code-stdin"}, input: "secret\n"},
		{name: "unknown method", args: []string{"auth", "login", "--harness", "codex", "--method", "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runWithInput(t.Context(), tc.args, strings.NewReader(tc.input), &stdout, &stderr)
			require.Error(t, err)
		})
	}
}
