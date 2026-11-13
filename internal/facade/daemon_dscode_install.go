package facade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Pin the reviewed installer, while letting it resolve the newest complete release.
// The official installer verifies archive checksums and refuses foreign command links.
const dscodeInstallerURL = "https://raw.githubusercontent.com/qiz029/dscode/77db18bcdeb2fc19327f6f60810e249dc4eb47ca/install.sh"

const dscodeInstallGuidance = "Installs the official self-contained package with its bundled runtime; no system Node or npm is required. Add ~/.local/bin to the paxd service PATH. Configure providers with /login in dscode, then run paxl daemon harness discover dscode. Legacy npm/Hub data requires explicit migration using DSCODE_HOME; existing foreign command links are not replaced."

func (f *DaemonLifecycleFacade) installDSCode(
	ctx context.Context,
	dryRun bool,
	opts ...func(*Option),
) (*DaemonLifecycleResponse, error) {
	resp := &DaemonLifecycleResponse{
		Binary:  "dscode",
		Action:  "install-harness",
		Status:  SetupStatusPending,
		Message: "Would download the reviewed official DSCODE installer and install the latest self-contained release on this machine. " + dscodeInstallGuidance,
	}
	if dryRun {
		return resp, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dscodeInstallerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prepare DSCODE installer: %w", err)
	}
	download, err := NewArtifactHTTPClient(f.client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("download DSCODE installer: %w", SanitizeArtifactHTTPError(err))
	}
	defer func() { _ = download.Body.Close() }()
	if download.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download DSCODE installer: HTTP %d", download.StatusCode)
	}
	script, err := io.ReadAll(io.LimitReader(download.Body, 128*1024+1))
	if err != nil {
		return nil, fmt.Errorf("read DSCODE installer: %w", err)
	}
	if len(script) == 0 || len(script) > 128*1024 {
		return nil, fmt.Errorf("invalid DSCODE installer size")
	}
	dir, err := os.MkdirTemp("", "paxl-dscode-install-")
	if err != nil {
		return nil, fmt.Errorf("stage DSCODE installer: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, script, 0600); err != nil {
		return nil, fmt.Errorf("write DSCODE installer: %w", err)
	}
	verbosef(applyOptions(opts), "Installing the official self-contained DSCODE package.")
	if err := f.runner.Run(ctx, "/bin/sh", []string{path, "latest"}); err != nil {
		return nil, fmt.Errorf("install DSCODE: %w", err)
	}
	resp.Status = SetupStatusInstalled
	resp.Message = "DSCODE self-contained installation completed. " + dscodeInstallGuidance
	return resp, nil
}
