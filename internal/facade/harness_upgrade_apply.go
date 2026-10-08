package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type HarnessUpgradeRequest struct {
	HarnessInspectRequest
	Version string
	DryRun  bool
}

type HarnessUpgradeResponse struct {
	Installation  *HarnessInstallation `json:"installation"`
	TargetVersion string               `json:"target_version"`
	Phase         string               `json:"phase"`
	RollbackID    string               `json:"rollback_id,omitempty"`
}

type harnessRollback struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

var harnessVersionPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`,
)

// Upgrade retains the old package tree so running processes can continue reading it.
// ACP process verification belongs to the daemon; installed is not a running-version claim.
func (f *HarnessUpgradeFacade) Upgrade(
	ctx context.Context,
	req *HarnessUpgradeRequest,
	opts ...func(*Option),
) (*HarnessUpgradeResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("harness upgrade request is required")
	}
	if req.Version != "" && req.Version != "latest" &&
		!harnessVersionPattern.MatchString(req.Version) {
		return nil, fmt.Errorf("an exact semantic target version is required")
	}
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("unsupported installation: Windows launcher activation")
	}
	before, err := f.Inspect(ctx, &req.HarnessInspectRequest, opts...)
	if err != nil {
		return nil, err
	}
	if err := requireHarnessSymlink(before.Path); err != nil {
		return nil, err
	}
	if req.Version == "" || req.Version == "latest" {
		version, err := resolveLatestHarnessVersion(ctx, before.Package)
		if err != nil {
			return nil, err
		}
		resolved := *req
		resolved.Version = version
		req = &resolved
		verbosef(applyOptions(opts), "Resolved latest %s to %s.", before.Package, version)
	}
	resp := &HarnessUpgradeResponse{
		Installation:  before,
		TargetVersion: req.Version,
		Phase:         "planned",
	}
	if req.DryRun {
		return resp, nil
	}
	// Canonicalize only the parent: locking the target would change the lock after activation.
	parent, err := filepath.EvalSymlinks(filepath.Dir(before.Path))
	if err != nil {
		return nil, fmt.Errorf("resolve launcher directory: %w", err)
	}
	unlock, err := LockExecutableUpdate(filepath.Join(parent, filepath.Base(before.Path)))
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := f.Inspect(ctx, &req.HarnessInspectRequest, opts...)
	if err != nil {
		return nil, err
	}
	if current.ResolvedPath != before.ResolvedPath || current.Version != before.Version {
		return nil, fmt.Errorf("harness installation changed before upgrade")
	}
	if before.Version == req.Version {
		if err := verifyHarnessExecutable(ctx, before); err != nil {
			return nil, err
		}
		resp.Phase = "installed"
		return resp, nil
	}
	return f.stageHarnessUpgrade(ctx, req, before, opts...)
}

func requireHarnessSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect launcher: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("unsupported installation: launcher must be a symbolic link")
	}
	return nil
}

func (f *HarnessUpgradeFacade) stageHarnessUpgrade(
	ctx context.Context,
	req *HarnessUpgradeRequest,
	before *HarnessInstallation,
	opts ...func(*Option),
) (*HarnessUpgradeResponse, error) {
	base := filepath.Join(filepath.Dir(before.Path), ".paxl-harness-versions")
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, fmt.Errorf("create harness staging directory: %w", err)
	}
	stage, err := os.MkdirTemp(base, req.Harness+"-"+string(req.Component)+"-"+req.Version+"-")
	if err != nil {
		return nil, fmt.Errorf("stage harness installation: %w", err)
	}
	retain := false
	defer func() {
		if !retain {
			_ = os.RemoveAll(stage)
		}
	}()
	verbosef(applyOptions(opts), "Staging %s version %s.", before.Package, req.Version)
	_, err = runHarnessProcess(
		ctx,
		4*time.Minute,
		"npm",
		"install",
		"--global",
		"--prefix",
		stage,
		"--no-audit",
		"--no-fund",
		"--",
		before.Package+"@"+req.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("install harness package: %w", err)
	}
	_, bin, _ := harnessPackageName(&req.HarnessInspectRequest)
	inspection := req.HarnessInspectRequest
	inspection.Path = filepath.Join(stage, "bin", bin)
	next, err := f.Inspect(ctx, &inspection, opts...)
	if err != nil {
		return nil, fmt.Errorf("verify staged harness: %w", err)
	}
	if next.Version != req.Version {
		return nil, fmt.Errorf(
			"staged harness version %q does not match %q",
			next.Version,
			req.Version,
		)
	}
	if err := verifyHarnessExecutable(ctx, next); err != nil {
		return nil, err
	}
	current, err := f.Inspect(ctx, &req.HarnessInspectRequest, opts...)
	if err != nil || current.ResolvedPath != before.ResolvedPath ||
		current.Version != before.Version {
		return nil, fmt.Errorf("harness launcher changed while preparing upgrade")
	}
	record := &harnessRollback{
		Path:   before.Path,
		Before: before.ResolvedPath,
		After:  next.ResolvedPath,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode harness rollback: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "rollback.json"), data, 0600); err != nil {
		return nil, fmt.Errorf("record harness rollback: %w", err)
	}
	if err := switchHarnessLauncher(before.Path, next.ResolvedPath); err != nil {
		return nil, err
	}
	// Keep rollback data even if post-activation verification or recovery fails.
	retain = true
	after, err := f.Inspect(ctx, &req.HarnessInspectRequest, opts...)
	if err == nil {
		err = verifyHarnessExecutable(ctx, after)
	}
	if err != nil {
		restoreErr := switchHarnessLauncher(before.Path, before.ResolvedPath)
		if restoreErr != nil {
			return nil, fmt.Errorf(
				"verify activated harness: %w; restore launcher: %w",
				err,
				restoreErr,
			)
		}
		return nil, fmt.Errorf("verify activated harness (previous launcher restored): %w", err)
	}
	return &HarnessUpgradeResponse{
		Installation:  after,
		TargetVersion: req.Version,
		Phase:         "installed",
		RollbackID:    filepath.Base(stage),
	}, nil
}

func switchHarnessLauncher(path, target string) error {
	dir, err := os.MkdirTemp(filepath.Dir(path), ".paxl-activate-")
	if err != nil {
		return fmt.Errorf("prepare launcher activation: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	next := filepath.Join(dir, "launcher")
	if err := os.Symlink(target, next); err != nil {
		return fmt.Errorf("prepare launcher link: %w", err)
	}
	if err := os.Rename(next, path); err != nil {
		return fmt.Errorf("activate harness launcher: %w", err)
	}
	return nil
}

func verifyHarnessExecutable(ctx context.Context, installed *HarnessInstallation) error {
	if installed.Harness == "pi" && installed.Component == HarnessComponentACP {
		// Pi has no --version flag. Check the staged JavaScript; the daemon
		// verifies agentInfo.version through ACP initialization after restart.
		_, err := runHarnessProcess(ctx, 10*time.Second, "node", "--check", installed.ResolvedPath)
		return err
	}
	out, err := runHarnessProcess(ctx, 10*time.Second, installed.Path, "--version")
	if err != nil {
		return fmt.Errorf("verify harness executable: %w", err)
	}
	for _, field := range strings.Fields(out) {
		if field == installed.Version {
			return nil
		}
	}
	return fmt.Errorf("executable did not report expected version %s", installed.Version)
}

type harnessOutput struct{ bytes.Buffer }

func (b *harnessOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 16384 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func runHarnessProcess(
	ctx context.Context,
	timeout time.Duration,
	executable string,
	args ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		executable,
		args...) // #nosec G204 -- Fixed npm package allowlist or the explicitly selected installed launcher.
	configureHarnessProcess(cmd)
	cmd.WaitDelay = time.Second
	var output harnessOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run %s: %w", filepath.Base(executable), err)
	}
	return output.String(), nil
}
