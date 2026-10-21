package facade

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func hermesFeedFixture() string {
	digest := sha512.Sum512([]byte("fixture bundle"))
	return fmt.Sprintf(
		"version: 0.22.0\nfiles:\n  - url: /releases/tag/rc.3-v0.22.0/HermesBundled-0.22.0-mac-arm64.zip\n    sha512: %s\n    size: 14\n",
		base64.StdEncoding.EncodeToString(digest[:]),
	)
}

func TestHermesPrebuiltFeedSelectsCompleteMatchingRuntime(t *testing.T) {
	artifact, err := parseHermesPrebuiltFeed([]byte(hermesFeedFixture()), "arm64")
	require.NoError(t, err)
	require.Equal(t, "0.22.0", artifact.Version)
	require.Equal(
		t,
		hermesArtifactOrigin+"/releases/tag/rc.3-v0.22.0/HermesBundled-0.22.0-mac-arm64.zip",
		artifact.URL,
	)
	artifact, err = parseHermesPrebuiltFeed([]byte(hermesFeedFixture()), "x64")
	require.ErrorIs(t, err, errHermesPrebuiltMissing)
	require.Nil(t, artifact)
	for _, body := range []string{
		strings.ReplaceAll(hermesFeedFixture(), "/releases/tag/", "https://untrusted.example/releases/tag/"),
		strings.ReplaceAll(hermesFeedFixture(), "HermesBundled-", "Hermes-Setup-"),
	} {
		artifact, err = parseHermesPrebuiltFeed([]byte(body), "arm64")
		require.True(t, err != nil || artifact == nil)
	}
}

func TestHermesPrebuiltFallsBackToNewestPublishedPlatformReceipt(t *testing.T) {
	f := NewHarnessInstallFacade(nil)
	var requested []string
	f.client = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		status, body := 200, ""
		switch {
		case req.URL.String() == hermesMacFeed:
			body = hermesFeedFixture()
		case req.URL.Host == "api.github.com":
			body = `[{"tag_name":"v0.22.2"},{"tag_name":"v0.22.1","prerelease":true},{"tag_name":"v0.21.7"},{"tag_name":"v0.21.6"}]`
		case strings.Contains(req.URL.Path, "v0.22.2/"):
			status = 404
		case strings.Contains(req.URL.Path, "v0.21.7/"):
			body = `{"schema":1,"tag":"v0.21.7","name":"darwin-x64","files":[{"path":"HermesBundled-0.21.7-mac-x64.zip","size":14,"sha256":"` + strings.Repeat(
				"a",
				64,
			) + `"}]}`
		default:
			t.Fatalf("Unexpected metadata request %s", req.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	artifact, err := f.resolveHermesPrebuilt(t.Context(), "darwin", "amd64")
	require.NoError(t, err)
	require.Equal(t, "0.21.7", artifact.Version)
	require.Len(t, requested, 4)
	_, err = f.resolveHermesPrebuilt(t.Context(), "linux", "amd64")
	require.ErrorContains(t, err, "source compilation is disabled")
}

func TestHermesPrebuiltRejectsCorruptDownloads(t *testing.T) {
	f := NewHarnessInstallFacade(nil)
	f.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("fixture bundle")),
		}, nil
	})
	digest := sha256.Sum256([]byte("fixture bundle"))
	artifact := &hermesPrebuiltArtifact{
		URL:    hermesArtifactOrigin + "/fixture.zip",
		Size:   14,
		Digest: hex.EncodeToString(digest[:]),
	}
	require.NoError(
		t,
		f.downloadHermesPrebuilt(t.Context(), artifact, filepath.Join(t.TempDir(), "valid.zip")),
	)
	artifact.Digest = strings.Repeat("0", 64)
	require.ErrorContains(
		t,
		f.downloadHermesPrebuilt(t.Context(), artifact, filepath.Join(t.TempDir(), "corrupt.zip")),
		"checksum mismatch",
	)
}

func TestHermesBundleRequiresPrebuiltCLIManifest(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "Hermes.app", "Contents", "Resources")
	require.NoError(t, os.MkdirAll(filepath.Join(resources, "agent-payload", "bin"), 0700))
	manifest := filepath.Join(resources, "install-stamp.json")
	require.NoError(
		t,
		os.WriteFile(
			manifest,
			[]byte(`{"payload":"bootstrap","runtime":{"commands":{"hermes":"bin/hermes"}}}`),
			0600,
		),
	)
	_, err := hermesBundledLauncher(root)
	require.ErrorContains(t, err, "prebuilt CLI runtime")
	launcher := filepath.Join(resources, "agent-payload", "bin", "hermes")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700))
	require.NoError(
		t,
		os.WriteFile(
			manifest,
			[]byte(`{"payload":"bundled","runtime":{"commands":{"hermes":"bin/hermes"}}}`),
			0600,
		),
	)
	actual, err := hermesBundledLauncher(root)
	require.NoError(t, err)
	require.Equal(t, launcher, actual)
}

func TestHermesBundlePublishesCompleteRuntimeAtomically(t *testing.T) {
	home := t.TempDir()
	stage := filepath.Join(home, "stage")
	unpack := filepath.Join(stage, "bundle")
	launcher := filepath.Join(
		unpack,
		"Hermes.app",
		"Contents",
		"Resources",
		"agent-payload",
		"bin",
		"hermes",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(launcher), 0700))
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700))
	entry, err := publishHermesBundle(home, stage, unpack, launcher, "0.22.0")
	require.NoError(t, err)
	data, err := os.ReadFile(entry)
	require.NoError(t, err)
	require.Contains(t, string(data), "exit 0")
	_, err = os.Stat(unpack)
	require.True(t, os.IsNotExist(err))
}

type hermesBundleFixtureRunner struct{ calls int }

func (*hermesBundleFixtureRunner) LookPath(name string) (string, error) {
	if name != "ditto" {
		return "", fmt.Errorf("unexpected installer dependency %q", name)
	}
	return name, nil
}

func (r *hermesBundleFixtureRunner) Run(_ context.Context, name string, args []string) error {
	if name != "ditto" || len(args) != 4 {
		return fmt.Errorf("unexpected installer command %q", name)
	}
	r.calls++
	resources := filepath.Join(args[3], "Hermes.app", "Contents", "Resources")
	launcher := filepath.Join(resources, "agent-payload", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(resources, "install-stamp.json"),
		[]byte(`{"payload":"bundled","runtime":{"commands":{"hermes":"bin/hermes"}}}`),
		0600,
	)
}

func TestHermesInstallationUsesPrebuiltRuntimeWithoutBuildingOrLoggingIn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS prebuilt installer")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &hermesBundleFixtureRunner{}
	f := NewHarnessInstallFacade(runner)
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	f.client = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := "fixture bundle"
		if req.URL.String() == hermesMacFeed {
			body = strings.ReplaceAll(hermesFeedFixture(), "mac-arm64", "mac-"+arch)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	result, err := f.Install(t.Context(), &HarnessInstallRequest{Harness: "hermes"})
	require.NoError(t, err)
	require.Equal(t, "installed", result.State)
	require.Equal(t, "0.22.0", result.Version)
	require.Equal(t, 1, runner.calls)
	_, err = os.Stat(filepath.Join(home, ".local", "bin", "hermes"))
	require.NoError(t, err)
}
