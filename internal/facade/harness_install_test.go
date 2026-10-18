package facade

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalPiInstallationUsesOfficialPackage(t *testing.T) {
	runner := &fakeDaemonLifecycleRunner{path: "/local/bin/npm"}
	result, err := NewHarnessInstallFacade(
		runner,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "pi", Via: HarnessInstallViaNPM})
	require.NoError(t, err)
	require.Equal(t, "/local/bin/npm", runner.name)
	require.Equal(
		t,
		[]string{"install", "-g", "@earendil-works/pi-coding-agent@latest"},
		runner.args,
	)
	require.Equal(t, "installed", result.State)
}

func TestPiInstallDryRunAndValidation(t *testing.T) {
	result, err := NewHarnessInstallFacade(
		nil,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "pi", Component: HarnessComponentACP, DryRun: true})
	require.NoError(t, err)
	require.Equal(t, "@ccgv2/pi-acp", result.Package)
	require.Equal(t, "planned", result.State)
	for _, req := range []*HarnessInstallRequest{nil, {Harness: "invalid"}, {Harness: "pi", Version: "latest;sh"}, {Harness: "pi", Component: HarnessComponent("invalid")}} {
		_, err := NewHarnessInstallFacade(nil).Install(t.Context(), req)
		require.Error(t, err)
	}
}

func TestOfficialClaudeInstallPlanUsesNativeScript(t *testing.T) {
	result, err := NewHarnessInstallFacade(
		nil,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "claude", DryRun: true})
	require.NoError(t, err)
	require.Equal(t, "https://claude.ai/install.sh", result.ScriptURL)
	require.Equal(t, "official", string(result.Via))
	require.Equal(t, []string{"bash", "<downloaded-official-installer>", "latest"}, result.Command)
}

func TestOfficialInstallationPlansCoverEveryHarness(t *testing.T) {
	cases := []struct{ name, source, target string }{
		{"claude", "script", "https://claude.ai/install.sh"},
		{"codex", "script", "https://chatgpt.com/codex/install.sh"},
		{"pi", "script", "https://pi.dev/install.sh"},
		{"gemini", "npm", "@google/gemini-cli"},
		{"antigravity", "script", "https://antigravity.google/cli/install.sh"},
		{"kimi", "script", "https://code.kimi.com/kimi-code/install.sh"},
		{"opencode", "script", "https://opencode.ai/install"},
		{"hermes", "script", "https://hermes-agent.nousresearch.com/install.sh"},
		{"openclaw", "script", "https://openclaw.ai/install.sh"},
		{"kiro", "script", "https://cli.kiro.dev/install"},
		{"dsh", "npm", "@deepseek-ai/dsh"},
		{"dscode", "script", "https://raw.githubusercontent.com/qiz029/dscode/main/install.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NewHarnessInstallFacade(
				nil,
			).Install(t.Context(), &HarnessInstallRequest{Harness: tc.name, DryRun: true})
			require.NoError(t, err)
			require.Equal(t, HarnessInstallViaOfficial, result.Via)
			require.Equal(t, tc.source, result.Source)
			require.Contains(t, []string{result.ScriptURL, result.Package}, tc.target)
			require.Equal(t, "planned", result.State)
		})
	}
	for _, harness := range []string{"claude", "codex", "pi"} {
		result, err := NewHarnessInstallFacade(
			nil,
		).Install(t.Context(), &HarnessInstallRequest{Harness: harness, Component: HarnessComponentACP, DryRun: true})
		require.NoError(t, err)
		require.Equal(t, "npm", result.Source)
	}
}

func TestOfficialInstallVersionOptionsAndRouteValidation(t *testing.T) {
	for _, tc := range []struct {
		harness string
		args    []string
	}{
		{"claude", []string{"2.1.0"}}, {"codex", []string{"--release", "2.1.0"}},
		{"kimi", []string{"--version", "2.1.0"}}, {"opencode", []string{"--version", "2.1.0"}},
		{"openclaw", []string{"--no-onboard", "--version", "2.1.0"}}, {"dscode", []string{"2.1.0"}},
	} {
		result, err := NewHarnessInstallFacade(
			nil,
		).Install(t.Context(), &HarnessInstallRequest{Harness: tc.harness, Version: "2.1.0", DryRun: true})
		require.NoError(t, err)
		require.Equal(t, tc.args, result.Command[2:])
	}
	for _, req := range []*HarnessInstallRequest{
		{Harness: "pi", Version: "1.3.0"}, {Harness: "kiro", Version: "1.3.0"}, {Harness: "hermes", Version: "1.3.0"},
		{Harness: "pi", Via: HarnessInstallVia("brew")}, {Harness: "claude", Via: HarnessInstallViaNPM},
		{Harness: "pi", Component: HarnessComponentACP, Via: HarnessInstallViaNPM}, {Harness: "gemini", Component: HarnessComponentACP},
	} {
		_, err := NewHarnessInstallFacade(nil).Install(t.Context(), req)
		require.Error(t, err)
	}
}

func TestAntigravityOfficialInstallUsesPublisherScript(t *testing.T) {
	result, err := NewHarnessInstallFacade(
		nil,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "antigravity", DryRun: true})
	require.NoError(t, err)
	require.Equal(t, "https://antigravity.google/cli/install.sh", result.ScriptURL)
	require.Equal(t, []string{"bash", "<downloaded-official-installer>"}, result.Command)
	require.Equal(t, HarnessInstallViaOfficial, result.Via)
	_, err = NewHarnessInstallFacade(
		nil,
	).Install(t.Context(), &HarnessInstallRequest{Harness: "antigravity", Version: "1.2.3", DryRun: true})
	require.ErrorContains(t, err, "omit --version")
}
