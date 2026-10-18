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

func TestKiroAndHermesAuthDelegateToDedicatedNativeCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct{ harness, operation, provider, method, args string }{
		{"kiro", "login", "", "", "login"},
		{"kiro", "login", "", "device", "login --use-device-flow"},
		{"kiro", "logout", "", "", "logout"},
		{"hermes", "login", "openai-codex", "oauth", "auth add openai-codex --type oauth"},
		{"hermes", "login", "anthropic", "api-key", "auth add anthropic --type api-key"},
		{"hermes", "logout", "anthropic", "", "auth logout anthropic"},
	} {
		t.Run(tc.harness+tc.operation+tc.method, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "native")
			require.NoError(
				t,
				os.WriteFile(
					binary,
					[]byte(
						"#!/bin/sh\n[ \"$*\" = '"+tc.args+"' ] || exit 23\necho 'Native authentication interaction' >&2\n",
					),
					0700,
				),
			)
			t.Setenv("KIRO_CLI_EXECUTABLE", binary)
			t.Setenv("HERMES_EXECUTABLE", binary)
			args := []string{"auth", tc.operation, "--harness", tc.harness}
			if tc.provider != "" {
				args = append(args, "--provider", tc.provider)
			}
			if tc.method != "" {
				args = append(args, "--method", tc.method)
			}
			var out, diagnostics bytes.Buffer
			require.NoError(
				t,
				runWithInput(t.Context(), args, strings.NewReader(""), &out, &diagnostics),
			)
			require.Contains(t, out.String(), "State: succeeded")
			require.Contains(t, diagnostics.String(), "Native authentication interaction")
		})
	}
}

func TestAdditionalAuthStatusReturnsOnlyRecognizedMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		harness, provider, args, output, state string
		fails                                  bool
	}{
		{"kiro", "", "whoami --format json", `{"accountType":"BuilderId","email":"private@example.test","token":"private"}`, "configured", false},
		{"kiro", "", "whoami --format json", `{"email":"private@example.test"}`, "", true},
		{"hermes", "anthropic", "auth status anthropic", "anthropic: logged in\n  private details", "configured", false},
		{"hermes", "anthropic", "auth status anthropic", "anthropic: logged out", "unknown", false},
		{"hermes", "anthropic", "auth status anthropic", "anthropic: logged out (network failure)", "", true},
	} {
		t.Run(tc.harness+tc.state+tc.output[:8], func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "native")
			script := "#!/bin/sh\n[ \"$*\" = '" + tc.args + "' ] || exit 23\ncat <<'OUTPUT'\n" + tc.output + "\nOUTPUT\n"
			require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
			t.Setenv("KIRO_CLI_EXECUTABLE", binary)
			t.Setenv("HERMES_EXECUTABLE", binary)
			args := []string{"auth", "status", "--harness", tc.harness, "--format", "json"}
			if tc.provider != "" {
				args = append(args, "--provider", tc.provider)
			}
			var out, diagnostics bytes.Buffer
			err := runWithInput(t.Context(), args, strings.NewReader(""), &out, &diagnostics)
			if tc.fails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Contains(t, out.String(), `"state":"`+tc.state+`"`)
			}
			require.NotContains(t, out.String()+diagnostics.String(), "private")
		})
	}
}
