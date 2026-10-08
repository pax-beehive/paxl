package facade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessInspectionBindsVersionToSelectedInstallation(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "lib", "node_modules", "@agentclientprotocol", "codex-acp")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(
				`{"name":"@agentclientprotocol/codex-acp","version":"1.2.3","bin":{"codex-acp":"bin/codex.js"}}`,
			),
			0644,
		),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "bin", "codex.js"),
			[]byte("#!/bin/sh\necho 'codex-acp 1.2.3'\n"),
			0755,
		),
	)
	launcher := filepath.Join(root, "bin", "codex-acp")
	require.NoError(t, os.MkdirAll(filepath.Dir(launcher), 0755))
	require.NoError(t, os.Symlink(filepath.Join(pkg, "bin", "codex.js"), launcher))
	resp, err := NewHarnessUpgradeFacade().Inspect(t.Context(), &HarnessInspectRequest{
		Harness: "codex", Component: HarnessComponentACP, Path: launcher,
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.3", resp.Version)
	require.Equal(t, "@agentclientprotocol/codex-acp", resp.Package)
	require.Equal(t, launcher, resp.Path)
	resolved, err := filepath.EvalSymlinks(filepath.Join(pkg, "bin", "codex.js"))
	require.NoError(t, err)
	require.Equal(t, resolved, resp.ResolvedPath)
}

func TestHarnessUpgradeRejectsAmbiguousInstallationsAndVersions(t *testing.T) {
	root := t.TempDir()
	writeHarnessPackage(t, root, "1.2.3")
	f := NewHarnessUpgradeFacade()
	request := HarnessInspectRequest{
		Harness:   "codex",
		Component: HarnessComponentACP,
		Path:      filepath.Join(root, "bin", "codex-acp"),
	}
	for _, version := range []string{"nightly", "1.2", "v1.2.3", "file:/tmp/package", "1.2.3 --help"} {
		_, err := f.Upgrade(
			t.Context(),
			&HarnessUpgradeRequest{HarnessInspectRequest: request, Version: version},
		)
		require.ErrorContains(t, err, "exact semantic")
	}
	planned, err := f.Upgrade(
		t.Context(),
		&HarnessUpgradeRequest{HarnessInspectRequest: request, Version: "1.2.4", DryRun: true},
	)
	require.NoError(t, err)
	require.Equal(t, "planned", planned.Phase)
	require.NoDirExists(t, filepath.Join(root, "bin", ".paxl-harness-versions"))
	request.Path = filepath.Join(
		root,
		"lib",
		"node_modules",
		"@agentclientprotocol",
		"codex-acp",
		"bin",
		"codex.js",
	)
	_, err = f.Upgrade(
		t.Context(),
		&HarnessUpgradeRequest{HarnessInspectRequest: request, Version: "1.2.4"},
	)
	require.ErrorContains(t, err, "symbolic link")
	request.Harness = "claude"
	_, err = f.Inspect(t.Context(), &request)
	require.ErrorContains(t, err, "unsupported installation")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = f.Inspect(ctx, &request)
	require.ErrorIs(t, err, context.Canceled)
}

func TestHarnessUpgradeRetainsLauncherWhenInstallationOrVerificationFails(t *testing.T) {
	for _, mode := range []string{"install-fails", "wrong-version", "broken-executable"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeHarnessPackage(t, root, "1.2.3")
			launcher := filepath.Join(root, "bin", "codex-acp")
			original, err := os.Readlink(launcher)
			require.NoError(t, err)
			script := "#!/bin/sh\nexit 1\n"
			if mode != "install-fails" {
				version, body := "1.2.4", "exit 1"
				if mode == "wrong-version" {
					version, body = "8.0.0", "echo codex-acp 8.0.0"
				}
				script = fmt.Sprintf(`#!/bin/sh
set -eu
prefix="$4"
mkdir -p "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin" "$prefix/bin"
echo '{"name":"@agentclientprotocol/codex-acp","version":"%s","bin":{"codex-acp":"bin/codex.js"}}' > "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/package.json"
printf '#!/bin/sh\n%s\n' > "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin/codex.js"
chmod +x "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin/codex.js"
ln -s "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin/codex.js" "$prefix/bin/codex-acp"
`, version, body)
			}
			require.NoError(
				t,
				os.WriteFile(filepath.Join(root, "bin", "npm"), []byte(script), 0755),
			)
			t.Setenv(
				"PATH",
				filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
			)
			_, err = NewHarnessUpgradeFacade().Upgrade(t.Context(), &HarnessUpgradeRequest{
				HarnessInspectRequest: HarnessInspectRequest{
					Harness:   "codex",
					Component: HarnessComponentACP,
					Path:      launcher,
				}, Version: "1.2.4",
			})
			require.Error(t, err)
			after, err := os.Readlink(launcher)
			require.NoError(t, err)
			require.Equal(t, original, after)
		})
	}
}

func TestHarnessUpgradeStagesSelectedPackageAndPreservesOldInstallation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	old := filepath.Join(root, "old")
	writeHarnessPackage(t, old, "1.2.3")
	npmDir := filepath.Join(root, "npm-bin")
	require.NoError(t, os.MkdirAll(npmDir, 0755))
	script := `#!/bin/sh
set -eu
if [ "$1" = view ]; then printf '"1.2.4"\n'; exit 0; fi
[ "$8" = "@agentclientprotocol/codex-acp@1.2.4" ] || exit 22
prefix="$4"
mkdir -p "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin" "$prefix/bin"
echo '{"name":"@agentclientprotocol/codex-acp","version":"1.2.4","bin":{"codex-acp":"bin/codex.js"}}' > "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/package.json"
printf '#!/bin/sh\necho "codex-acp 1.2.4"\n' > "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin/codex.js"
chmod +x "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin/codex.js"
ln -s "$prefix/lib/node_modules/@agentclientprotocol/codex-acp/bin/codex.js" "$prefix/bin/codex-acp"
`
	require.NoError(t, os.WriteFile(filepath.Join(npmDir, "npm"), []byte(script), 0755))
	t.Setenv("PATH", npmDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := NewHarnessUpgradeFacade()
	resp, err := f.Upgrade(t.Context(), &HarnessUpgradeRequest{
		HarnessInspectRequest: HarnessInspectRequest{
			Harness:   "codex",
			Component: HarnessComponentACP,
			Path:      filepath.Join(old, "bin", "codex-acp"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.4", resp.Installation.Version)
	data, err := os.ReadFile(
		filepath.Join(
			old,
			"lib",
			"node_modules",
			"@agentclientprotocol",
			"codex-acp",
			"package.json",
		),
	)
	require.NoError(t, err)
	require.Contains(t, string(data), "1.2.3")
	require.NotEmpty(t, resp.RollbackID)
	restored, err := f.Rollback(t.Context(), &HarnessRollbackRequest{
		HarnessInspectRequest: HarnessInspectRequest{
			Harness:   "codex",
			Component: HarnessComponentACP,
			Path:      filepath.Join(old, "bin", "codex-acp"),
		},
		RollbackID: resp.RollbackID,
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.3", restored.Version)
}

func writeHarnessPackage(t *testing.T, root, version string) {
	t.Helper()
	pkg := filepath.Join(root, "lib", "node_modules", "@agentclientprotocol", "codex-acp")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(
				fmt.Sprintf(
					`{"name":"@agentclientprotocol/codex-acp","version":%q,"bin":{"codex-acp":"bin/codex.js"}}`,
					version,
				),
			),
			0644,
		),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "bin", "codex.js"),
			[]byte(fmt.Sprintf("#!/bin/sh\necho 'codex-acp %s'\n", version)),
			0755,
		),
	)
	require.NoError(
		t,
		os.Symlink(filepath.Join(pkg, "bin", "codex.js"), filepath.Join(root, "bin", "codex-acp")),
	)
}

func TestPiAdapterInspectionUsesScopedPackage(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "lib", "node_modules", "@ccgv2", "pi-acp")
	require.NoError(t, os.MkdirAll(pkg, 0755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(`{"name":"@ccgv2/pi-acp","version":"0.6.0","bin":{"pi-acp":"index.mjs"}}`),
			0600,
		),
	)
	entry := filepath.Join(pkg, "index.mjs")
	require.NoError(t, os.WriteFile(entry, []byte("#!/usr/bin/env node\nprocess.exit(0);\n"), 0755))
	installed, err := NewHarnessUpgradeFacade().Inspect(context.Background(), &HarnessInspectRequest{Harness: "pi", Component: HarnessComponentACP, Path: entry})
	require.NoError(t, err)
	require.Equal(t, "@ccgv2/pi-acp", installed.Package)
	require.Equal(t, "0.6.0", installed.Version)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(pkg, "package.json"),
			[]byte(`{"name":"pi-acp","version":"0.6.0","bin":{"pi-acp":"index.mjs"}}`),
			0600,
		),
	)
	_, err = NewHarnessUpgradeFacade().Inspect(context.Background(), &HarnessInspectRequest{Harness: "pi", Component: HarnessComponentACP, Path: entry})
	require.ErrorContains(t, err, "unsupported installation")
}

func TestNativeHarnessPackagesCanBeInspected(t *testing.T) {
	for _, item := range []struct{ harness, name, bin string }{{"claude", "@anthropic-ai/claude-code", "claude"}, {"codex", "@openai/codex", "codex"}, {"pi", "@earendil-works/pi-coding-agent", "pi"}} {
		t.Run(item.harness, func(t *testing.T) {
			root := t.TempDir()
			pkg := filepath.Join(root, "lib", "node_modules", filepath.FromSlash(item.name))
			require.NoError(t, os.MkdirAll(pkg, 0755))
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(pkg, "package.json"),
					[]byte(
						fmt.Sprintf(
							`{"name":%q,"version":"1.2.3","bin":{%q:"entry.js"}}`,
							item.name,
							item.bin,
						),
					),
					0600,
				),
			)
			entry := filepath.Join(pkg, "entry.js")
			require.NoError(t, os.WriteFile(entry, []byte("#!/bin/sh\necho 1.2.3\n"), 0755))
			result, err := NewHarnessUpgradeFacade().Inspect(context.Background(), &HarnessInspectRequest{Harness: item.harness, Component: HarnessComponent("cli"), Path: entry})
			require.NoError(t, err)
			require.Equal(t, item.name, result.Package)
		})
	}
}

func TestHarnessUpgradeResolvesLatestBeforePlanning(t *testing.T) {
	root := t.TempDir()
	writeHarnessPackage(t, root, "1.2.3")
	bin := filepath.Join(root, "bin")
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(bin, "npm"),
			[]byte(
				"#!/bin/sh\n[ \"$1\" = view ] || exit 20\n[ \"$2\" = '@agentclientprotocol/codex-acp@latest' ] || exit 21\nprintf '\"1.2.4\"\\n'\nprintf 'registry notice\\n' >&2\n",
			),
			0755,
		),
	)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	request := &HarnessUpgradeRequest{
		HarnessInspectRequest: HarnessInspectRequest{
			Harness:   "codex",
			Component: HarnessComponentACP,
			Path:      filepath.Join(bin, "codex-acp"),
		},
		DryRun: true,
	}
	result, err := NewHarnessUpgradeFacade().Upgrade(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "1.2.4", result.TargetVersion)
	require.Equal(t, "1.2.3", result.Installation.Version)
	require.Empty(t, request.Version)
	require.NoDirExists(t, filepath.Join(bin, ".paxl-harness-versions"))
}

func TestHarnessLatestLookupFailurePreservesInstallation(t *testing.T) {
	for _, body := range []string{"exit 2", "printf '\"latest\"'", "printf '[\"1.2.4\"]'", "printf 'invalid-json'"} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeHarnessPackage(t, root, "1.2.3")
			bin := filepath.Join(root, "bin")
			launcher := filepath.Join(bin, "codex-acp")
			before, err := os.Readlink(launcher)
			require.NoError(t, err)
			require.NoError(
				t,
				os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\n"+body+"\n"), 0755),
			)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			request := HarnessInspectRequest{
				Harness:   "codex",
				Component: HarnessComponentACP,
				Path:      launcher,
			}
			_, err = NewHarnessUpgradeFacade().Upgrade(t.Context(), &HarnessUpgradeRequest{HarnessInspectRequest: request})
			require.Error(t, err)
			after, err := os.Readlink(launcher)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.NoDirExists(t, filepath.Join(bin, ".paxl-harness-versions"))
			// A pinned dry run works without registry access even when npm is unavailable.
			planned, err := NewHarnessUpgradeFacade().Upgrade(t.Context(), &HarnessUpgradeRequest{HarnessInspectRequest: request, Version: "1.2.4", DryRun: true})
			require.NoError(t, err)
			require.Equal(t, "1.2.4", planned.TargetVersion)
		})
	}
}
