package facade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type DaemonLifecycleRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args []string) error
}

type DaemonLifecycleFacade struct {
	runner DaemonLifecycleRunner
	client UpdateHTTPClient
}

var (
	daemonCommandStdin  io.Reader = os.Stdin
	daemonCommandStdout io.Writer = os.Stdout
	daemonCommandStderr io.Writer = os.Stderr
)

const DefaultDaemonResolverURL = "https://api.paxtech.net/api/v1/public/paxd/download"

type DaemonInstallRequest struct {
	DryRun      bool
	ResolverURL string
	Platform    string
	Tag         string
	InstallDir  string
	BinaryName  string
}

type DaemonUpdateRequest = DaemonInstallRequest

type DaemonUpdateCheckRequest struct {
	ResolverURL string
	Platform    string
	Tag         string
}

type DaemonSetupRequest struct {
	DryRun      bool
	CloudURL    string
	ResolverURL string
	Platform    string
	Tag         string
	InstallDir  string
	BinaryName  string
}

type DaemonServiceRequest struct {
	Action string
	DryRun bool
}

type DaemonLifecycleResponse struct {
	Status  SetupStatus
	Binary  string
	Path    string
	Action  string
	Message string
}

type DaemonUpdateCheckResponse struct {
	Binary      string `json:"binary"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	Action      string `json:"action"`
	Message     string `json:"message"`
}

type defaultDaemonLifecycleRunner struct{}

func NewDaemonLifecycleFacade(runner DaemonLifecycleRunner) *DaemonLifecycleFacade {
	if runner == nil {
		runner = defaultDaemonLifecycleRunner{}
	}
	return &DaemonLifecycleFacade{runner: runner, client: http.DefaultClient}
}

func (f *DaemonLifecycleFacade) Install(
	ctx context.Context,
	req *DaemonInstallRequest,
	opts ...func(*Option),
) (*DaemonLifecycleResponse, error) {
	_ = ctx
	_ = applyOptions(opts)
	if req == nil {
		req = &DaemonInstallRequest{}
	}
	if req.DryRun {
		return &DaemonLifecycleResponse{
			Status:  SetupStatusPending,
			Binary:  daemonBinaryName(req.BinaryName),
			Action:  "install",
			Message: "Would install paxd.",
		}, nil
	}
	platform := firstNonEmpty(strings.TrimSpace(req.Platform), currentPlatform())
	artifact, err := f.resolveDaemonArtifact(
		ctx,
		firstNonEmpty(strings.TrimSpace(req.ResolverURL), DefaultDaemonResolverURL),
		platform,
		firstNonEmpty(strings.TrimSpace(req.Tag), DefaultUpdateTag),
	)
	if err != nil {
		return nil, fmt.Errorf("resolve paxd artifact: %w", err)
	}
	binary, err := f.downloadDaemonArtifact(ctx, artifact)
	if err != nil {
		return nil, fmt.Errorf("download paxd artifact: %w", err)
	}
	if err := verifyDaemonArtifact(binary, artifact.SHA256); err != nil {
		return nil, err
	}
	installDir, err := f.daemonInstallDir(req.InstallDir)
	if err != nil {
		return nil, err
	}
	binaryName := daemonBinaryName(req.BinaryName)
	path := filepath.Join(installDir, binaryName)
	// #nosec G301 -- bin dirs must be searchable.
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return nil, fmt.Errorf("create install dir: %w", err)
	}
	if err := installDaemonBinary(path, binary); err != nil {
		return nil, err
	}
	return &DaemonLifecycleResponse{
		Status:  SetupStatusInstalled,
		Binary:  binaryName,
		Path:    path,
		Action:  "install",
		Message: fmt.Sprintf("paxd %s installed.", artifact.Version),
	}, nil
}

func installDaemonBinary(path string, binary []byte) error {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp paxd binary: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp paxd binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp paxd binary: %w", err)
	}
	// #nosec G302 -- paxd must be executable.
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod temp paxd binary: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace paxd binary: %w", err)
	}
	removeTemp = false
	return nil
}

func (f *DaemonLifecycleFacade) Update(
	ctx context.Context,
	req *DaemonUpdateRequest,
	opts ...func(*Option),
) (*DaemonLifecycleResponse, error) {
	resp, err := f.Install(ctx, req, opts...)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		resp.Action = "update"
		if resp.Status == SetupStatusPending {
			resp.Message = "Would update paxd."
		} else {
			resp.Message = strings.Replace(resp.Message, "installed", "updated", 1)
		}
	}
	return resp, nil
}

func (f *DaemonLifecycleFacade) Check(
	ctx context.Context,
	req *DaemonUpdateCheckRequest,
	opts ...func(*Option),
) (*DaemonUpdateCheckResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &DaemonUpdateCheckRequest{}
	}
	platform := firstNonEmpty(strings.TrimSpace(req.Platform), currentPlatform())
	artifact, err := f.resolveDaemonArtifact(
		ctx,
		firstNonEmpty(strings.TrimSpace(req.ResolverURL), DefaultDaemonResolverURL),
		platform,
		firstNonEmpty(strings.TrimSpace(req.Tag), DefaultUpdateTag),
	)
	if err != nil {
		return nil, fmt.Errorf("resolve paxd artifact: %w", err)
	}
	return &DaemonUpdateCheckResponse{
		Binary:      "paxd",
		Version:     artifact.Version,
		Platform:    platform,
		DownloadURL: artifact.URL,
		SHA256:      artifact.SHA256,
		SizeBytes:   artifact.Size,
		Action:      "update check",
		Message:     fmt.Sprintf("Latest paxd %s is available for %s.", artifact.Version, platform),
	}, nil
}

func (f *DaemonLifecycleFacade) Setup(
	ctx context.Context,
	req *DaemonSetupRequest,
	opts ...func(*Option),
) (*DaemonLifecycleResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &DaemonSetupRequest{}
	}
	if req.DryRun {
		return &DaemonLifecycleResponse{
			Status:  SetupStatusPending,
			Binary:  "paxd",
			Action:  "setup",
			Message: "Would set up paxd and start the background service.",
		}, nil
	}
	path, err := f.runner.LookPath("paxd")
	if err != nil {
		installed, installErr := f.Install(ctx, &DaemonInstallRequest{
			ResolverURL: req.ResolverURL,
			Platform:    req.Platform,
			Tag:         req.Tag,
			InstallDir:  req.InstallDir,
			BinaryName:  req.BinaryName,
		})
		if installErr != nil {
			return nil, fmt.Errorf("install paxd before setup: %w", installErr)
		}
		path = installed.Path
	}
	args := []string{"setup"}
	if strings.TrimSpace(req.CloudURL) != "" {
		args = append(args, "--cloud-url", strings.TrimRight(strings.TrimSpace(req.CloudURL), "/"))
	}
	if err := f.runner.Run(ctx, path, args); err != nil {
		return nil, fmt.Errorf("run paxd setup: %w", err)
	}
	return &DaemonLifecycleResponse{
		Status:  SetupStatusInstalled,
		Binary:  "paxd",
		Path:    path,
		Action:  "setup",
		Message: "paxd setup completed.",
	}, nil
}

func (f *DaemonLifecycleFacade) Service(
	ctx context.Context,
	req *DaemonServiceRequest,
	opts ...func(*Option),
) (*DaemonLifecycleResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("daemon service: request is required")
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		return nil, fmt.Errorf("daemon service: action is required")
	}
	if req.DryRun {
		return &DaemonLifecycleResponse{
			Status:  SetupStatusPending,
			Binary:  "paxd",
			Action:  "service " + action,
			Message: "Would run paxd service " + action + ".",
		}, nil
	}
	path, err := f.runner.LookPath("paxd")
	if err != nil {
		return nil, fmt.Errorf(
			"control paxd service: paxd is not installed; run `paxl daemon install` first",
		)
	}
	if err := f.runner.Run(ctx, path, []string{"service", action}); err != nil {
		return nil, fmt.Errorf("run paxd service %s: %w", action, err)
	}
	return &DaemonLifecycleResponse{
		Status:  SetupStatusInstalled,
		Binary:  "paxd",
		Path:    path,
		Action:  "service " + action,
		Message: "paxd service " + action + " completed.",
	}, nil
}

func (defaultDaemonLifecycleRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (defaultDaemonLifecycleRunner) Run(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- paxl intentionally runs paxd.
	cmd.Stdin = daemonCommandStdin
	cmd.Stdout = daemonCommandStdout
	cmd.Stderr = daemonCommandStderr
	return cmd.Run()
}

func (f *DaemonLifecycleFacade) resolveDaemonArtifact(
	ctx context.Context,
	resolverURL string,
	platform string,
	tag string,
) (*updateArtifact, error) {
	endpoint, err := url.Parse(resolverURL)
	if err != nil {
		return nil, fmt.Errorf("parse resolver URL: %w", err)
	}
	query := endpoint.Query()
	if strings.Contains(endpoint.Path, "/artifacts/") && query.Get("product") == "" {
		query.Set("product", "paxd")
	}
	query.Set("platform", platform)
	query.Set("tags", tag)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	) // #nosec G107
	if err != nil {
		return nil, fmt.Errorf("create resolver request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "paxl-daemon-install")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request resolver: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("resolver returned HTTP %d", resp.StatusCode)
	}
	var resolverResp updateResolverResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&resolverResp); err != nil {
		return nil, fmt.Errorf("decode resolver response: %w", err)
	}
	artifact := resolverResp.Data.toArtifact()
	if err := validateDaemonArtifact(artifact); err != nil {
		return nil, err
	}
	artifact.URL = normalizeArtifactURL(artifact.URL)
	return artifact, nil
}

func (f *DaemonLifecycleFacade) downloadDaemonArtifact(
	ctx context.Context,
	artifact *updateArtifact,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil) // #nosec G107
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "paxl-daemon-install")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request download: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	limit := artifact.Size + 1
	if limit <= 1 {
		limit = 256 << 20
	}
	binary, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read download: %w", err)
	}
	if artifact.Size > 0 && int64(len(binary)) != artifact.Size {
		return nil, fmt.Errorf(
			"download size %d does not match expected %d",
			len(binary),
			artifact.Size,
		)
	}
	return binary, nil
}

func verifyDaemonArtifact(binary []byte, expectedSHA string) error {
	sum := sha256.Sum256(binary)
	got := hex.EncodeToString(sum[:])
	if got != strings.TrimSpace(expectedSHA) {
		return fmt.Errorf("paxd sha256 %s does not match expected %s", got, expectedSHA)
	}
	return nil
}

func validateDaemonArtifact(artifact *updateArtifact) error {
	if artifact == nil {
		return fmt.Errorf("resolver artifact is required")
	}
	if artifact.Product != "" && artifact.Product != "paxd" {
		return fmt.Errorf("resolver product %q is not paxd", artifact.Product)
	}
	if strings.TrimSpace(artifact.Version) == "" {
		return fmt.Errorf("resolver version is required")
	}
	if strings.TrimSpace(artifact.URL) == "" {
		return fmt.Errorf("resolver download URL is required")
	}
	if strings.TrimSpace(artifact.SHA256) == "" {
		return fmt.Errorf("resolver sha256 is required")
	}
	return nil
}

func (f *DaemonLifecycleFacade) daemonInstallDir(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override), nil
	}
	path, err := f.runner.LookPath("paxd")
	if err == nil && strings.TrimSpace(path) != "" {
		return filepath.Dir(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("choose paxd install dir: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func daemonBinaryName(override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return "paxd"
}
