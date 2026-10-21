//go:build !windows

package facade

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestLocalAuthReportsSignedOutWithoutDaemonOrRawOutput(t *testing.T) {
	dir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, "codex"),
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'login status' ] || exit 10\necho 'Not logged in' >&2\nexit 1\n",
			),
			0700,
		),
	)
	t.Setenv("CODEX_PATH", filepath.Join(dir, "codex"))
	var output bytes.Buffer
	result, err := NewHarnessAuthFacade().Execute(t.Context(), &HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthStatus}, WithStreams(strings.NewReader(""), &output, &output))
	require.NoError(t, err)
	require.Equal(t, model.HarnessAuthLoggedOut, result.State)
	require.NotNil(t, result.LoggedIn)
	require.False(t, *result.LoggedIn)
	require.Empty(t, output.String())
}

func TestLocalAuthValidatesSecretInputBeforeRunningNativeCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "called")
	command := filepath.Join(t.TempDir(), "codex")
	require.NoError(
		t,
		os.WriteFile(command, []byte("#!/bin/sh\necho called > '"+marker+"'\n"), 0700),
	)
	t.Setenv("CODEX_PATH", command)
	for _, tc := range []struct {
		method model.HarnessAuthMethod
		secret bool
		input  string
	}{
		{model.HarnessAuthAPIKey, false, "key\n"},
		{model.HarnessAuthAccessToken, true, "\n"},
		{model.HarnessAuthAPIKey, true, strings.Repeat("x", 4097) + "\n"},
		{model.HarnessAuthAPIKey, true, " key\n"},
		{model.HarnessAuthDevice, true, "key\n"},
	} {
		_, err := NewHarnessAuthFacade().Execute(t.Context(), &HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthLogin, Method: tc.method, SecretStdin: tc.secret}, WithStreams(strings.NewReader(tc.input), nil, nil))
		require.Error(t, err)
	}
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}

func TestLocalAuthStatusExposesOnlyKnownMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, harness, output string
		exit                  int
		loggedIn              bool
		method                string
		fails                 bool
	}{
		{name: "Claude subscription", harness: "claude", output: `{"loggedIn":true,"authMethod":"claude.ai","email":"private@example.test","apiKey":"do-not-return"}`, loggedIn: true, method: "claude.ai"},
		{name: "Claude signed out", harness: "claude", output: `{"loggedIn":false}`, exit: 1},
		{name: "Claude invalid JSON", harness: "claude", output: `invalid`, fails: true},
		{name: "Claude unknown", harness: "claude", output: `{}`, fails: true},
		{name: "Codex API key", harness: "codex", output: "Logged in using an API key - private-fragment", loggedIn: true, method: "api_key"},
		{name: "Codex ChatGPT", harness: "codex", output: "Logged in using ChatGPT", loggedIn: true, method: "chatgpt"},
		{name: "Codex access token", harness: "codex", output: "Logged in using personal access token", loggedIn: true, method: "access_token"},
		{name: "Codex network failure", harness: "codex", output: "network error: private-fragment", exit: 1, fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := filepath.Join(t.TempDir(), tc.harness)
			script := fmt.Sprintf(
				"#!/bin/sh\ncat <<'DATA'\n%s\nDATA\nexit %d\n",
				tc.output,
				tc.exit,
			)
			require.NoError(t, os.WriteFile(command, []byte(script), 0700))
			t.Setenv("CODEX_PATH", command)
			t.Setenv("CLAUDE_CODE_EXECUTABLE", command)
			var output bytes.Buffer
			result, err := NewHarnessAuthFacade().Execute(t.Context(), &HarnessAuthRequest{Harness: model.AgentName(tc.harness), Operation: model.HarnessAuthStatus}, WithStreams(nil, &output, &output))
			if tc.fails {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-fragment")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.loggedIn, *result.LoggedIn)
			require.Equal(t, tc.method, result.AuthMethod)
			require.Empty(t, output.String())
		})
	}
}

func TestLocalAuthLogoutDoesNotClaimEnvironmentCredentialsWereRemoved(t *testing.T) {
	command := filepath.Join(t.TempDir(), "codex")
	require.NoError(
		t,
		os.WriteFile(command, []byte("#!/bin/sh\n[ \"$*\" = 'logout' ] || exit 12\n"), 0700),
	)
	t.Setenv("CODEX_PATH", command)
	t.Setenv("OPENAI_API_KEY", "existing-key")
	result, err := NewHarnessAuthFacade().Execute(t.Context(), &HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthLogout})
	require.NoError(t, err)
	require.Nil(t, result.LoggedIn)
	require.Equal(t, "existing-key", os.Getenv("OPENAI_API_KEY"))
}

func TestLocalAuthCanCancelWhileWaitingForSecretInput(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = input.Close() }()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := NewHarnessAuthFacade().Execute(ctx, &HarnessAuthRequest{Harness: model.AgentNameCodex, Operation: model.HarnessAuthLogin, Method: model.HarnessAuthAPIKey, SecretStdin: true}, WithStreams(input, nil, nil))
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Cancel blocked on secret stdin")
	}
}

func TestHermesProviderSelectionRejectsNoninteractiveInput(t *testing.T) {
	_, err := NewHarnessAuthFacade().Execute(t.Context(), &HarnessAuthRequest{
		Harness: model.AgentNameHermes, Operation: model.HarnessAuthLogin,
	}, WithStreams(strings.NewReader("1\n"), nil, nil))
	require.ErrorContains(t, err, "use --provider for noninteractive login")
}
