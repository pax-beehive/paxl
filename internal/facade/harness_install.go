package facade

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

type HarnessInstallRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, []string) error
}

type HarnessInstallFacade struct {
	runner HarnessInstallRunner
	client UpdateHTTPClient
}

func NewHarnessInstallFacade(runner HarnessInstallRunner) *HarnessInstallFacade {
	return &HarnessInstallFacade{runner: runner}
}

type HarnessInstallRequest struct {
	Harness   string
	Via       HarnessInstallVia
	Component HarnessComponent
	Version   string
	DryRun    bool
}

type HarnessInstallResult struct {
	Harness   string            `json:"harness"`
	Component HarnessComponent  `json:"component"`
	Package   string            `json:"package"`
	Version   string            `json:"version"`
	State     string            `json:"state"`
	Command   []string          `json:"command"`
	Via       HarnessInstallVia `json:"via"`
	Source    string            `json:"source"`
	ScriptURL string            `json:"script_url,omitempty"`
}

func (f *HarnessInstallFacade) Install(
	ctx context.Context,
	req *HarnessInstallRequest,
	opts ...func(*Option),
) (*HarnessInstallResult, error) {
	if req == nil {
		return nil, fmt.Errorf("harness installation request is required")
	}
	result, err := planHarnessInstall(req)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		return result, nil
	}
	option := applyOptions(opts)
	runner := f.runner
	if runner == nil {
		runner = &nativeHarnessInstallRunner{input: option.Stdin, output: option.Stderr}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	command, err := runner.LookPath(result.Command[0])
	if err != nil {
		return nil, fmt.Errorf(
			"find %s for %s installation: %w",
			result.Command[0],
			result.Harness,
			err,
		)
	}
	args := append([]string(nil), result.Command[1:]...)
	if result.ScriptURL != "" {
		path, cleanup, err := f.downloadOfficialInstaller(ctx, result.ScriptURL)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		args[0] = path
	}
	verbosef(option, "Installing %s using its official %s route.", result.Harness, result.Source)
	if err := runner.Run(ctx, command, args); err != nil {
		return nil, fmt.Errorf("install %s: %w", result.Harness, err)
	}
	result.State = "installed"
	if result.Source == "script" {
		result.State = "completed"
	}
	return result, nil
}

type nativeHarnessInstallRunner struct {
	input  io.Reader
	output io.Writer
}

func (r *nativeHarnessInstallRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}
func (r *nativeHarnessInstallRunner) Run(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(
		ctx,
		name,
		args...) // #nosec G204 -- Allowlisted installer executable and parsed arguments.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.input, r.output, r.output
	configureHarnessInstallProcess(cmd)
	defer func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	}()
	cmd.WaitDelay = time.Second
	return cmd.Run()
}
