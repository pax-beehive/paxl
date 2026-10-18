package model

import (
	"fmt"
	"strings"
	"unicode"
)

// Local authentication is independent of the daemon's wire protocol.
type HarnessAuthOperation string

const (
	HarnessAuthOperationUnknown HarnessAuthOperation = ""
	HarnessAuthLogin            HarnessAuthOperation = "login"
	HarnessAuthStatus           HarnessAuthOperation = "status"
	HarnessAuthLogout           HarnessAuthOperation = "logout"
	HarnessAuthMethods          HarnessAuthOperation = "methods"
)

type HarnessAuthMethod string

const (
	HarnessAuthMethodUnknown HarnessAuthMethod = ""
	HarnessAuthSubscription  HarnessAuthMethod = "subscription"
	HarnessAuthConsole       HarnessAuthMethod = "console"
	HarnessAuthDevice        HarnessAuthMethod = "device"
	HarnessAuthBrowser       HarnessAuthMethod = "browser"
	HarnessAuthAPIKey        HarnessAuthMethod = "api-key"
	HarnessAuthOAuth         HarnessAuthMethod = "oauth"
	HarnessAuthAccessToken   HarnessAuthMethod = "access-token"
)

func ParseLocalAuthHarness(raw string) (AgentName, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "claude", "claude-code":
		return AgentNameClaude, nil
	case "codex":
		return AgentNameCodex, nil
	case "kimi":
		return AgentNameKimi, nil
	case "pi":
		return AgentNamePi, nil
	case "opencode":
		return AgentNameOpenCode, nil
	case "kiro", "kiro-cli":
		return AgentNameKiro, nil
	case "hermes":
		return AgentNameHermes, nil
	case "openclaw":
		return AgentNameOpenClaw, nil
	default:
		return AgentNameUnknown, fmt.Errorf(
			"local authentication currently supports claude, codex, kimi, pi, opencode, kiro, hermes, and openclaw",
		)
	}
}

func ParseHarnessAuthMethod(raw string, harness AgentName) (HarnessAuthMethod, error) {
	if harness == AgentNameOpenCode {
		if raw != "" {
			return HarnessAuthMethodUnknown, fmt.Errorf(
				"opencode requires native method selection; omit --method to see this provider's subscription/OAuth and API-key choices (the native CLI can ignore --method for non-plugin providers)",
			)
		}
		return HarnessAuthMethodUnknown, nil
	}
	if harness == AgentNameOpenClaw {
		// Installed provider plugins own method labels/IDs; do not hardcode their
		// provider-specific OAuth or API-key choices into Pax.
		if len(raw) > 128 || strings.IndexFunc(raw, unicode.IsControl) >= 0 ||
			strings.HasPrefix(raw, "-") || strings.TrimSpace(raw) != raw {
			return HarnessAuthMethodUnknown, fmt.Errorf(
				"invalid %s method; omit --method to select from installed provider methods",
				harness,
			)
		}
		return HarnessAuthMethod(raw), nil
	}
	methods := map[AgentName][]HarnessAuthMethod{
		AgentNameClaude: {HarnessAuthSubscription, HarnessAuthConsole},
		AgentNameCodex: {
			HarnessAuthDevice,
			HarnessAuthBrowser,
			HarnessAuthAPIKey,
			HarnessAuthAccessToken,
		},
		AgentNameKimi:   {HarnessAuthDevice},
		AgentNamePi:     {HarnessAuthOAuth, HarnessAuthAPIKey},
		AgentNameKiro:   {HarnessAuthBrowser, HarnessAuthDevice},
		AgentNameHermes: {HarnessAuthOAuth, HarnessAuthAPIKey},
	}
	supported, ok := methods[harness]
	if !ok {
		return HarnessAuthMethodUnknown, fmt.Errorf(
			"unsupported authentication harness %q",
			harness,
		)
	}
	if raw == "" {
		return HarnessAuthMethodUnknown, nil
	}
	choices := make([]string, 0, len(supported))
	for _, method := range supported {
		if string(method) == raw {
			return method, nil
		}
		choices = append(choices, string(method))
	}
	defaultMethod := string(supported[0])
	if harness == AgentNamePi || harness == AgentNameHermes {
		defaultMethod = "provider-specific choice"
	}
	if harness == AgentNameKiro {
		defaultMethod = "native environment detection"
	}
	return HarnessAuthMethodUnknown, fmt.Errorf(
		"unsupported authentication method %q; supported methods for %s: %s (default: %s)",
		raw, harness, strings.Join(choices, ", "), defaultMethod,
	)
}

type HarnessAuthState string

const (
	HarnessAuthStateUnknown HarnessAuthState = "unknown"
	HarnessAuthSucceeded    HarnessAuthState = "succeeded"
	HarnessAuthFailed       HarnessAuthState = "failed"
	HarnessAuthLoggedIn     HarnessAuthState = "logged_in"
	HarnessAuthLoggedOut    HarnessAuthState = "logged_out"
	HarnessAuthConfigured   HarnessAuthState = "configured"
	HarnessAuthAvailable    HarnessAuthState = "available"
)

type HarnessAuthProvider struct {
	ProfileID      string              `json:"profile_id,omitempty"`
	ID             string              `json:"id"`
	Label          string              `json:"label,omitempty"`
	Methods        []HarnessAuthMethod `json:"methods,omitempty"`
	CredentialType string              `json:"credential_type,omitempty"`
	Source         string              `json:"source,omitempty"`
}

type HarnessAuthResult struct {
	Provider       string                `json:"provider,omitempty"`
	Providers      []HarnessAuthProvider `json:"providers,omitempty"`
	Message        string                `json:"message,omitempty"`
	RuntimeVersion string                `json:"runtime_version,omitempty"`
	Harness        AgentName             `json:"harness"`
	State          HarnessAuthState      `json:"state"`
	Method         HarnessAuthMethod     `json:"method,omitempty"`
	LoggedIn       *bool                 `json:"logged_in,omitempty"`
	AuthMethod     string                `json:"auth_method,omitempty"`
}
