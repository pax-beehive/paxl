package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type HarnessComponent string

const (
	HarnessComponentUnknown HarnessComponent = ""
	HarnessComponentACP     HarnessComponent = "acp"
	HarnessComponentCLI     HarnessComponent = "cli"
)

func ParseHarnessComponent(raw string) (HarnessComponent, error) {
	switch HarnessComponent(raw) {
	case HarnessComponentUnknown:
		return HarnessComponentUnknown, fmt.Errorf("harness component is required")
	case HarnessComponentACP, HarnessComponentCLI:
		return HarnessComponent(raw), nil
	default:
		return HarnessComponentUnknown, fmt.Errorf("unsupported harness component %q", raw)
	}
}

type HarnessInspectRequest struct {
	Harness   string
	Component HarnessComponent
	Path      string
}

type HarnessInstallation struct {
	SchemaVersion int              `json:"schema_version"`
	Harness       string           `json:"harness"`
	Component     HarnessComponent `json:"component"`
	Path          string           `json:"path"`
	ResolvedPath  string           `json:"resolved_path"`
	Package       string           `json:"package"`
	Version       string           `json:"version"`
	Source        string           `json:"source"`
}

type HarnessUpgradeFacade struct{}

func NewHarnessUpgradeFacade() *HarnessUpgradeFacade { return &HarnessUpgradeFacade{} }

type harnessPackage struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Bin     json.RawMessage `json:"bin"`
}

func harnessPackageName(req *HarnessInspectRequest) (string, string, error) {
	if req == nil {
		return "", "", fmt.Errorf("harness inspection request is required")
	}
	switch {
	case req.Harness == "claude" && req.Component == HarnessComponentCLI:
		return "@anthropic-ai/claude-code", "claude", nil
	case req.Harness == "codex" && req.Component == HarnessComponentCLI:
		return "@openai/codex", "codex", nil
	case req.Harness == "pi" && req.Component == HarnessComponentCLI:
		return "@earendil-works/pi-coding-agent", "pi", nil
	case req.Harness == "claude" && req.Component == HarnessComponentACP:
		return "@agentclientprotocol/claude-agent-acp", "claude-agent-acp", nil
	case req.Harness == "codex" && req.Component == HarnessComponentACP:
		return "@agentclientprotocol/codex-acp", "codex-acp", nil
	case req.Harness == "pi" && req.Component == HarnessComponentACP:
		return "@ccgv2/pi-acp", "pi-acp", nil
	default:
		return "", "", fmt.Errorf("unsupported harness/component %q/%q", req.Harness, req.Component)
	}
}

// Inspect follows the selected launcher, never an unrelated executable on PATH.
func (f *HarnessUpgradeFacade) Inspect(
	ctx context.Context, req *HarnessInspectRequest, opts ...func(*Option),
) (*HarnessInstallation, error) {
	name, bin, err := harnessPackageName(req)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("inspect harness: %w", err)
	}
	selected := req.Path
	if selected == "" {
		selected = bin
	}
	path, err := exec.LookPath(selected)
	if err != nil {
		return nil, fmt.Errorf("resolve harness launcher: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute launcher: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve harness installation: %w", err)
	}
	for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
		if part == "Cellar" || part == "Caskroom" || part == ".pnpm" || part == ".bun" {
			return nil, fmt.Errorf(
				"unsupported installation: launcher belongs to another package manager",
			)
		}
	}
	pkg, err := inspectHarnessPackage(resolved, name, bin)
	if err != nil {
		return nil, err
	}
	verbosef(applyOptions(opts), "Inspected %s at %s.", name, path)
	return &HarnessInstallation{
		SchemaVersion: 1, Harness: req.Harness, Component: req.Component,
		Path: path, ResolvedPath: resolved, Package: name, Version: pkg.Version, Source: "npm",
	}, nil
}

func inspectHarnessPackage(resolved, name, bin string) (*harnessPackage, error) {
	for dir := filepath.Dir(resolved); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		data, err := os.ReadFile(
			filepath.Join(dir, "package.json"),
		) // #nosec G304 -- Inspect the explicitly selected local installation.
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read harness package: %w", err)
		}
		var pkg harnessPackage
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("decode harness package: %w", err)
		}
		if pkg.Name != name || pkg.Version == "" {
			break
		}
		if !strings.HasSuffix(
			dir,
			string(
				filepath.Separator,
			)+filepath.Join(
				"lib",
				"node_modules",
				filepath.FromSlash(name),
			),
		) {
			break
		}
		var entry string
		if err := json.Unmarshal(pkg.Bin, &entry); err != nil {
			var bins map[string]string
			if err := json.Unmarshal(pkg.Bin, &bins); err != nil {
				return nil, fmt.Errorf("decode package launcher: %w", err)
			}
			entry = bins[bin]
		}
		if entry == "" {
			break
		}
		target, err := filepath.EvalSymlinks(filepath.Join(dir, entry))
		if err != nil || target != resolved {
			break
		}
		return &pkg, nil
	}
	return nil, fmt.Errorf(
		"unsupported installation: %s is not the declared launcher of %s",
		resolved,
		name,
	)
}
