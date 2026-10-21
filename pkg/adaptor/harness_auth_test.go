//go:build !windows

package adaptor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/require"
)

func TestNativeAuthPassesAPIKeyOnlyThroughStdin(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "codex")
	require.NoError(
		t,
		os.WriteFile(
			command,
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'login --with-api-key' ] || exit 12\nread -r key\n[ \"$key\" = 'private-key' ] || exit 13\necho private-key\n",
			),
			0700,
		),
	)
	t.Setenv("CODEX_PATH", command)
	var output bytes.Buffer
	auth := NewNativeHarnessAuth()
	err := auth.Run(
		context.Background(),
		&NativeAuthRequest{
			Harness:   "codex",
			Operation: "login",
			Method:    "api-key",
			Stdin:     strings.NewReader("private-key\n"),
			Stdout:    &output,
			Stderr:    &output,
		},
	)
	require.NoError(t, err)
	require.Empty(
		t,
		output.String(),
		"Secret-entry commands must not echo keys into protocol output",
	)
}

func TestNativeAuthUsesOfficialLoginCommands(t *testing.T) {
	for _, tc := range []struct{ harness, method, args string }{
		{"claude", "", "auth login"}, {"claude", "subscription", "auth login"}, {"claude", "console", "auth login --console"},
		{"codex", "", "login --device-auth"}, {"codex", "device", "login --device-auth"}, {"codex", "browser", "login"}, {"codex", "access-token", "login --with-access-token"},
		{"kimi", "", "login"}, {"kimi", "device", "login"},
	} {
		t.Run(tc.harness+"/"+tc.method, func(t *testing.T) {
			dir := t.TempDir()
			command := filepath.Join(dir, tc.harness)
			script := "#!/bin/sh\n[ \"$*\" = '" + tc.args + "' ] || exit 12\n[ \"$AUTH_TEST_CONTEXT\" = 'isolated' ] || exit 13\nexit 0\n"
			require.NoError(t, os.WriteFile(command, []byte(script), 0700))
			t.Setenv("CODEX_PATH", command)
			t.Setenv("CLAUDE_CODE_EXECUTABLE", command)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("AUTH_TEST_CONTEXT", "isolated")
			var output bytes.Buffer
			err := NewNativeHarnessAuth().Run(t.Context(), &NativeAuthRequest{Harness: model.AgentName(tc.harness), Operation: model.HarnessAuthLogin, Method: model.HarnessAuthMethod(tc.method), Stdin: strings.NewReader("token\n"), Stdout: &output, Stderr: &output})
			require.NoError(t, err)
		})
	}
}

func TestNativeAuthRejectsUnsupportedOperationsBeforeExecution(t *testing.T) {
	for _, req := range []*NativeAuthRequest{
		nil,
		{Harness: "unsupported", Operation: "login"},
		{Harness: "claude", Operation: "login", Method: "api-key"},
		{Harness: "codex", Operation: "login", Method: "console"},
		{Harness: "codex", Operation: "status", Method: "device"},
		{Harness: "codex", Operation: "invalid"},
	} {
		require.Error(t, NewNativeHarnessAuth().Run(t.Context(), req))
	}
}

func TestNativeAuthStatusRejectsSuccessfulLookingFailure(t *testing.T) {
	command := filepath.Join(t.TempDir(), "codex")
	require.NoError(
		t,
		os.WriteFile(command, []byte("#!/bin/sh\necho 'Logged in using ChatGPT'\nexit 1\n"), 0700),
	)
	t.Setenv("CODEX_PATH", command)
	_, err := NewNativeHarnessAuth().CaptureStatus(t.Context(), &NativeAuthRequest{Harness: "codex", Operation: "status"})
	require.Error(t, err)
}
