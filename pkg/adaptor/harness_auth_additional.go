package adaptor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

var nativeAuthProviderID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func additionalNativeAuthArgs(req *NativeAuthRequest) ([]string, error) {
	if req.Operation != model.HarnessAuthLogin && req.Method != "" {
		return nil, fmt.Errorf("method is only valid for login")
	}
	if req.Operation == model.HarnessAuthLogin {
		if _, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness); err != nil {
			return nil, err
		}
	}
	if req.Harness == model.AgentNameKiro {
		return kiroAuthArgs(req)
	}
	if !nativeAuthProviderID.MatchString(req.Provider) {
		return nil, fmt.Errorf(
			"hermes requires --provider with a provider ID, for example openai-codex or anthropic",
		)
	}
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthLogin:
		args := []string{"auth", "add", req.Provider}
		if req.Method != "" {
			args = append(args, "--type", string(req.Method))
		}
		return args, nil
	case model.HarnessAuthStatus:
		return []string{"auth", "status", req.Provider}, nil
	case model.HarnessAuthLogout:
		return []string{"auth", "logout", req.Provider}, nil
	default:
		return nil, fmt.Errorf(
			"hermes supports login, status, and logout; provider-specific methods are checked by the installed CLI",
		)
	}
}

func kiroAuthArgs(req *NativeAuthRequest) ([]string, error) {
	if req.Provider != "" {
		return nil, fmt.Errorf("kiro uses its native account selection; omit --provider")
	}
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthLogin:
		if req.Method == model.HarnessAuthDevice {
			return []string{"login", "--use-device-flow"}, nil
		}
		return []string{"login"}, nil
	case model.HarnessAuthStatus:
		return []string{"whoami", "--format", "json"}, nil
	case model.HarnessAuthLogout:
		return []string{"logout"}, nil
	default:
		return nil, fmt.Errorf(
			"kiro supports login, status, and logout; login methods: browser, device (omit for native environment detection)",
		)
	}
}

func parseAdditionalNativeAuthStatus(
	req *NativeAuthRequest,
	data []byte,
) (*model.HarnessAuthResult, error) {
	if req.Harness == model.AgentNameKiro {
		var raw struct {
			AccountType string `json:"accountType"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("could not determine Kiro authentication status")
		}
		// Only recognized metadata is returned, never email or raw diagnostics.
		switch raw.AccountType {
		case "BuilderId", "IamIdentityCenter", "Google", "Github", "GitHub", "Social", "ApiKey":
			return &model.HarnessAuthResult{
				Harness:    req.Harness,
				State:      model.HarnessAuthConfigured,
				AuthMethod: raw.AccountType,
				Message:    "Native CLI reports an account configuration; model access has not been verified.",
			}, nil
		default:
			return nil, fmt.Errorf("unrecognized Kiro authentication status")
		}
	}
	text := strings.TrimSpace(string(data))
	first, _, _ := strings.Cut(text, "\n")
	switch first {
	case req.Provider + ": logged in":
		return &model.HarnessAuthResult{
			Harness:  req.Harness,
			Provider: req.Provider,
			State:    model.HarnessAuthConfigured,
			Message:  "Native CLI reports configured credentials; model access has not been verified.",
		}, nil
	case req.Provider + ": logged out":
		return &model.HarnessAuthResult{
			Harness:  req.Harness,
			Provider: req.Provider,
			State:    model.HarnessAuthStateUnknown,
			Message:  "Native CLI reports no active provider login. Other pooled or environment credentials may still apply.",
		}, nil
	default:
		return nil, fmt.Errorf("could not determine Hermes provider authentication status")
	}
}
