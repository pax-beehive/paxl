package adaptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

// NativeHarnessAuth runs official commands in the caller's credential environment.
// It does not read credential files or create a second token store.
type NativeHarnessAuth struct{}

func NewNativeHarnessAuth() *NativeHarnessAuth { return &NativeHarnessAuth{} }

type NativeAuthRequest struct {
	Events      model.HarnessAuthEventSink
	SecretStdin bool
	Profile     string
	Provider    string
	Harness     model.AgentName
	Operation   model.HarnessAuthOperation
	Method      model.HarnessAuthMethod
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

func nativeAuthArgs(req *NativeAuthRequest) ([]string, error) {
	if req == nil {
		return nil, fmt.Errorf("authentication request is required")
	}
	if req.Harness == model.AgentNameAntigravity {
		return nil, validateAntigravityAuth(req)
	}
	if req.Harness == model.AgentNameKimi && req.Operation != model.HarnessAuthLogin {
		return nil, validateKimiAuth(req)
	}
	if req.Harness == model.AgentNamePi {
		return nil, validatePiAuthRequest(req)
	}
	if isSDKAuth(req.Harness) {
		return nil, validateSDKAuth(req)
	}
	if req.Harness == model.AgentNameOpenCode {
		return openCodeAuthArgs(req)
	}
	if req.Harness == model.AgentNameOpenClaw {
		return openClawAuthArgs(req)
	}
	if req.Harness == model.AgentNameKiro || req.Harness == model.AgentNameHermes {
		return additionalNativeAuthArgs(req)
	}
	if req.Operation == model.HarnessAuthLogin {
		return nativeLoginArgs(req)
	}
	if req.Method != model.HarnessAuthMethodUnknown {
		return nil, fmt.Errorf("method is only valid for login")
	}
	// Only implemented native authentication operations are mapped here.
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthStatus:
		// Harness discovery does not imply native authentication support.
		//exhaustive:ignore
		switch req.Harness {
		case model.AgentNameClaude:
			return []string{"auth", "status", "--json"}, nil
		case model.AgentNameCodex:
			return []string{"login", "status"}, nil
		}
	case model.HarnessAuthLogout:
		// Harness discovery does not imply native authentication support.
		//exhaustive:ignore
		switch req.Harness {
		case model.AgentNameClaude:
			return []string{"auth", "logout"}, nil
		case model.AgentNameCodex:
			return []string{"logout"}, nil
		}
	}
	return nil, fmt.Errorf(
		"%s does not provide a supported native %s command",
		req.Harness,
		req.Operation,
	)
}

func nativeLoginArgs(req *NativeAuthRequest) ([]string, error) {
	if _, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness); err != nil {
		return nil, err
	}
	// Harness discovery does not imply native authentication support.
	//exhaustive:ignore
	switch req.Harness {
	case model.AgentNameClaude:
		// Methods are intentionally provider-specific.
		//exhaustive:ignore
		switch req.Method {
		case model.HarnessAuthMethodUnknown, model.HarnessAuthSubscription:
			return []string{"auth", "login"}, nil
		case model.HarnessAuthConsole:
			return []string{"auth", "login", "--console"}, nil
		}
	case model.AgentNameCodex:
		// Methods are intentionally provider-specific.
		//exhaustive:ignore
		switch req.Method {
		case model.HarnessAuthMethodUnknown, model.HarnessAuthDevice:
			return []string{"login", "--device-auth"}, nil
		case model.HarnessAuthBrowser:
			return []string{"login"}, nil
		case model.HarnessAuthAPIKey:
			return []string{"login", "--with-api-key"}, nil
		case model.HarnessAuthAccessToken:
			return []string{"login", "--with-access-token"}, nil
		}
	case model.AgentNameKimi:
		if req.Method == model.HarnessAuthMethodUnknown || req.Method == model.HarnessAuthDevice {
			return []string{"login"}, nil
		}
	}
	return nil, fmt.Errorf("unsupported authentication method %q for %s", req.Method, req.Harness)
}

func (a *NativeHarnessAuth) Run(ctx context.Context, req *NativeAuthRequest) error {
	if req != nil && req.Harness == model.AgentNameAntigravity {
		_, err := a.RunAntigravity(ctx, req)
		return err
	}
	if req != nil && req.Harness == model.AgentNameKimi && req.Operation != model.HarnessAuthLogin {
		_, err := a.RunKimi(ctx, req)
		return err
	}
	if req != nil && isSDKAuth(req.Harness) {
		_, err := a.RunSDK(ctx, req)
		return err
	}
	if req != nil && req.Harness == model.AgentNamePi {
		_, err := a.RunPi(ctx, req)
		return err
	}
	args, err := nativeAuthArgs(req)
	if err != nil {
		return err
	}
	command := authExecutable(req.Harness)
	cmd := exec.CommandContext(
		ctx,
		command,
		args...) // #nosec G204 -- Fixed native subcommands, no shell.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, req.Stdout, req.Stderr
	if (req.Harness == model.AgentNameCodex &&
		(req.Method == model.HarnessAuthAPIKey || req.Method == model.HarnessAuthAccessToken)) ||
		(req.Harness == model.AgentNameOpenClaw && req.Method == model.HarnessAuthAPIKey) {
		// Native output is untrusted and may echo the supplied secret on failure.
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	}
	prepareNativeAuth(cmd)
	defer stopNativeAuth(cmd)
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("run %s authentication: %w", req.Harness, ctx.Err())
		}
		return fmt.Errorf("run %s authentication: %w", req.Harness, err)
	}
	return nil
}

func authExecutable(harness model.AgentName) string {
	// Match runtime executable overrides instead of silently choosing another CLI.
	key := ""
	// Only these runtimes expose executable overrides.
	//exhaustive:ignore
	switch harness {
	case model.AgentNameClaude:
		key = "CLAUDE_CODE_EXECUTABLE"
	case model.AgentNameCodex:
		key = "CODEX_PATH"
	case model.AgentNameOpenCode:
		key = "OPENCODE_EXECUTABLE"
	case model.AgentNameOpenClaw:
		key = "OPENCLAW_EXECUTABLE"
	case model.AgentNameKiro:
		if value := os.Getenv("KIRO_CLI_EXECUTABLE"); value != "" {
			return value
		}
		return "kiro-cli"
	case model.AgentNameHermes:
		key = "HERMES_EXECUTABLE"
	case model.AgentNameKimi:
		key = "KIMI_EXECUTABLE"
	case model.AgentNameAntigravity:
		if value := os.Getenv("AGY_EXECUTABLE"); value != "" {
			return value
		}
		return "agy"
	}
	if value := os.Getenv(key); key != "" && value != "" {
		return value
	}
	return string(harness)
}

// CaptureStatus never returns raw native output: it can contain masked keys.
func (a *NativeHarnessAuth) CaptureStatus(
	ctx context.Context,
	req *NativeAuthRequest,
) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("authentication request is required")
	}
	var output boundedAuthOutput
	request := *req
	request.Stdout, request.Stderr = &output, &output
	request.Stdin = nil
	err := a.Run(ctx, &request)
	if output.overflow {
		return nil, fmt.Errorf("authentication status output exceeds limit")
	}
	if err != nil {
		var exit *exec.ExitError
		// Both official CLIs may use exit 1 to report a known signed-out state.
		if !errors.As(err, &exit) || exit.ExitCode() != 1 ||
			!knownSignedOut(req.Harness, output.data) {
			return nil, err
		}
	}
	return bytes.TrimSpace(output.data), nil
}

type boundedAuthOutput struct {
	data     []byte
	overflow bool
}

func (b *boundedAuthOutput) Write(p []byte) (int, error) {
	const limit = 64 * 1024
	n := len(p)
	if len(b.data)+n > limit {
		b.overflow = true
		p = p[:limit-len(b.data)]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func nativeCodexStatus(text string) (bool, string, bool) {
	switch {
	case text == "Not logged in":
		return false, "", true
	case strings.HasPrefix(text, "Logged in using ChatGPT"):
		return true, "chatgpt", true
	case strings.HasPrefix(text, "Logged in using an API key"):
		return true, "api_key", true
	case strings.HasPrefix(text, "Logged in using personal access token"),
		strings.HasPrefix(text, "Logged in using access token"):
		return true, "access_token", true
	case strings.HasPrefix(text, "Logged in using workload identity"):
		return true, "workload_identity", true
	default:
		return false, "", false
	}
}

func ParseNativeCodexAuthStatus(data []byte) (*model.HarnessAuthResult, error) {
	loggedIn, method, known := nativeCodexStatus(strings.TrimSpace(string(data)))
	if !known {
		return nil, fmt.Errorf("could not determine Codex authentication status")
	}
	state := model.HarnessAuthLoggedOut
	if loggedIn {
		state = model.HarnessAuthLoggedIn
	}
	return &model.HarnessAuthResult{
		Harness:    model.AgentNameCodex,
		State:      state,
		LoggedIn:   &loggedIn,
		AuthMethod: method,
	}, nil
}

func knownSignedOut(harness model.AgentName, data []byte) bool {
	if harness == model.AgentNameCodex {
		return strings.TrimSpace(string(data)) == "Not logged in"
	}
	if harness == model.AgentNameClaude {
		var raw struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		return json.Unmarshal(data, &raw) == nil && raw.LoggedIn != nil && !*raw.LoggedIn
	}
	return false
}

func (a *NativeHarnessAuth) Validate(req *NativeAuthRequest) error {
	_, err := nativeAuthArgs(req)
	return err
}

// Status normalizes provider-specific output at the native adapter boundary.
func (a *NativeHarnessAuth) Status(
	ctx context.Context,
	req *NativeAuthRequest,
) (*model.HarnessAuthResult, error) {
	if req != nil && req.Harness == model.AgentNameAntigravity {
		return a.RunAntigravity(ctx, req)
	}
	if req != nil && req.Harness == model.AgentNameKimi {
		return a.RunKimi(ctx, req)
	}
	if req != nil && isSDKAuth(req.Harness) {
		return a.RunSDK(ctx, req)
	}
	if req != nil && req.Harness == model.AgentNamePi {
		return a.RunPi(ctx, req)
	}
	data, err := a.CaptureStatus(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.Harness == model.AgentNameCodex {
		return ParseNativeCodexAuthStatus(data)
	}
	if req.Harness == model.AgentNameOpenCode {
		return parseOpenCodeAuthStatus(data)
	}
	if req.Harness == model.AgentNameOpenClaw {
		return parseOpenClawAuthStatus(req, data)
	}
	if req.Harness == model.AgentNameKiro || req.Harness == model.AgentNameHermes {
		return parseAdditionalNativeAuthStatus(req, data)
	}
	var raw struct {
		LoggedIn   *bool  `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.LoggedIn == nil {
		return nil, fmt.Errorf("could not determine Claude authentication status")
	}
	state := model.HarnessAuthLoggedOut
	if *raw.LoggedIn {
		state = model.HarnessAuthLoggedIn
	}
	return &model.HarnessAuthResult{
		Harness:    req.Harness,
		State:      state,
		LoggedIn:   raw.LoggedIn,
		AuthMethod: raw.AuthMethod,
	}, nil
}
