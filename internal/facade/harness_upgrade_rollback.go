package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type HarnessRollbackRequest struct {
	HarnessInspectRequest
	RollbackID string
}

func (f *HarnessUpgradeFacade) Rollback(
	ctx context.Context,
	req *HarnessRollbackRequest,
	opts ...func(*Option),
) (*HarnessInstallation, error) {
	if req == nil || req.RollbackID == "" || req.RollbackID == "." || req.RollbackID == ".." ||
		filepath.Base(req.RollbackID) != req.RollbackID {
		return nil, fmt.Errorf("a valid rollback identifier is required")
	}
	if _, _, err := harnessPackageName(&req.HarnessInspectRequest); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(req.Path) {
		return nil, fmt.Errorf("rollback requires an absolute launcher path")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(req.Path))
	if err != nil {
		return nil, fmt.Errorf("resolve launcher directory: %w", err)
	}
	unlock, err := LockExecutableUpdate(filepath.Join(parent, filepath.Base(req.Path)))
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := os.ReadFile(
		filepath.Join(parent, ".paxl-harness-versions", req.RollbackID, "rollback.json"),
	)
	if err != nil {
		return nil, fmt.Errorf("read harness rollback: %w", err)
	}
	var record harnessRollback
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("decode harness rollback: %w", err)
	}
	if record.Path != req.Path {
		return nil, fmt.Errorf("rollback belongs to another launcher")
	}
	current, err := filepath.EvalSymlinks(req.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve current launcher: %w", err)
	}
	if current != record.After && current != record.Before {
		return nil, fmt.Errorf("launcher changed since this upgrade")
	}
	previousReq := req.HarnessInspectRequest
	previousReq.Path = record.Before
	previous, err := f.Inspect(ctx, &previousReq, opts...)
	if err != nil {
		return nil, fmt.Errorf("verify rollback target: %w", err)
	}
	if err := verifyHarnessExecutable(ctx, previous); err != nil {
		return nil, err
	}
	if err := switchHarnessLauncher(req.Path, record.Before); err != nil {
		return nil, err
	}
	return f.Inspect(ctx, &req.HarnessInspectRequest, opts...)
}
