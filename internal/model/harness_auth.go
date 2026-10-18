package model

import (
	"fmt"
	"strings"
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
	default:
		return AgentNameUnknown, fmt.Errorf(
			"local authentication currently supports claude, codex, kimi, and pi",
		)
	}
}

func ParseHarnessAuthMethod(raw string, harness AgentName) (HarnessAuthMethod, error) {
	methods := map[AgentName][]HarnessAuthMethod{
		AgentNameClaude: {HarnessAuthSubscription, HarnessAuthConsole},
		AgentNameCodex: {
			HarnessAuthDevice,
			HarnessAuthBrowser,
			HarnessAuthAPIKey,
			HarnessAuthAccessToken,
		},
		AgentNameKimi: {HarnessAuthDevice},
		AgentNamePi:   {HarnessAuthOAuth, HarnessAuthAPIKey},
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
	if harness == AgentNamePi {
		defaultMethod = "provider choice"
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
