package facade

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func hermesBuildToolsFixture(t *testing.T, rustVersion string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"bash", "git", "curl", "tar", "shasum", "rustc", "cargo", "xcrun", "pkg-config"} {
		output := "fixture"
		switch name {
		case "rustc":
			output = "rustc " + rustVersion + " (fixture)"
		case "cargo":
			output = "cargo " + rustVersion + " (fixture)"
		case "xcrun":
			output = "/fixture/clang"
		case "pkg-config":
			output = "3.6.3"
		}
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho '"+output+"'\n"), 0700),
		)
	}
	t.Setenv("PATH", dir)
	t.Setenv("OPENSSL_DIR", "")
}

func TestHermesMissingIntelBundleFallsBackAfterBuildPreflight(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "amd64" {
		t.Skip("Intel macOS fallback")
	}
	for _, version := range []string{"1.82.0", "1.99.1"} {
		t.Run(version, func(t *testing.T) {
			hermesBuildToolsFixture(t, version)
			runner := &scriptInstallRunner{}
			f := NewHarnessInstallFacade(runner)
			downloads := 0
			f.client = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := "[]"
				if req.URL.String() == hermesMacFeed {
					body = hermesFeedFixture()
				}
				if req.URL.String() == "https://hermes-agent.nousresearch.com/install.sh" {
					downloads++
					body = "#!/bin/bash\necho fixture\n"
				}
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})
			result, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "hermes"})
			if version == "1.82.0" {
				require.ErrorContains(t, err, "Rust >= 1.83.0")
				require.Zero(t, downloads)
				require.Empty(t, runner.content)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "script", result.Source)
			require.Equal(t, "completed", result.State)
			require.Equal(t, []string{"--skip-setup"}, runner.args)
			require.Equal(t, 1, downloads)
		})
	}
}

func TestHermesMetadataFailuresDoNotStartSourceInstallation(t *testing.T) {
	runner := &scriptInstallRunner{}
	f := NewHarnessInstallFacade(runner)
	f.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 503,
			Body:       io.NopCloser(strings.NewReader("unavailable")),
		}, nil
	})
	_, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "hermes"})
	require.Error(t, err)
	require.Empty(t, runner.content)
}

func TestHermesCorruptBundleDoesNotFallBackToSource(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundles")
	}
	runner := &scriptInstallRunner{}
	f := NewHarnessInstallFacade(runner)
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	f.client = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := "damaged bundle"
		if req.URL.String() == hermesMacFeed {
			body = strings.ReplaceAll(hermesFeedFixture(), "mac-arm64", "mac-"+arch)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Setenv("HOME", t.TempDir())
	_, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "hermes"})
	require.ErrorContains(t, err, "checksum mismatch")
	require.Empty(t, runner.content)
}

func TestHermesPreflightReportsMissingBuildTools(t *testing.T) {
	hermesBuildToolsFixture(t, "1.99.1")
	require.NoError(t, os.Remove(filepath.Join(os.Getenv("PATH"), "xcrun")))
	require.NoError(t, os.Remove(filepath.Join(os.Getenv("PATH"), "pkg-config")))
	err := checkHermesBuildPrerequisites(t.Context())
	require.ErrorContains(t, err, "xcode-select --install")
	require.ErrorContains(t, err, "OpenSSL 3 development files")
}
