//go:build !windows

package adaptor_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

func nativeAuthFixture(t *testing.T, harness, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "native-auth")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700))
	key := map[string]string{"opencode": "OPENCODE_EXECUTABLE", "kiro": "KIRO_CLI_EXECUTABLE", "hermes": "HERMES_EXECUTABLE", "openclaw": "OPENCLAW_EXECUTABLE"}[harness]
	t.Setenv(key, path)
}

func TestProviderAuthStatusNormalizesMetadataAndRejectsUnknownOutput(t *testing.T) {
	for _, tc := range []struct {
		harness, provider, text, state string
		exit                           int
		fails                          bool
	}{
		{harness: "opencode", text: "\x1b[90m┌ Credentials ~/.local/share/opencode/auth.json\x1b[0m\n│ private-name api\n└ 1 credential", state: "configured"},
		{harness: "opencode", text: "┌ Credentials file\n└ 0 credentials", state: "unknown"},
		{harness: "opencode", text: "Credentials file\n└ 1 credential\n└ 2 credentials", fails: true},
		{harness: "opencode", text: "Credentials file\n└ 99999999999999999999999999999 credentials", fails: true},
		{harness: "opencode", text: "Credentials file\nprivate-token", fails: true},
		{harness: "opencode", text: "┌ Credentials file\n└ 1 credential", exit: 1, fails: true},
		{harness: "kiro", text: `{"accountType":"BuilderId","email":"private","token":"private"}`, state: "configured"},
		{harness: "kiro", text: `{"accountType":"IamIdentityCenter"}`, state: "configured"},
		{harness: "kiro", text: `{"accountType":"future"}`, fails: true},
		{harness: "kiro", text: `not-json`, fails: true},
		{harness: "hermes", provider: "anthropic", text: "anthropic: logged in\n private-token", state: "configured"},
		{harness: "hermes", provider: "anthropic", text: "anthropic: logged out", state: "unknown"},
		{harness: "hermes", provider: "anthropic", text: "anthropic: logged out (network failure)", fails: true},
		{harness: "openclaw", text: `{"profiles":[{"id":"openai:manual","provider":"openai","type":"api_key","key":"private"}]}`, state: "configured"},
		{harness: "openclaw", text: `{"profiles":[]}`, state: "unknown"},
		{harness: "openclaw", text: `{}`, fails: true},
		{harness: "openclaw", text: `{"profiles":[{"id":"id","provider":"bad\nprovider","type":"oauth"}]}`, fails: true},
		{harness: "openclaw", text: `{"profiles":[{"id":"id","provider":"openai","type":"future"}]}`, fails: true},
		{harness: "openclaw", provider: "anthropic", text: `{"profiles":[{"id":"id","provider":"openai","type":"oauth"}]}`, fails: true},
	} {
		t.Run(tc.harness+tc.text, func(t *testing.T) {
			script := "cat <<'OUTPUT'\n" + tc.text + "\nOUTPUT\n"
			if tc.exit != 0 {
				script += "exit 1\n"
			}
			nativeAuthFixture(t, tc.harness, script)
			var raw bytes.Buffer
			result, err := adaptor.NewNativeHarnessAuth().
				Status(t.Context(), &adaptor.NativeAuthRequest{Harness: model.AgentName(tc.harness), Provider: tc.provider, Operation: model.HarnessAuthStatus, Stdout: &raw, Stderr: &raw})
			if tc.fails {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, model.HarnessAuthState(tc.state), result.State)
				require.Nil(t, result.LoggedIn)
				require.NotContains(t, result.Message, "private")
			}
			require.Empty(t, raw.String())
		})
	}
}

func TestStructuredAuthStatusIgnoresDiagnosticStderr(t *testing.T) {
	for _, tc := range []struct {
		harness   model.AgentName
		key, data string
		state     model.HarnessAuthState
	}{
		{model.AgentNameClaude, "CLAUDE_CODE_EXECUTABLE", `{"loggedIn":true,"authMethod":"oauth"}`, model.HarnessAuthLoggedIn},
		{model.AgentNameKiro, "KIRO_CLI_EXECUTABLE", `{"accountType":"BuilderId"}`, model.HarnessAuthConfigured},
		{model.AgentNameOpenClaw, "OPENCLAW_EXECUTABLE", `{"profiles":[]}`, model.HarnessAuthStateUnknown},
	} {
		t.Run(string(tc.harness), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth")
			script := "#!/bin/sh\necho private-native-warning >&2\ncat <<'JSON'\n" + tc.data + "\nJSON\n"
			require.NoError(t, os.WriteFile(path, []byte(script), 0700))
			t.Setenv(tc.key, path)
			var output bytes.Buffer
			result, err := adaptor.NewNativeHarnessAuth().
				Status(t.Context(), &adaptor.NativeAuthRequest{
					Harness:   tc.harness,
					Operation: model.HarnessAuthStatus,
					Stdout:    &output,
					Stderr:    &output,
				})
			require.NoError(t, err)
			require.Equal(t, tc.state, result.State)
			require.Empty(t, output.String())
			require.NotContains(t, result.Message, "private-native-warning")
		})
	}
}

func TestProviderAuthUsesSelectedNativeCommandAndPreservesInput(t *testing.T) {
	for _, tc := range []struct{ harness, provider, profile, operation, method, args string }{
		{"opencode", "", "", "login", "", "auth login"},
		{"opencode", "openai", "", "login", "", "auth login --provider openai"},
		{"opencode", "", "", "logout", "", "auth logout"},
		{"opencode", "openai", "", "logout", "", "auth logout openai"},
		{"kiro", "", "", "login", "", "login"},
		{"kiro", "", "", "login", "browser", "login"},
		{"kiro", "", "", "login", "device", "login --use-device-flow"},
		{"kiro", "", "", "logout", "", "logout"},
		{"hermes", "anthropic", "", "login", "api-key", "auth add anthropic --type api-key"},
		{"hermes", "openai-codex", "", "login", "oauth", "auth add openai-codex --type oauth"},
		{"hermes", "nous", "", "login", "", "auth add nous"},
		{"hermes", "", "", "login", "", "auth"},
		{"hermes", "nous", "", "logout", "", "auth logout nous"},
		{"openclaw", "", "", "login", "", "models auth login"},
		{"openclaw", "openai-codex", "", "login", "device-code", "models auth login --provider openai-codex --method device-code"},
		{"openclaw", "openai", "", "login", "api-key", "models auth paste-api-key --provider openai"},
		{"openclaw", "", "openai:manual", "logout", "", "models auth logout openai:manual"},
	} {
		t.Run(tc.harness+tc.args, func(t *testing.T) {
			script := "[ \"$*\" = '" + tc.args + "' ] || exit 23\nread -r value\n[ \"$value\" = 'fixture' ] || exit 24\necho 'Native prompt' >&2\n"
			nativeAuthFixture(t, tc.harness, script)
			var output bytes.Buffer
			require.NoError(
				t,
				adaptor.NewNativeHarnessAuth().
					Run(t.Context(), &adaptor.NativeAuthRequest{Harness: model.AgentName(tc.harness), Provider: tc.provider, Profile: tc.profile, Operation: model.HarnessAuthOperation(tc.operation), Method: model.HarnessAuthMethod(tc.method), Stdin: strings.NewReader("fixture\n"), Stdout: &output, Stderr: &output}),
			)
			if tc.harness == "openclaw" && tc.method == "api-key" {
				require.Empty(t, output.String())
			} else {
				require.Contains(t, output.String(), "Native prompt")
			}
		})
	}
}

func TestProviderAuthRejectsUnsupportedSelectorsBeforeExecution(t *testing.T) {
	for _, req := range []*adaptor.NativeAuthRequest{
		{Harness: "opencode", Operation: "login", Provider: "-provider"},
		{Harness: "opencode", Operation: "status", Method: "API Key"},
		{Harness: "opencode", Operation: "login", Provider: "openai", Method: "bad\nmethod"},
		{Harness: "opencode", Operation: "login", Method: "API Key"},
		{Harness: "opencode", Operation: "status", Provider: "openai"},
		{Harness: "opencode", Operation: "methods"},
		{Harness: "kiro", Operation: "login", Provider: "google"},
		{Harness: "kiro", Operation: "login", Method: "api-key"},
		{Harness: "kiro", Operation: "status", Method: "browser"},
		{Harness: "kiro", Operation: "methods"},
		{Harness: "hermes", Operation: "login", Method: "oauth"},
		{Harness: "hermes", Operation: "login", Provider: "bad provider"},
		{Harness: "hermes", Operation: "login", Provider: "anthropic", Method: "device"},
		{Harness: "hermes", Operation: "methods", Provider: "anthropic"},
		{Harness: "openclaw", Operation: "login", Provider: "bad provider"},
		{Harness: "openclaw", Operation: "status", Method: "oauth"},
		{Harness: "openclaw", Operation: "login", Profile: "openai:manual"},
		{Harness: "openclaw", Operation: "logout"},
		{Harness: "openclaw", Operation: "logout", Profile: "-all"},
		{Harness: "openclaw", Operation: "logout", Profile: "openai:manual", Provider: "openai"},
		{Harness: "openclaw", Operation: "login", Method: "api-key"},
		{Harness: "openclaw", Operation: "login", Method: "oauth"},
		{Harness: "openclaw", Operation: "login", Provider: "openai", Method: "bad\nmethod"},
		{Harness: "openclaw", Operation: "methods"},
	} {
		require.Error(t, adaptor.NewNativeHarnessAuth().Validate(req), "%+v", req)
	}
}
