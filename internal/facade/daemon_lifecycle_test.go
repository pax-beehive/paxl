package facade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonLifecycleInstallDownloadsAndVerifiesPaxdArtifact(t *testing.T) {
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	var resolverProduct string
	var resolverPlatform string
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/resolve":
			resolverProduct = r.URL.Query().Get("product")
			resolverPlatform = r.URL.Query().Get("platform")
			return jsonResponse(fmt.Sprintf(
				`{"data":{"url":"https://download.test/download/paxd","sha256":"%s","size_bytes":%d,"version":"0.2.0"}}`,
				sha,
				len(binary),
			)), nil
		case "/download/paxd":
			return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser(binary)}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: ioNopCloser(nil)}, nil
		}
	})
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = client
	installDir := t.TempDir()

	resp, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "darwin/arm64",
		InstallDir:  installDir,
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusInstalled, resp.Status)
	assert.Equal(t, "paxd", resp.Binary)
	assert.Equal(t, "paxd 0.2.0 installed.", resp.Message)
	assert.Empty(t, resolverProduct)
	assert.Equal(t, "darwin/arm64", resolverPlatform)
	raw, err := os.ReadFile(filepath.Join(installDir, "paxd"))
	require.NoError(t, err)
	assert.Equal(t, binary, raw)
	info, err := os.Stat(filepath.Join(installDir, "paxd"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestDaemonLifecycleInstallAddsProductForGenericArtifactResolver(t *testing.T) {
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	var resolverProduct string
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v1/public/artifacts/download":
			resolverProduct = r.URL.Query().Get("product")
			return jsonResponse(fmt.Sprintf(
				`{"data":{"url":"https://download.test/download/paxd","sha256":"%s","size_bytes":%d,"version":"0.2.0"}}`,
				sha,
				len(binary),
			)), nil
		case "/download/paxd":
			return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser(binary)}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: ioNopCloser(nil)}, nil
		}
	})
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = client

	_, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/api/v1/public/artifacts/download",
		Platform:    "darwin/arm64",
		InstallDir:  t.TempDir(),
	})

	require.NoError(t, err)
	assert.Equal(t, "paxd", resolverProduct)
}

func TestDaemonLifecycleInstallRejectsChecksumMismatch(t *testing.T) {
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/resolve":
			return jsonResponse(
				`{"data":{"url":"https://download.test/download/paxd","sha256":"bad","size_bytes":4,"version":"0.2.0"}}`,
			), nil
		case "/download/paxd":
			return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser([]byte("paxd"))}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: ioNopCloser(nil)}, nil
		}
	})
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = client

	_, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  t.TempDir(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256")
}

func TestDaemonLifecycleInstallDryRunUsesBinaryOverride(t *testing.T) {
	resp, err := NewDaemonLifecycleFacade(nil).Install(context.Background(), &DaemonInstallRequest{
		DryRun:     true,
		BinaryName: "paxd-test",
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusPending, resp.Status)
	assert.Equal(t, "paxd-test", resp.Binary)
	assert.Contains(t, resp.Message, "Would install paxd")
}

func TestDaemonLifecycleUpdateDryRunReportsUpdateAction(t *testing.T) {
	resp, err := NewDaemonLifecycleFacade(nil).Update(context.Background(), &DaemonUpdateRequest{
		DryRun: true,
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusPending, resp.Status)
	assert.Equal(t, "update", resp.Action)
	assert.Equal(t, "Would update paxd.", resp.Message)
}

func TestDaemonLifecycleUpdateDownloadsAndReportsUpdate(t *testing.T) {
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	installDir := t.TempDir()
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = successfulDaemonArtifactClient(binary, sha)

	resp, err := lifecycle.Update(context.Background(), &DaemonUpdateRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  installDir,
	})

	require.NoError(t, err)
	assert.Equal(t, "update", resp.Action)
	assert.Equal(t, "paxd 0.2.0 updated.", resp.Message)
	assert.FileExists(t, filepath.Join(installDir, "paxd"))
}

func TestDaemonLifecycleUpdateReplacesExistingBinaryWithNewFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inode replacement assertion is unix-specific")
	}
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	installDir := t.TempDir()
	target := filepath.Join(installDir, "paxd")
	require.NoError(t, os.WriteFile(target, []byte("old-paxd-binary"), 0o755))
	before := requireFileInfo(t, target)
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = successfulDaemonArtifactClient(binary, sha)

	resp, err := lifecycle.Update(context.Background(), &DaemonUpdateRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  installDir,
	})

	require.NoError(t, err)
	assert.Equal(t, "update", resp.Action)
	after := requireFileInfo(t, target)
	assert.False(t, os.SameFile(before, after))
	raw, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, binary, raw)
}

func TestInstallDaemonBinaryExplainsUnwritableInstallDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions required")
	}
	installDir := t.TempDir()
	require.NoError(t, os.Chmod(installDir, 0o555))
	defer func() {
		_ = os.Chmod(installDir, 0o755)
	}()

	err := installDaemonBinary(filepath.Join(installDir, "paxd"), []byte("new-paxd"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not writable")
	assert.Contains(t, err.Error(), "--install-dir")
	assert.Contains(t, err.Error(), "sudo")
}

func TestDaemonLifecycleCheckResolvesLatestPaxdArtifact(t *testing.T) {
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "/api/v1/public/paxd/download", r.URL.Path)
		assert.Equal(t, "linux/amd64", r.URL.Query().Get("platform"))
		assert.Equal(t, "stable", r.URL.Query().Get("tags"))
		return jsonResponse(`{
			"data": {
				"url": "gs://pax-tech-bucket/paxd/releases/0.2.0/paxd",
				"sha256": "abc123",
				"size_bytes": 42,
				"version": "0.2.0",
				"product": "paxd",
				"platform": "linux/amd64"
			}
		}`), nil
	})
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = client

	resp, err := lifecycle.Check(context.Background(), &DaemonUpdateCheckRequest{
		ResolverURL: "https://resolver.test/api/v1/public/paxd/download",
		Platform:    "linux/amd64",
		Tag:         "stable",
	})

	require.NoError(t, err)
	assert.Equal(t, "paxd", resp.Binary)
	assert.Equal(t, "0.2.0", resp.Version)
	assert.Equal(t, "linux/amd64", resp.Platform)
	assert.Equal(t, "abc123", resp.SHA256)
	assert.Equal(t, int64(42), resp.SizeBytes)
	assert.Equal(
		t,
		"https://storage.googleapis.com/pax-tech-bucket/paxd/releases/0.2.0/paxd",
		resp.DownloadURL,
	)
	assert.Contains(t, resp.Message, "Latest paxd 0.2.0")
}

func requireFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info
}

func TestDaemonLifecycleCheckUsesDefaultsWhenRequestIsNil(t *testing.T) {
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "/api/v1/public/paxd/download", r.URL.Path)
		assert.NotEmpty(t, r.URL.Query().Get("platform"))
		assert.Equal(t, DefaultUpdateTag, r.URL.Query().Get("tags"))
		return jsonResponse(`{
			"data": {
				"url": "https://download.test/paxd",
				"sha256": "abc123",
				"size_bytes": 42,
				"version": "0.2.0",
				"product": "paxd"
			}
		}`), nil
	})
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = client

	resp, err := lifecycle.Check(context.Background(), nil)

	require.NoError(t, err)
	assert.Equal(t, "0.2.0", resp.Version)
	assert.Equal(t, "https://download.test/paxd", resp.DownloadURL)
}

func TestValidateDaemonArtifactRejectsWrongProduct(t *testing.T) {
	err := validateDaemonArtifact(&updateArtifact{
		Product: "paxl",
		Version: "0.2.0",
		URL:     "https://download.test/paxd",
		SHA256:  "abc123",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not paxd")
}

func TestValidateDaemonArtifactRejectsNilArtifact(t *testing.T) {
	err := validateDaemonArtifact(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact is required")
}

func TestValidateDaemonArtifactRejectsMissingRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name     string
		artifact *updateArtifact
		want     string
	}{
		{
			name:     "version",
			artifact: &updateArtifact{URL: "https://download.test/paxd", SHA256: "abc123"},
			want:     "version",
		},
		{
			name:     "url",
			artifact: &updateArtifact{Version: "0.2.0", SHA256: "abc123"},
			want:     "download URL",
		},
		{
			name:     "sha",
			artifact: &updateArtifact{Version: "0.2.0", URL: "https://download.test/paxd"},
			want:     "sha256",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDaemonArtifact(tc.artifact)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDaemonLifecycleInstallDirUsesOverride(t *testing.T) {
	got, err := NewDaemonLifecycleFacade(nil).daemonInstallDir(" /opt/pax/bin ")

	require.NoError(t, err)
	assert.Equal(t, "/opt/pax/bin", got)
}

func TestDaemonLifecycleInstallDirFallsBackToUserLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := NewDaemonLifecycleFacade(&fakeDaemonLifecycleRunner{}).daemonInstallDir("")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "bin"), got)
}

func TestDaemonLifecycleInstallUsesExistingPaxdDirectoryWhenInstallDirIsEmpty(t *testing.T) {
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	installDir := t.TempDir()
	runner := &fakeDaemonLifecycleRunner{path: filepath.Join(installDir, "paxd")}
	lifecycle := NewDaemonLifecycleFacade(runner)
	lifecycle.client = successfulDaemonArtifactClient(binary, sha)

	resp, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
	})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(installDir, "paxd"), resp.Path)
	raw, err := os.ReadFile(resp.Path)
	require.NoError(t, err)
	assert.Equal(t, binary, raw)
}

func TestDaemonLifecycleInstallRejectsDownloadSizeMismatch(t *testing.T) {
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/resolve":
			return jsonResponse(fmt.Sprintf(
				`{"data":{"url":"https://download.test/download/paxd","sha256":"%s","size_bytes":%d,"version":"0.2.0"}}`,
				sha,
				len(binary)+1,
			)), nil
		case "/download/paxd":
			return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser(binary)}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: ioNopCloser(nil)}, nil
		}
	})
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = client

	_, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  t.TempDir(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "download size")
}

func TestDaemonLifecycleInstallReturnsResolverHTTPError(t *testing.T) {
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: ioNopCloser(nil)}, nil
	})

	_, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  t.TempDir(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolver returned HTTP 502")
}

func TestDaemonLifecycleInstallReturnsDownloadHTTPError(t *testing.T) {
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/resolve":
			return jsonResponse(
				`{"data":{"url":"https://download.test/download/paxd","sha256":"abc123","size_bytes":1,"version":"0.2.0"}}`,
			), nil
		case "/download/paxd":
			return &http.Response{StatusCode: http.StatusBadGateway, Body: ioNopCloser(nil)}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: ioNopCloser(nil)}, nil
		}
	})

	_, err := lifecycle.Install(context.Background(), &DaemonInstallRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  t.TempDir(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "download returned HTTP 502")
}

func TestDaemonLifecycleResolveRejectsBadResolverURL(t *testing.T) {
	_, err := NewDaemonLifecycleFacade(nil).resolveDaemonArtifact(
		context.Background(),
		"://bad",
		"linux/amd64",
		"stable",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse resolver URL")
}

func TestDaemonLifecycleResolveReturnsRequestError(t *testing.T) {
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})

	_, err := lifecycle.resolveDaemonArtifact(
		context.Background(),
		"https://resolver.test/api/v1/public/paxd/download",
		"linux/amd64",
		"stable",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "request resolver")
	assert.Contains(t, err.Error(), "network unavailable")
}

func TestDaemonLifecycleResolveRejectsMalformedJSON(t *testing.T) {
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       ioNopCloser([]byte("not-json")),
		}, nil
	})

	_, err := lifecycle.resolveDaemonArtifact(
		context.Background(),
		"https://resolver.test/api/v1/public/paxd/download",
		"linux/amd64",
		"stable",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode resolver response")
}

func TestDaemonLifecycleDownloadReturnsRequestError(t *testing.T) {
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})

	_, err := lifecycle.downloadDaemonArtifact(context.Background(), &updateArtifact{
		URL: "https://download.test/paxd",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "request download")
	assert.Contains(t, err.Error(), "network unavailable")
}

func TestDaemonLifecycleDownloadUsesDefaultLimitWhenSizeMissing(t *testing.T) {
	binary := []byte("paxd")
	lifecycle := NewDaemonLifecycleFacade(nil)
	lifecycle.client = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser(binary)}, nil
	})

	got, err := lifecycle.downloadDaemonArtifact(context.Background(), &updateArtifact{
		URL: "https://download.test/paxd",
	})

	require.NoError(t, err)
	assert.Equal(t, binary, got)
}

func TestDaemonLifecycleSetupRunsPaxdSetupWithCloudURL(t *testing.T) {
	runner := &fakeDaemonLifecycleRunner{path: "/usr/local/bin/paxd"}

	resp, err := NewDaemonLifecycleFacade(runner).Setup(context.Background(), &DaemonSetupRequest{
		CloudURL: "https://api.example.com/",
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusInstalled, resp.Status)
	assert.Equal(t, "/usr/local/bin/paxd", runner.name)
	assert.Equal(t, []string{"setup", "--cloud-url", "https://api.example.com"}, runner.args)
}

func TestDaemonLifecycleSetupInstallsPaxdWhenMissing(t *testing.T) {
	binary := []byte("fake-paxd-binary")
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	installDir := t.TempDir()
	runner := &fakeDaemonLifecycleRunner{}
	lifecycle := NewDaemonLifecycleFacade(runner)
	lifecycle.client = successfulDaemonArtifactClient(binary, sha)

	resp, err := lifecycle.Setup(context.Background(), &DaemonSetupRequest{
		CloudURL:    "https://api.example.com",
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  installDir,
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusInstalled, resp.Status)
	assert.Equal(t, filepath.Join(installDir, "paxd"), resp.Path)
	assert.Equal(t, filepath.Join(installDir, "paxd"), runner.name)
	assert.Equal(t, []string{"setup", "--cloud-url", "https://api.example.com"}, runner.args)
	raw, err := os.ReadFile(filepath.Join(installDir, "paxd"))
	require.NoError(t, err)
	assert.Equal(t, binary, raw)
}

func TestDaemonLifecycleSetupReturnsInstallErrorWhenAutoInstallFails(t *testing.T) {
	lifecycle := NewDaemonLifecycleFacade(&fakeDaemonLifecycleRunner{})
	lifecycle.client = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: ioNopCloser(nil)}, nil
	})

	_, err := lifecycle.Setup(context.Background(), &DaemonSetupRequest{
		ResolverURL: "https://resolver.test/resolve",
		Platform:    "linux/amd64",
		InstallDir:  t.TempDir(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "install paxd before setup")
}

func TestDaemonLifecycleServiceRunsPaxdServiceAction(t *testing.T) {
	runner := &fakeDaemonLifecycleRunner{path: "/usr/local/bin/paxd"}

	resp, err := NewDaemonLifecycleFacade(
		runner,
	).Service(context.Background(), &DaemonServiceRequest{
		Action: "restart",
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusInstalled, resp.Status)
	assert.Equal(t, "/usr/local/bin/paxd", runner.name)
	assert.Equal(t, []string{"service", "restart"}, runner.args)
}

func TestDaemonLifecycleServiceDryRunReportsAction(t *testing.T) {
	resp, err := NewDaemonLifecycleFacade(nil).Service(context.Background(), &DaemonServiceRequest{
		Action: "restart",
		DryRun: true,
	})

	require.NoError(t, err)
	assert.Equal(t, SetupStatusPending, resp.Status)
	assert.Equal(t, "Would run paxd service restart.", resp.Message)
}

func TestDaemonLifecycleServiceRejectsMissingAction(t *testing.T) {
	_, err := NewDaemonLifecycleFacade(nil).Service(context.Background(), &DaemonServiceRequest{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "action is required")
}

func TestDefaultDaemonLifecycleRunnerExecutesCommands(t *testing.T) {
	runner := defaultDaemonLifecycleRunner{}
	path, err := runner.LookPath("sh")
	require.NoError(t, err)

	err = runner.Run(context.Background(), path, []string{"-c", "exit 0"})

	require.NoError(t, err)
}

func TestDefaultDaemonLifecycleRunnerInheritsCloudflareEnv(t *testing.T) {
	t.Setenv("PAX_CLOUD_CF_CLIENT_ID", "cf-client")
	runner := defaultDaemonLifecycleRunner{}
	path, err := runner.LookPath("sh")
	require.NoError(t, err)

	err = runner.Run(
		context.Background(),
		path,
		[]string{"-c", `test "$PAX_CLOUD_CF_CLIENT_ID" = "cf-client"`},
	)

	require.NoError(t, err)
}

func TestDefaultDaemonLifecycleRunnerDisplaysChildProcessIO(t *testing.T) {
	runner := defaultDaemonLifecycleRunner{}
	path, err := runner.LookPath("sh")
	require.NoError(t, err)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	originalStdin := daemonCommandStdin
	originalStdout := daemonCommandStdout
	originalStderr := daemonCommandStderr
	daemonCommandStdin = strings.NewReader("pair-code\n")
	daemonCommandStdout = &stdout
	daemonCommandStderr = &stderr
	defer func() {
		daemonCommandStdin = originalStdin
		daemonCommandStdout = originalStdout
		daemonCommandStderr = originalStderr
	}()

	err = runner.Run(
		context.Background(),
		path,
		[]string{"-c", `read code; echo "stdout:$code"; echo "stderr:$code" >&2`},
	)

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "stdout:pair-code")
	assert.Contains(t, stderr.String(), "stderr:pair-code")
}

func ioNopCloser(raw []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(raw))
}

func successfulDaemonArtifactClient(binary []byte, sha string) UpdateHTTPClient {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/resolve":
			return jsonResponse(fmt.Sprintf(
				`{"data":{"url":"https://download.test/download/paxd","sha256":"%s","size_bytes":%d,"version":"0.2.0"}}`,
				sha,
				len(binary),
			)), nil
		case "/download/paxd":
			return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser(binary)}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: ioNopCloser(nil)}, nil
		}
	})
}

type fakeDaemonLifecycleRunner struct {
	path string
	name string
	args []string
}

func (r *fakeDaemonLifecycleRunner) LookPath(_ string) (string, error) {
	if r.path == "" {
		return "", os.ErrNotExist
	}
	return r.path, nil
}

func (r *fakeDaemonLifecycleRunner) Run(_ context.Context, name string, args []string) error {
	r.name = name
	r.args = append([]string(nil), args...)
	return nil
}
