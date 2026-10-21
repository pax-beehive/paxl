//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawAPIKeyUsesNativeStdinWithoutLeakingSecret(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "openclaw")
	script := `#!/bin/sh
[ "$*" = 'models auth paste-api-key --provider openai' ] || exit 23
read -r key
[ "$key" = 'fixture-private-key' ] || exit 24
echo fixture-private-key >&2
`
	require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
	t.Setenv("OPENCLAW_EXECUTABLE", binary)
	var out, diagnostics bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{
			"auth",
			"login",
			"--harness",
			"openclaw",
			"--provider",
			"openai",
			"--method",
			"api-key",
			"--secret-stdin",
		},
		strings.NewReader("fixture-private-key\n"),
		&out,
		&diagnostics,
	)
	require.NoError(t, err)
	require.Contains(t, out.String(), "State: succeeded")
	require.Contains(t, out.String(), "Gateway")
	require.NotContains(t, out.String()+diagnostics.String(), "fixture-private-key")
}

func TestOpenClawNativeLoginAndProfileLogoutKeepUserInteraction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "openclaw")
	script := `#!/bin/sh
case "$*" in
 'models auth login --provider openai-codex --method device-code') echo 'Authorize native device code' >&2 ;;
 'models auth logout openai:manual')
  echo 'Confirm profile removal' >&2
  read -r answer
  [ "$answer" = 'yes' ] || exit 24
  ;;
 *) exit 23 ;;
esac
`
	require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
	t.Setenv("OPENCLAW_EXECUTABLE", binary)
	for _, args := range [][]string{
		{"auth", "login", "--harness", "openclaw", "--provider", "openai-codex", "--method", "device-code"},
		{"auth", "logout", "--harness", "openclaw", "--profile", "openai:manual"},
	} {
		var out, diagnostics bytes.Buffer
		require.NoError(
			t,
			runWithInput(t.Context(), args, strings.NewReader("yes\n"), &out, &diagnostics),
		)
		require.Contains(t, out.String(), "State: succeeded")
		require.NotEmpty(t, diagnostics.String())
	}
}

func TestOpenClawAuthStatusOnlyReturnsProfileMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "openclaw")
	script := `#!/bin/sh
[ "$*" = 'models auth list --json --provider openai' ] || exit 23
cat <<'OUTPUT'
{"profiles":[{"id":"openai:manual","provider":"openai","type":"api_key","key":"private-key","email":"private@example.test"}],"agentDir":"/private/path"}
OUTPUT
`
	require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
	t.Setenv("OPENCLAW_EXECUTABLE", binary)
	var out, diagnostics bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{
			"auth",
			"status",
			"--harness",
			"openclaw",
			"--provider",
			"openai",
			"--format",
			"json",
		},
		strings.NewReader(""),
		&out,
		&diagnostics,
	)
	require.NoError(t, err)
	require.Contains(t, out.String(), `"profile_id":"openai:manual"`)
	require.Contains(t, out.String(), `"state":"configured"`)
	require.NotContains(t, out.String()+diagnostics.String(), "private")
}

func TestOpenClawAuthRejectsAmbiguousLogoutBeforeExecution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "openclaw")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho MUST-NOT-RUN\n"), 0700))
	t.Setenv("OPENCLAW_EXECUTABLE", binary)
	for _, extra := range [][]string{
		{"logout"},
		{"logout", "--provider", "openai", "--profile", "openai:manual"},
		{"login", "--profile", "openai:manual"},
		{"login", "--provider", "openai", "--method", "api-key"},
		{"login", "--provider", "openai", "--method", "oauth", "--secret-stdin"},
	} {
		args := append([]string{"auth"}, extra...)
		args = append(args, "--harness", "openclaw")
		var out, diagnostics bytes.Buffer
		require.Error(
			t,
			runWithInput(t.Context(), args, strings.NewReader("key\n"), &out, &diagnostics),
		)
		require.Empty(t, out.String()+diagnostics.String())
	}
}
