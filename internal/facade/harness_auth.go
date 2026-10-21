package facade

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"golang.org/x/term"
)

type HarnessAuthFacade struct{ native *adaptor.NativeHarnessAuth }

func NewHarnessAuthFacade() *HarnessAuthFacade {
	return &HarnessAuthFacade{native: adaptor.NewNativeHarnessAuth()}
}

type HarnessAuthRequest struct {
	Profile     string
	Provider    string
	Harness     model.AgentName
	Operation   model.HarnessAuthOperation
	Method      model.HarnessAuthMethod
	SecretStdin bool
}

func (f *HarnessAuthFacade) Execute(
	ctx context.Context,
	req *HarnessAuthRequest,
	opts ...func(*Option),
) (*model.HarnessAuthResult, error) {
	option := applyOptions(opts)
	req, err := prepareLocalAuthRequest(req, option)
	if err != nil {
		return nil, err
	}
	if err := validateLocalAuth(req); err != nil {
		return nil, err
	}
	native := &adaptor.NativeAuthRequest{
		SecretStdin: req.SecretStdin,
		Profile:     req.Profile,
		Provider:    req.Provider,
		Harness:     req.Harness,
		Operation:   req.Operation,
		Method:      req.Method,
		Stdin:       option.Stdin,
		Stdout:      option.Stderr,
		Stderr:      option.Stderr,
	}
	if err := f.native.Validate(native); err != nil {
		return nil, err
	}
	if req.Operation == model.HarnessAuthStatus {
		return f.status(ctx, native)
	}
	if req.SecretStdin {
		secret, err := readPromptedAuthSecret(ctx, option)
		if err != nil {
			return nil, err
		}
		native.Stdin = strings.NewReader(secret + "\n")
	}
	verbosef(option, "Running native %s authentication locally.", req.Harness)
	if req.Harness == model.AgentNameAntigravity {
		return f.native.RunAntigravity(ctx, native)
	}
	if req.Harness == model.AgentNameKimi {
		return f.native.RunKimi(ctx, native)
	}
	if req.Harness == model.AgentNamePi {
		return f.native.RunPi(ctx, native)
	}
	if req.Harness == model.AgentNameGemini || req.Harness == model.AgentNameDSH ||
		req.Harness == model.AgentNameDSCode {
		return f.native.RunSDK(ctx, native)
	}
	if err := f.native.Run(ctx, native); err != nil {
		return nil, fmt.Errorf("local harness authentication: %w", err)
	}
	// Successful command completion is not a provider-request verification.
	// In particular, logout may leave environment credentials active.
	result := &model.HarnessAuthResult{
		Harness:  req.Harness,
		Provider: req.Provider,
		State:    "succeeded",
		Method:   req.Method,
	}
	if req.Harness == model.AgentNameOpenClaw && req.Operation == model.HarnessAuthLogin &&
		req.Method == model.HarnessAuthAPIKey {
		result.Message = "API key saved. A running Gateway may require openclaw gateway restart to apply it; Gateway activation and model access are not verified."
	}
	return result, nil
}

func prepareLocalAuthRequest(req *HarnessAuthRequest, option *Option) (*HarnessAuthRequest, error) {
	if req != nil && req.Harness == model.AgentNameHermes &&
		req.Operation == model.HarnessAuthLogin && req.Provider == "" {
		if err := requireHermesAuthTerminal(option.Stdin); err != nil {
			return nil, err
		}
	}
	if req != nil && req.Operation == model.HarnessAuthLogin &&
		(req.Method == model.HarnessAuthAPIKey || req.Method == model.HarnessAuthAccessToken) &&
		req.Harness != model.AgentNameAntigravity && req.Harness != model.AgentNameOpenCode &&
		req.Harness != model.AgentNameHermes {
		if input, ok := option.Stdin.(*os.File); ok && term.IsTerminal(int(input.Fd())) {
			copyReq := *req
			copyReq.SecretStdin = true
			req = &copyReq
		}
	}
	return req, nil
}

func readPromptedAuthSecret(ctx context.Context, option *Option) (string, error) {
	if input, ok := option.Stdin.(*os.File); ok && term.IsTerminal(int(input.Fd())) {
		output := option.Stderr
		if output == nil {
			output = io.Discard
		}
		return readTerminalAuthSecret(ctx, input, output)
	}
	return readLocalAuthSecret(ctx, option.Stdin)
}

func validateLocalAuth(req *HarnessAuthRequest) error {
	if req == nil {
		return fmt.Errorf("authentication request is required")
	}
	if _, err := model.ParseLocalAuthHarness(string(req.Harness)); err != nil {
		return err
	}
	if err := validateAuthSelectors(req); err != nil {
		return err
	}
	if req.Operation == model.HarnessAuthLogin {
		if _, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness); err != nil {
			return err
		}
	}
	if req.Operation != model.HarnessAuthLogin && req.SecretStdin {
		return fmt.Errorf("secret input is only valid for login")
	}
	return validateAuthSecret(req)
}

func validateAuthSecret(req *HarnessAuthRequest) error {
	if req.Harness == model.AgentNameAntigravity {
		if req.SecretStdin {
			return fmt.Errorf(
				"antigravity uses existing GEMINI_API_KEY or ADC environment; --secret-stdin is not supported",
			)
		}
		return nil
	}
	if req.Harness == model.AgentNameOpenCode || req.Harness == model.AgentNameHermes {
		if req.SecretStdin {
			return fmt.Errorf(
				"%s uses native masked API-key prompts; --secret-stdin is not supported",
				req.Harness,
			)
		}
		return nil
	}
	secretMethod := req.Method == model.HarnessAuthAPIKey ||
		req.Method == model.HarnessAuthAccessToken
	if req.Harness == model.AgentNameOpenClaw {
		secretMethod = req.Method == model.HarnessAuthAPIKey
	}
	if req.Harness == model.AgentNamePi && req.SecretStdin && req.Provider == "" {
		return fmt.Errorf(
			"pi secret input requires --provider; list choices with paxl harness auth methods --harness pi",
		)
	}
	if (req.Harness == model.AgentNameDSH || req.Harness == model.AgentNameDSCode) &&
		req.SecretStdin &&
		req.Provider == "" {
		return fmt.Errorf("SDK secret input requires --provider; use auth methods to list choices")
	}
	if req.Operation == model.HarnessAuthLogin && req.SecretStdin != secretMethod {
		return fmt.Errorf(
			"API-key and access-token login require --secret-stdin; other methods do not accept it",
		)
	}
	return nil
}

func validateAuthSelectors(req *HarnessAuthRequest) error {
	if req.Provider != "" {
		//exhaustive:ignore
		switch req.Harness {
		case model.AgentNamePi,
			model.AgentNameOpenCode,
			model.AgentNameHermes,
			model.AgentNameOpenClaw, model.AgentNameGemini, model.AgentNameDSH, model.AgentNameDSCode:
		default:
			return fmt.Errorf(
				"--provider is supported for Pi, OpenCode, Hermes, OpenClaw, Gemini, DSH, and DSCODE",
			)
		}
	}
	if req.Profile != "" && req.Harness != model.AgentNameOpenClaw {
		return fmt.Errorf("--profile is currently supported only for OpenClaw logout")
	}
	return nil
}

func readLocalAuthSecret(ctx context.Context, input io.Reader) (string, error) {
	return awaitAuthSecret(ctx, func() (string, error) { return readLocalAuthSecretLine(input) })
}

func awaitAuthSecret(ctx context.Context, read func() (string, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read authentication input: %w", err)
	}
	type secretResult struct {
		value string
		err   error
	}
	result := make(chan secretResult, 1)
	go func() { value, err := read(); result <- secretResult{value: value, err: err} }()
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("read authentication input: %w", ctx.Err())
	case read := <-result:
		return read.value, read.err
	}
}

func readLocalAuthSecretLine(input io.Reader) (string, error) {
	if input == nil {
		return "", fmt.Errorf("secret stdin is required")
	}
	line, err := bufio.NewReader(io.LimitReader(input, 4098)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read authentication input: %w", err)
	}
	secret := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if secret == "" || len(secret) > 4096 || strings.TrimSpace(secret) != secret ||
		strings.ContainsAny(secret, "\r\n\x00") {
		return "", fmt.Errorf("expected one nonempty secret line of at most 4096 bytes")
	}
	return secret, nil
}

func (f *HarnessAuthFacade) status(
	ctx context.Context,
	req *adaptor.NativeAuthRequest,
) (*model.HarnessAuthResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := f.native.Status(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("read native authentication status: %w", err)
	}
	return result, nil
}
