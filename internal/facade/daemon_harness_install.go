package facade

import (
	"context"
	"fmt"
	"strings"
)

type DaemonHarnessInstallRequest struct {
	Harness string
	DryRun  bool
}

// InstallHarness installs on the CLI host; it never sends an install request to paxd.
func (f *DaemonLifecycleFacade) InstallHarness(
	ctx context.Context,
	req *DaemonHarnessInstallRequest,
	opts ...func(*Option),
) (*DaemonLifecycleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("harness installation request is required")
	}
	harness := strings.TrimSpace(req.Harness)
	packageName, displayName := "@deepseek-ai/dsh@latest", "DeepSeek Harness"
	if harness == "dscode" {
		packageName, displayName = "@toddzheng024/dscode@latest", "DSCODE"
	} else if harness != "dsh" {
		return nil, fmt.Errorf("harness installation currently supports dsh and dscode only")
	}
	option := applyOptions(opts)
	guidance := "Requires Node 22.19+ on 22.x or Node 24+. Configure DEEPSEEK_API_KEY in the paxd service environment, then run paxl daemon harness discover dsh."
	if harness == "dscode" {
		guidance = "Requires Node 22.19+ on 22.x or Node 24+. Use a DSCODE build with native dscode acp support; run dscode install to initialize its profile. Configure credentials with /login inside dscode or DEEPSEEK_API_KEY in the paxd service environment, then run paxl daemon harness discover dscode."
	}
	resp := &DaemonLifecycleResponse{
		Binary: harness, Action: "install-harness", Status: SetupStatusPending,
		Message: "Would run npm install -g " + packageName + " on this machine. " + guidance,
	}
	if req.DryRun {
		return resp, nil
	}
	npm, err := f.runner.LookPath("npm")
	if err != nil {
		return nil, fmt.Errorf("find npm for %s installation: %w", harness, err)
	}
	verbosef(option, "Installing %s on this machine.", displayName)
	args := []string{"install", "-g", packageName}
	if err := f.runner.Run(ctx, npm, args); err != nil {
		return nil, fmt.Errorf("install %s: %w", displayName, err)
	}
	resp.Status = SetupStatusInstalled
	resp.Message = displayName + " installation completed. " + guidance
	return resp, nil
}
