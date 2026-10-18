package facade

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

type HarnessAuthFacade struct{ native *adaptor.NativeHarnessAuth }

func NewHarnessAuthFacade() *HarnessAuthFacade {
	return &HarnessAuthFacade{native: adaptor.NewNativeHarnessAuth()}
}

type HarnessAuthRequest struct {
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
	if err := validateLocalAuth(req); err != nil {
		return nil, err
	}
	option := applyOptions(opts)
	native := &adaptor.NativeAuthRequest{
		Provider:  req.Provider,
		Harness:   req.Harness,
		Operation: req.Operation,
		Method:    req.Method,
		Stdin:     option.Stdin,
		Stdout:    option.Stderr,
		Stderr:    option.Stderr,
	}
	if err := f.native.Validate(native); err != nil {
		return nil, err
	}
	if req.Operation == model.HarnessAuthStatus {
		return f.status(ctx, native)
	}
	if req.SecretStdin {
		secret, err := readLocalAuthSecret(ctx, option.Stdin)
		if err != nil {
			return nil, err
		}
		native.Stdin = strings.NewReader(secret + "\n")
	}
	verbosef(option, "Running native %s authentication locally.", req.Harness)
	if req.Harness == model.AgentNamePi {
		return f.native.RunPi(ctx, native)
	}
	if err := f.native.Run(ctx, native); err != nil {
		return nil, fmt.Errorf("local harness authentication: %w", err)
	}
	// Successful command completion is not a provider-request verification.
	// In particular, logout may leave environment credentials active.
	return &model.HarnessAuthResult{
		Harness: req.Harness,
		State:   "succeeded",
		Method:  req.Method,
	}, nil
}

func validateLocalAuth(req *HarnessAuthRequest) error {
	if req == nil {
		return fmt.Errorf("authentication request is required")
	}
	if _, err := model.ParseLocalAuthHarness(string(req.Harness)); err != nil {
		return err
	}
	if req.Provider != "" && req.Harness != model.AgentNamePi {
		return fmt.Errorf("--provider is currently supported only for Pi")
	}
	if req.Operation == model.HarnessAuthLogin {
		if _, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness); err != nil {
			return err
		}
	}
	if req.Operation != model.HarnessAuthLogin && req.SecretStdin {
		return fmt.Errorf("secret input is only valid for login")
	}
	secretMethod := req.Method == model.HarnessAuthAPIKey ||
		req.Method == model.HarnessAuthAccessToken
	if req.Harness == model.AgentNamePi && req.SecretStdin && req.Provider == "" {
		return fmt.Errorf(
			"pi secret input requires --provider; list choices with paxl harness auth methods --harness pi",
		)
	}
	if req.Operation == model.HarnessAuthLogin && req.SecretStdin != secretMethod {
		return fmt.Errorf(
			"API-key and access-token login require --secret-stdin; other methods do not accept it",
		)
	}
	return nil
}

func readLocalAuthSecret(ctx context.Context, input io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read authentication input: %w", err)
	}
	type secretResult struct {
		value string
		err   error
	}
	result := make(chan secretResult, 1)
	go func() { value, err := readLocalAuthSecretLine(input); result <- secretResult{value: value, err: err} }()
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
