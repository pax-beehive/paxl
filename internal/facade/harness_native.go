package facade

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func isClaudeNativePath(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "versions" &&
		filepath.Base(filepath.Dir(filepath.Dir(path))) == "claude" &&
		harnessVersionPattern.MatchString(filepath.Base(path))
}

func inspectClaudeNative(
	ctx context.Context,
	req *HarnessInspectRequest,
	path, resolved, name string,
) (*HarnessInstallation, error) {
	out, err := runHarnessProcess(ctx, 10*time.Second, resolved, "--version")
	if err != nil {
		return nil, fmt.Errorf("inspect native Claude: %w", err)
	}
	version := filepath.Base(resolved)
	if strings.TrimSpace(out) != version+" (Claude Code)" {
		return nil, fmt.Errorf(
			"native Claude executable did not report expected version %s",
			version,
		)
	}
	return &HarnessInstallation{SchemaVersion: 1, Harness: req.Harness, Component: req.Component,
		Path: path, ResolvedPath: resolved, Package: name, Version: version, Source: "native"}, nil
}
