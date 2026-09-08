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
	if req == nil || strings.TrimSpace(req.Harness) != "dsh" {
		return nil, fmt.Errorf("harness installation currently supports dsh only")
	}
	option := applyOptions(opts)
	const guidance = "Requires Node 22.19+ on 22.x or Node 24+. Configure DEEPSEEK_API_KEY in the paxd service environment, then run paxl daemon harness discover dsh."
	resp := &DaemonLifecycleResponse{
		Binary: "dsh", Action: "install-harness", Status: SetupStatusPending,
		Message: "Would run npm install -g @deepseek-ai/dsh@latest on this machine. " + guidance,
	}
	if req.DryRun {
		return resp, nil
	}
	npm, err := f.runner.LookPath("npm")
	if err != nil {
		return nil, fmt.Errorf("find npm for DSH installation: %w", err)
	}
	verbosef(option, "Installing DeepSeek Harness on this machine.")
	args := []string{"install", "-g", "@deepseek-ai/dsh@latest"}
	if err := f.runner.Run(ctx, npm, args); err != nil {
		return nil, fmt.Errorf("install DeepSeek Harness: %w", err)
	}
	resp.Status = SetupStatusInstalled
	resp.Message = "DeepSeek Harness installation completed. " + guidance
	return resp, nil
}
