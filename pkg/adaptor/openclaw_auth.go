package adaptor

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/pax-oss/paxl/internal/model"
)

func openClawAuthArgs(req *NativeAuthRequest) ([]string, error) {
	if req.Provider != "" && !nativeAuthProviderID.MatchString(req.Provider) {
		return nil, fmt.Errorf("invalid OpenClaw provider ID")
	}
	if req.Operation != model.HarnessAuthLogin && req.Method != "" {
		return nil, fmt.Errorf("method is only valid for login")
	}
	if req.Operation != model.HarnessAuthLogout && req.Profile != "" {
		return nil, fmt.Errorf("--profile is only supported for OpenClaw logout")
	}
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthLogin:
		return openClawLoginArgs(req)
	case model.HarnessAuthStatus:
		args := []string{"models", "auth", "list", "--json"}
		if req.Provider != "" {
			args = append(args, "--provider", req.Provider)
		}
		return args, nil
	case model.HarnessAuthLogout:
		if req.Profile == "" || len(req.Profile) > 512 || strings.HasPrefix(req.Profile, "-") ||
			strings.TrimSpace(req.Profile) != req.Profile ||
			strings.IndexFunc(req.Profile, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf(
				"OpenClaw logout requires --profile with an exact saved profile ID from paxl harness auth status --harness openclaw",
			)
		}
		if req.Provider != "" {
			return nil, fmt.Errorf("OpenClaw logout selects one exact --profile; omit --provider")
		}
		return []string{"models", "auth", "logout", req.Profile}, nil
	default:
		return nil, fmt.Errorf(
			"OpenClaw supports login, status, and logout; select provider methods using native interactive login",
		)
	}
}

func openClawLoginArgs(req *NativeAuthRequest) ([]string, error) {
	if _, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness); err != nil {
		return nil, err
	}
	if req.Method == model.HarnessAuthAPIKey {
		if req.Provider == "" {
			return nil, fmt.Errorf("OpenClaw API-key login requires --provider and --secret-stdin")
		}
		return []string{"models", "auth", "paste-api-key", "--provider", req.Provider}, nil
	}
	if req.Method != "" && req.Provider == "" {
		return nil, fmt.Errorf(
			"OpenClaw --method requires --provider; omit both to choose interactively",
		)
	}
	args := []string{"models", "auth", "login"}
	if req.Provider != "" {
		args = append(args, "--provider", req.Provider)
	}
	if req.Method != "" {
		args = append(args, "--method", string(req.Method))
	}
	return args, nil
}

func parseOpenClawAuthStatus(
	req *NativeAuthRequest,
	data []byte,
) (*model.HarnessAuthResult, error) {
	var raw struct {
		Profiles *[]struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Type     string `json:"type"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Profiles == nil {
		return nil, fmt.Errorf("could not determine OpenClaw saved profile status")
	}
	result := &model.HarnessAuthResult{
		Harness:  model.AgentNameOpenClaw,
		Provider: req.Provider,
		State:    model.HarnessAuthStateUnknown,
		Message:  "Saved profile metadata only; credentials and model access have not been verified. Environment credentials may also apply.",
	}
	for _, profile := range *raw.Profiles {
		if !nativeAuthProviderID.MatchString(profile.Provider) || profile.ID == "" ||
			len(profile.ID) > 512 ||
			strings.IndexFunc(profile.ID, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("invalid OpenClaw profile metadata")
		}
		switch profile.Type {
		case "api_key", "oauth", "token":
		default:
			return nil, fmt.Errorf("unrecognized OpenClaw credential type")
		}
		if req.Provider != "" && profile.Provider != req.Provider {
			return nil, fmt.Errorf("OpenClaw returned profiles for an unexpected provider")
		}
		result.Providers = append(
			result.Providers,
			model.HarnessAuthProvider{
				ID:             profile.Provider,
				ProfileID:      profile.ID,
				CredentialType: profile.Type,
			},
		)
	}
	if len(result.Providers) > 0 {
		result.State = model.HarnessAuthConfigured
	}
	return result, nil
}
