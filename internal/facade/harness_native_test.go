//go:build !windows

package facade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nativeClaudeFixture(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, ".local", "share", "claude", "versions", version)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))
	require.NoError(
		t,
		os.WriteFile(target, []byte("#!/bin/sh\nprintf '"+version+" (Claude Code)\\n'\n"), 0755),
	)
	path := filepath.Join(root, ".local", "bin", "claude")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.Symlink(target, path))
	return path
}

func TestInspectClaudeNativeInstallation(t *testing.T) {
	path := nativeClaudeFixture(t, "2.1.263")
	got, err := NewHarnessUpgradeFacade().Inspect(t.Context(), &HarnessInspectRequest{Harness: "claude", Component: HarnessComponentCLI, Path: path})
	require.NoError(t, err)
	require.Equal(t, "native", got.Source)
	require.Equal(t, "2.1.263", got.Version)
	require.Equal(t, path, got.Path)
	require.Equal(t, "@anthropic-ai/claude-code", got.Package)
}

type nativeClaudeTransport func(*http.Request) (*http.Response, error)

func (f nativeClaudeTransport) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	return f(req)
}

func mockNativeClaudeDownloads(
	t *testing.T,
	f *HarnessUpgradeFacade,
	version, executable string,
) *[]string {
	t.Helper()
	requests := []string{}
	sum := sha256.Sum256([]byte(executable))
	platform := runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOARCH == "amd64" {
		platform = runtime.GOOS + "-x64"
	}
	manifest, err := json.Marshal(map[string]any{"version": version, "platforms": map[string]any{
		platform: map[string]any{
			"binary":   "claude",
			"checksum": hex.EncodeToString(sum[:]),
			"size":     len(executable),
		},
	}})
	require.NoError(t, err)
	f.nativeHTTP = &http.Client{
		Transport: nativeClaudeTransport(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "https", req.URL.Scheme)
			require.Equal(t, "downloads.claude.ai", req.URL.Host)
			requests = append(requests, req.URL.Path)
			var body string
			switch req.URL.Path {
			case "/claude-code-releases/latest":
				body = version + "\n"
			case "/claude-code-releases/" + version + "/manifest.json":
				body = string(manifest)
			case "/claude-code-releases/" + version + "/" + platform + "/claude":
				body = executable
			default:
				t.Fatalf("unexpected download %s", req.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	return &requests
}

func TestUpgradeNativeClaudePreservesRollbackAfterOriginalIsRemoved(t *testing.T) {
	path := nativeClaudeFixture(t, "2.1.263")
	original, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	f := NewHarnessUpgradeFacade()
	requests := mockNativeClaudeDownloads(
		t,
		f,
		"2.1.264",
		"#!/bin/sh\nprintf '2.1.264 (Claude Code)\\n'\n",
	)
	inspection := HarnessInspectRequest{
		Harness:   "claude",
		Component: HarnessComponentCLI,
		Path:      path,
	}
	upgraded, err := f.Upgrade(
		t.Context(),
		&HarnessUpgradeRequest{HarnessInspectRequest: inspection},
	)
	require.NoError(t, err)
	require.Equal(t, "native", upgraded.Installation.Source)
	require.Equal(t, "2.1.264", upgraded.Installation.Version)
	require.Len(t, *requests, 3)
	require.NoError(t, os.Remove(original))
	rolledBack, err := f.Rollback(
		t.Context(),
		&HarnessRollbackRequest{HarnessInspectRequest: inspection, RollbackID: upgraded.RollbackID},
	)
	require.NoError(t, err)
	require.Equal(t, "2.1.263", rolledBack.Version)
	require.NotEqual(t, original, rolledBack.ResolvedPath)
	_, err = f.Rollback(
		t.Context(),
		&HarnessRollbackRequest{HarnessInspectRequest: inspection, RollbackID: upgraded.RollbackID},
	)
	require.NoError(t, err)
}

func TestNativeClaudePlansLatestWithoutChangingInstallation(t *testing.T) {
	path := nativeClaudeFixture(t, "2.1.263")
	original, err := os.Readlink(path)
	require.NoError(t, err)
	f := NewHarnessUpgradeFacade()
	requests := mockNativeClaudeDownloads(t, f, "2.1.264", "unused")
	req := &HarnessUpgradeRequest{
		HarnessInspectRequest: HarnessInspectRequest{
			Harness:   "claude",
			Component: HarnessComponentCLI,
			Path:      path,
		},
		DryRun: true,
	}
	planned, err := f.Upgrade(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "planned", planned.Phase)
	require.Equal(t, "2.1.264", planned.TargetVersion)
	require.Equal(t, []string{"/claude-code-releases/latest"}, *requests)
	req.Version = "2.1.265"
	planned, err = f.Upgrade(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "2.1.265", planned.TargetVersion)
	require.Len(t, *requests, 1)
	current, err := os.Readlink(path)
	require.NoError(t, err)
	require.Equal(t, original, current)
	_, err = os.Stat(filepath.Join(filepath.Dir(path), ".paxl-harness-versions"))
	require.True(t, os.IsNotExist(err))
}

func TestNativeClaudeRejectsUnverifiedDownloadsWithoutSwitching(t *testing.T) {
	for _, tc := range []struct {
		name, route, body, message string
		status                     int
	}{
		{name: "checksum", route: "/claude", body: "corrupted", message: "checksum mismatch"},
		{name: "wrong manifest version", route: "manifest.json", body: `{"version":"9.9.9"}`, message: "invalid native Claude manifest"},
		{name: "missing platform", route: "manifest.json", body: `{"version":"2.1.264","platforms":{}}`, message: "invalid native Claude manifest"},
		{name: "invalid manifest", route: "manifest.json", body: `{`, message: "decode native Claude manifest"},
		{name: "invalid latest", route: "latest", body: "../../untrusted", message: "not an exact version"},
		{name: "large latest", route: "latest", body: strings.Repeat("1", 129), message: "size limit"},
		{name: "redirect", route: "manifest.json", status: 302, message: "HTTP 302"},
		{name: "missing binary", route: "/claude", status: 404, message: "HTTP 404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := nativeClaudeFixture(t, "2.1.263")
			original, err := os.Readlink(path)
			require.NoError(t, err)
			f := NewHarnessUpgradeFacade()
			mockNativeClaudeDownloads(
				t,
				f,
				"2.1.264",
				"#!/bin/sh\nprintf '2.1.264 (Claude Code)\\n'\n",
			)
			client, ok := f.nativeHTTP.(*http.Client)
			require.True(t, ok)
			transport := client.Transport
			client.Transport = nativeClaudeTransport(
				func(req *http.Request) (*http.Response, error) {
					resp, err := transport.RoundTrip(req)
					if err == nil && strings.HasSuffix(req.URL.Path, tc.route) {
						require.NoError(t, resp.Body.Close())
						resp.Body = io.NopCloser(strings.NewReader(tc.body))
						if tc.status != 0 {
							resp.StatusCode = tc.status
						}
					}
					return resp, err
				},
			)
			_, err = f.Upgrade(
				t.Context(),
				&HarnessUpgradeRequest{
					HarnessInspectRequest: HarnessInspectRequest{
						Harness:   "claude",
						Component: HarnessComponentCLI,
						Path:      path,
					},
				},
			)
			require.ErrorContains(t, err, tc.message)
			current, err := os.Readlink(path)
			require.NoError(t, err)
			require.Equal(t, original, current)
			entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), ".paxl-harness-versions"))
			if !os.IsNotExist(err) {
				require.NoError(t, err)
				require.Empty(t, entries)
			}
		})
	}
}

func TestNativeClaudeRejectsBinaryReportingAnotherVersion(t *testing.T) {
	path := nativeClaudeFixture(t, "2.1.263")
	original, err := os.Readlink(path)
	require.NoError(t, err)
	f := NewHarnessUpgradeFacade()
	requests := mockNativeClaudeDownloads(
		t,
		f,
		"2.1.264",
		"#!/bin/sh\nprintf '0.0.1 (Claude Code)\\n'\n",
	)
	_, err = f.Upgrade(
		t.Context(),
		&HarnessUpgradeRequest{
			HarnessInspectRequest: HarnessInspectRequest{
				Harness:   "claude",
				Component: HarnessComponentCLI,
				Path:      path,
			},
			Version: "2.1.264",
		},
	)
	require.ErrorContains(t, err, "did not report expected version")
	require.Len(t, *requests, 2)
	current, err := os.Readlink(path)
	require.NoError(t, err)
	require.Equal(t, original, current)
}

func TestNativeClaudeInspectionRejectsWrongIdentity(t *testing.T) {
	for _, output := range []string{"2.1.262 (Claude Code)", "2.1.263 (Other Tool)"} {
		t.Run(output, func(t *testing.T) {
			path := nativeClaudeFixture(t, "2.1.263")
			require.NoError(
				t,
				os.WriteFile(path, []byte("#!/bin/sh\nprintf '"+output+"\\n'\n"), 0755),
			)
			_, err := NewHarnessUpgradeFacade().Inspect(t.Context(), &HarnessInspectRequest{Harness: "claude", Component: HarnessComponentCLI, Path: path})
			require.ErrorContains(t, err, "did not report expected version")
		})
	}
}
