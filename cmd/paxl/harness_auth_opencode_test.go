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

func TestOpenCodeAuthRunsNativeProviderSelectionLocally(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "opencode")
	require.NoError(
		t,
		os.WriteFile(
			binary,
			[]byte(
				"#!/bin/sh\n[ \"$*\" = 'auth login --provider openai' ] || exit 23\necho 'Complete native browser authorization' >&2\n",
			),
			0700,
		),
	)
	t.Setenv("OPENCODE_EXECUTABLE", binary)
	var out, diagnostics bytes.Buffer
	err := runWithInput(
		t.Context(),
		[]string{
			"harness",
			"auth",
			"login",
			"--harness",
			"opencode",
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
	require.JSONEq(
		t,
		`{"harness":"opencode","state":"succeeded","provider":"openai"}`,
		out.String(),
	)
	require.Contains(t, diagnostics.String(), "Complete native browser authorization")
}

func TestOpenCodeAuthKeepsNativeKeyPromptAndProviderLogout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "opencode")
	script := `#!/bin/sh
case "$*" in
 'auth login --provider anthropic')
  echo 'Native masked API key prompt' >&2
  read -r key
  [ "$key" = 'fixture-only' ] || exit 24
  ;;
 'auth logout anthropic') ;;
 *) exit 23 ;;
esac
`
	require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
	t.Setenv("OPENCODE_EXECUTABLE", binary)
	for _, op := range []string{"login", "logout"} {
		var out, diagnostics bytes.Buffer
		err := runWithInput(
			t.Context(),
			[]string{"auth", op, "--harness", "opencode", "--provider", "anthropic"},
			strings.NewReader("fixture-only\n"),
			&out,
			&diagnostics,
		)
		require.NoError(t, err)
		require.Contains(t, out.String(), "State: succeeded")
		require.NotContains(t, out.String()+diagnostics.String(), "fixture-only")
		if op == "login" {
			require.Contains(t, diagnostics.String(), "Native masked API key prompt")
		}
	}
}

func TestOpenCodeAuthStatusReturnsMetadataWithoutCredentialOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name, text, state string
		fails             bool
	}{
		{"configured", "┌ Credentials ~/.local/share/opencode/auth.json\n│ private-provider api\n└ 2 credentials", "configured", false},
		{"no stored credentials", "┌ Credentials ~/.local/share/opencode/auth.json\n└ 0 credentials", "unknown", false},
		{"unknown format", "private diagnostic", "", true},
		{"duplicate summaries", "Credentials file\n└ 1 credential\n└ 2 credentials", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "opencode")
			script := "#!/bin/sh\n[ \"$*\" = 'auth list' ] || exit 23\ncat <<'OUTPUT'\n" + tc.text + "\nOUTPUT\n"
			require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
			t.Setenv("OPENCODE_EXECUTABLE", binary)
			var out, diagnostics bytes.Buffer
			err := runWithInput(
				t.Context(),
				[]string{"auth", "status", "--harness", "opencode", "--format", "json"},
				strings.NewReader(""),
				&out,
				&diagnostics,
			)
			if tc.fails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Contains(t, out.String(), `"state":"`+tc.state+`"`)
				require.NotContains(t, out.String(), `"logged_in":true`)
			}
			require.NotContains(t, out.String()+diagnostics.String(), "private")
		})
	}
}

func TestOpenCodeAuthRejectsUnsupportedInputBeforeRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "opencode")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho MUST-NOT-RUN\n"), 0700))
	t.Setenv("OPENCODE_EXECUTABLE", binary)
	for _, args := range [][]string{
		{"login", "--method", "API Key"},
		{"login", "--method", "api-key", "--secret-stdin", "--provider", "openai"},
		{"status", "--provider", "openai"},
		{"login", "--provider", "--help"},
		{"methods"},
	} {
		var out, diagnostics bytes.Buffer
		command := append([]string{"auth"}, args...)
		command = append(command, "--harness", "opencode")
		err := runWithInput(t.Context(), command, strings.NewReader("key\n"), &out, &diagnostics)
		require.Error(t, err)
		require.NotContains(t, out.String()+diagnostics.String(), "MUST-NOT-RUN")
	}
}

func TestProviderAuthHelpDocumentsMethodsAndInputBoundaries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out, diagnostics bytes.Buffer
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"auth", "login", "-h"},
			strings.NewReader(""),
			&out,
			&diagnostics,
		),
	)
	for _, text := range []string{"OpenCode:", "Kiro:", "Hermes:", "OpenClaw:", "--provider", "--profile", "--secret-stdin", "Explicit --method is rejected", "masked prompt"} {
		require.Contains(t, out.String(), text)
	}
}
