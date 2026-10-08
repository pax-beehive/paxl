package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Resolve the tag once, then keep every installation and verification step
// bound to that exact version. Registry errors never become an implicit target.
func resolveLatestHarnessVersion(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		"npm",
		"view",
		name+"@latest",
		"version",
		"--json",
	) // #nosec G204 -- Package name comes from the inspected fixed allowlist.
	configureHarnessProcess(cmd)
	cmd.WaitDelay = time.Second
	var stdout harnessOutput
	cmd.Stdout = &stdout
	// Registry stderr may include authentication details. Do not include it in errors.
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("resolve latest %s version: %w", name, err)
	}
	var version string
	if err := json.Unmarshal(
		stdout.Bytes(),
		&version,
	); err != nil ||
		!harnessVersionPattern.MatchString(version) {
		return "", fmt.Errorf("registry did not return an exact semantic version for %s", name)
	}
	return version, nil
}
