package adaptor

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

//go:embed pi_auth.mjs
var piAuthScript string

func validatePiAuthRequest(req *NativeAuthRequest) error {
	if len(req.Provider) > 128 || strings.TrimSpace(req.Provider) != req.Provider ||
		strings.ContainsAny(req.Provider, "\x00\r\n") {
		return fmt.Errorf("invalid Pi provider identifier")
	}
	switch req.Operation {
	case model.HarnessAuthLogin:
		_, err := model.ParseHarnessAuthMethod(string(req.Method), model.AgentNamePi)
		return err
	case model.HarnessAuthStatus, model.HarnessAuthLogout, model.HarnessAuthMethods:
		if req.Method != model.HarnessAuthMethodUnknown {
			return fmt.Errorf("method is only valid for login")
		}
		return nil
	case model.HarnessAuthOperationUnknown:
		return fmt.Errorf("pi authentication operation is required")
	}
	return fmt.Errorf("unsupported Pi authentication operation")
}

func piSDKInstallation() (*PiInstallation, error) {
	selected := os.Getenv("PI_EXECUTABLE")
	if selected == "" {
		selected = "pi"
	}
	return ResolvePiInstallation(selected)
}

// RunPi uses the SDK belonging to the selected Pi launcher, never an npx download
// or a second credential store. The helper returns metadata only.
func (a *NativeHarnessAuth) RunPi(
	ctx context.Context,
	req *NativeAuthRequest,
) (*model.HarnessAuthResult, error) {
	if req == nil {
		return nil, fmt.Errorf("authentication request is required")
	}
	if err := validatePiAuthRequest(req); err != nil {
		return nil, err
	}
	installation, err := piSDKInstallation()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(
		ctx,
		installation.Node,
		"--input-type=module",
		"-e",
		piAuthScript,
		"--",
		installation.SDKRoot,
		string(req.Operation),
		req.Provider,
		string(req.Method),
	) // #nosec G204 -- Embedded helper and parsed identifiers; secrets only on stdin.
	var output boundedAuthOutput
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, &output, req.Stderr
	prepareNativeAuth(cmd)
	// Allow readline to restore terminal mode before CommandContext's WaitDelay
	// escalates to a kill. Platforms without interrupt support kill directly.
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	defer stopNativeAuth(cmd)
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("pi authentication: %w", ctx.Err())
	}
	if output.overflow {
		return nil, fmt.Errorf("pi authentication response exceeds limit")
	}
	var result model.HarnessAuthResult
	if err := json.Unmarshal(output.data, &result); err != nil ||
		result.Harness != model.AgentNamePi {
		if runErr != nil {
			return nil, fmt.Errorf("run Pi authentication helper: %w", runErr)
		}
		return nil, fmt.Errorf("invalid Pi authentication response")
	}
	if result.State == model.HarnessAuthFailed {
		return nil, fmt.Errorf("pi authentication: %s", result.Message)
	}
	if runErr != nil {
		return nil, fmt.Errorf("run Pi authentication helper: %w", runErr)
	}
	return &result, nil
}
