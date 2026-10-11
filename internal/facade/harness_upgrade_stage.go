package facade

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (f *HarnessUpgradeFacade) installHarnessStage(
	ctx context.Context,
	req *HarnessUpgradeRequest,
	before *HarnessInstallation,
	stage string,
) (string, error) {
	if before.Source == "native" {
		return f.stageClaudeNative(ctx, req, before, stage)
	}
	_, err := runHarnessProcess(
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
		return "", fmt.Errorf("install harness package: %w", err)
	}
	return before.ResolvedPath, nil
}

func (f *HarnessUpgradeFacade) stageClaudeNative(
	ctx context.Context,
	req *HarnessUpgradeRequest,
	before *HarnessInstallation,
	stage string,
) (string, error) {
	next := filepath.Join(stage, "claude", "versions", req.Version)
	if err := f.downloadClaudeNative(ctx, req.Version, next); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(stage, "bin"), 0700); err != nil {
		return "", fmt.Errorf("create native Claude launcher directory: %w", err)
	}
	if err := os.Symlink(next, filepath.Join(stage, "bin", "claude")); err != nil {
		return "", fmt.Errorf("stage native Claude launcher: %w", err)
	}
	// Claude's own updater can prune its old versions. Keep an independent copy
	// under paxl's private staging directory so rollback survives that cleanup.
	previous := filepath.Join(stage, "previous", "claude", "versions", before.Version)
	if err := copyClaudeNative(before.ResolvedPath, previous); err != nil {
		return "", err
	}
	inspection := req.HarnessInspectRequest
	inspection.Path = previous
	preserved, err := f.Inspect(ctx, &inspection)
	if err != nil {
		return "", fmt.Errorf("verify native Claude rollback copy: %w", err)
	}
	return preserved.ResolvedPath, nil
}

func copyClaudeNative(source, target string) error {
	input, err := os.Open(
		source,
	) // #nosec G304 -- Explicitly selected and inspected installed binary.
	if err != nil {
		return fmt.Errorf("open native Claude rollback source: %w", err)
	}
	defer func() { _ = input.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return fmt.Errorf("create native Claude rollback directory: %w", err)
	}
	output, err := os.OpenFile(
		target,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0700,
	) // #nosec G302 G304 -- Private rollback copy must be executable for verification and recovery.
	if err != nil {
		return fmt.Errorf("create native Claude rollback copy: %w", err)
	}
	defer func() { _ = output.Close() }()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy native Claude rollback binary: %w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync native Claude rollback copy: %w", err)
	}
	return nil
}
