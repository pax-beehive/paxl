package adaptor

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

//go:embed sdk_auth_interaction.mjs
var sdkAuthInteraction string

//go:embed gemini_auth.mjs
var geminiAuthScript string

//go:embed dsh_auth.mjs
var dshAuthScript string

//go:embed dscode_auth.mjs
var dscodeAuthScript string

type sdkAuthInstallation struct{ root, node string }

func isSDKAuth(harness model.AgentName) bool {
	return harness == model.AgentNameGemini || harness == model.AgentNameDSH ||
		harness == model.AgentNameDSCode
}

func validateSDKAuth(req *NativeAuthRequest) error {
	if req == nil {
		return fmt.Errorf("authentication request is required")
	}
	if req.Provider != "" && !nativeAuthProviderID.MatchString(req.Provider) {
		return fmt.Errorf("invalid provider ID")
	}
	if req.Profile != "" {
		return fmt.Errorf("--profile is not supported for SDK authentication")
	}
	switch req.Operation {
	case model.HarnessAuthLogin:
		_, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness)
		return err
	case model.HarnessAuthStatus, model.HarnessAuthLogout, model.HarnessAuthMethods:
		if req.Method != "" {
			return fmt.Errorf("method is only valid for login")
		}
		return nil
	case model.HarnessAuthOperationUnknown:
		return fmt.Errorf("authentication operation is required")
	}
	return fmt.Errorf("unsupported authentication operation")
}

func resolveSDKAuth(harness model.AgentName) (*sdkAuthInstallation, error) {
	keys := map[model.AgentName]string{
		model.AgentNameGemini: "GEMINI_EXECUTABLE",
		model.AgentNameDSH:    "DSH_EXECUTABLE",
		model.AgentNameDSCode: "DSCODE_EXECUTABLE",
	}
	selected := os.Getenv(keys[harness])
	if selected == "" {
		selected = string(harness)
	}
	path, err := exec.LookPath(selected)
	if err != nil {
		return nil, fmt.Errorf("find selected %s installation: %w", harness, err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve selected %s launcher: %w", harness, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve launcher path: %w", err)
	}
	expected := map[model.AgentName]string{model.AgentNameGemini: "@google/gemini-cli", model.AgentNameDSH: "@deepseek-ai/dsh", model.AgentNameDSCode: "todd-dsh-tui-harness"}[harness]
	dir := filepath.Dir(path)
	for range 8 {
		var pkg struct {
			Name string `json:"name"`
		}
		data, readErr := os.ReadFile(
			filepath.Join(dir, "package.json"),
		) // #nosec G304 -- Selected installed package metadata only.
		if readErr == nil && json.Unmarshal(data, &pkg) == nil && pkg.Name == expected {
			node := os.Getenv("PAXL_AUTH_NODE")
			if harness == model.AgentNameDSCode {
				node = filepath.Join(dir, "runtime", "bin", "node")
			}
			if node == "" {
				node = "node"
			}
			node, err = exec.LookPath(node)
			if err != nil {
				return nil, fmt.Errorf("find SDK Node runtime: %w", err)
			}
			return &sdkAuthInstallation{root: dir, node: node}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, fmt.Errorf(
		"selected %s launcher is not a supported official SDK installation; no alternate package was downloaded",
		harness,
	)
}

func (a *NativeHarnessAuth) RunSDK(
	ctx context.Context,
	req *NativeAuthRequest,
) (*model.HarnessAuthResult, error) {
	if err := validateSDKAuth(req); err != nil {
		return nil, err
	}
	install, err := resolveSDKAuth(req.Harness)
	if err != nil {
		return nil, err
	}
	scripts := map[model.AgentName]string{
		model.AgentNameGemini: geminiAuthScript,
		model.AgentNameDSH:    dshAuthScript,
		model.AgentNameDSCode: dscodeAuthScript,
	}
	script, ok := scripts[req.Harness]
	if !ok {
		return nil, fmt.Errorf("unsupported SDK authentication harness")
	}
	cmd := exec.CommandContext(
		ctx,
		install.node,
		"--input-type=module",
		"-e",
		sdkAuthInteraction+"\n"+script,
		"--",
		string(req.Harness),
		install.root,
		string(req.Operation),
		req.Provider,
		string(req.Method),
		fmt.Sprint(req.SecretStdin),
	) // #nosec G204 G702 -- Caller-selected installed runtime and fixed embedded helper; validated provider/method arguments, no shell; secrets only on stdin.
	var out boundedAuthOutput
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, &out, req.Stderr
	prepareNativeAuth(cmd)
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
		return nil, fmt.Errorf("SDK authentication: %w", ctx.Err())
	}
	if out.overflow {
		return nil, fmt.Errorf("SDK authentication response exceeds limit")
	}
	var result model.HarnessAuthResult
	if err := json.Unmarshal(out.data, &result); err != nil || result.Harness != req.Harness {
		if runErr != nil {
			return nil, fmt.Errorf("run native SDK helper: %w", runErr)
		}
		return nil, fmt.Errorf("invalid native SDK authentication response")
	}
	if result.State == model.HarnessAuthFailed {
		return nil, fmt.Errorf("native authentication: %s", strings.TrimSpace(result.Message))
	}
	if runErr != nil {
		return nil, fmt.Errorf("run native SDK helper: %w", runErr)
	}
	return &result, nil
}
