//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemainingSDKLoginFlowsThroughLocalFacadeAndKeepsSecretsOffOutput(t *testing.T) {
	for _, tc := range []struct{ harness, pkg string }{{"gemini", "@google/gemini-cli"}, {"dsh", "@deepseek-ai/dsh"}, {"dscode", "todd-dsh-tui-harness"}} {
		t.Run(tc.harness, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(root, "package.json"),
					[]byte(fmt.Sprintf(`{"name":%q}`, tc.pkg)),
					0600,
				),
			)
			launcher := filepath.Join(root, tc.harness)
			require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexit 99\n"), 0700))
			t.Setenv(strings.ToUpper(tc.harness)+"_EXECUTABLE", launcher)
			node := filepath.Join(root, "node")
			if tc.harness == "dscode" {
				node = filepath.Join(root, "runtime", "bin", "node")
				require.NoError(t, os.MkdirAll(filepath.Dir(node), 0700))
			}
			script := "#!/bin/sh\nread -r key\n[ \"$key\" = 'fixture-stdin-key' ] || exit 23\nprintf '%s\\n' '{\"harness\":\"" + tc.harness + "\",\"state\":\"succeeded\",\"method\":\"api-key\"}'\n"
			require.NoError(t, os.WriteFile(node, []byte(script), 0700))
			t.Setenv("PAXL_AUTH_NODE", node)
			var output, diagnostics bytes.Buffer
			args := []string{
				"auth",
				"login",
				"--harness",
				tc.harness,
				"--method",
				"api-key",
				"--secret-stdin",
				"--format",
				"json",
			}
			if tc.harness != "gemini" {
				args = append(args, "--provider", "openai")
			}
			require.NoError(
				t,
				runWithInput(
					t.Context(),
					args,
					strings.NewReader("fixture-stdin-key\n"),
					&output,
					&diagnostics,
				),
			)
			require.JSONEq(
				t,
				`{"harness":"`+tc.harness+`","state":"succeeded","method":"api-key"}`,
				output.String(),
			)
			require.NotContains(t, output.String()+diagnostics.String(), "fixture-stdin-key")
		})
	}
}
func TestAntigravityAliasDescribesItsNativeAccountLimitWithoutStartingIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("AGY_ADC_AUTH", "")
	t.Setenv("AGY_EXECUTABLE", "/not/a/real/agy")
	var output, diagnostics bytes.Buffer
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"auth", "status", "--harness", "agy", "--format", "json"},
			strings.NewReader(""),
			&output,
			&diagnostics,
		),
	)
	require.Contains(t, output.String(), `"state":"unknown"`)
	require.Contains(t, output.String(), "native account keyring")
	output.Reset()
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"auth", "methods", "--harness", "antigravity"},
			strings.NewReader(""),
			&output,
			&diagnostics,
		),
	)
	require.Contains(t, output.String(), "interactive")
	require.Contains(t, output.String(), "adc")
}
func TestLocalAuthHelpIncludesEveryRemainingHarnessAndNativeLimits(t *testing.T) {
	var output, diagnostics bytes.Buffer
	require.NoError(
		t,
		runWithInput(
			t.Context(),
			[]string{"auth", "login", "-h"},
			strings.NewReader(""),
			&output,
			&diagnostics,
		),
	)
	for _, text := range []string{"Gemini:", "DSH / DSCODE:", "Antigravity", "Remote Control", "--secret-stdin", "unknown"} {
		require.Contains(t, output.String(), text)
	}
}
