package model

import (
	"fmt"
	"strings"
)

type DaemonHarnessAuthState string

const (
	DaemonHarnessAuthUnknown         DaemonHarnessAuthState = "unknown"
	DaemonHarnessAuthStarting        DaemonHarnessAuthState = "starting"
	DaemonHarnessAuthAwaitingCode    DaemonHarnessAuthState = "awaiting_code"
	DaemonHarnessAuthAwaitingBrowser DaemonHarnessAuthState = "awaiting_browser"
	DaemonHarnessAuthExchanging      DaemonHarnessAuthState = "exchanging"
	DaemonHarnessAuthSucceeded       DaemonHarnessAuthState = "succeeded"
	DaemonHarnessAuthFailed          DaemonHarnessAuthState = "failed"
	DaemonHarnessAuthExpired         DaemonHarnessAuthState = "expired"
	DaemonHarnessAuthCancelled       DaemonHarnessAuthState = "cancelled"
	DaemonHarnessAuthLoggedIn        DaemonHarnessAuthState = "logged_in"
	DaemonHarnessAuthLoggedOut       DaemonHarnessAuthState = "logged_out"
)

func ParseDaemonHarnessAuthState(raw string) (DaemonHarnessAuthState, error) {
	switch state := DaemonHarnessAuthState(raw); state {
	case DaemonHarnessAuthAwaitingBrowser,
		DaemonHarnessAuthUnknown,
		DaemonHarnessAuthStarting,
		DaemonHarnessAuthAwaitingCode,
		DaemonHarnessAuthExchanging,
		DaemonHarnessAuthSucceeded,
		DaemonHarnessAuthFailed,
		DaemonHarnessAuthExpired,
		DaemonHarnessAuthCancelled,
		DaemonHarnessAuthLoggedIn,
		DaemonHarnessAuthLoggedOut:
		return state, nil
	default:
		return DaemonHarnessAuthUnknown, fmt.Errorf(
			"unsupported daemon authentication state %q",
			raw,
		)
	}
}

func ParseDaemonAuthHarness(raw string) (AgentName, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "claude", "claude-code":
		return AgentNameClaude, nil
	case "codex":
		return AgentNameCodex, nil
	default:
		return AgentNameUnknown, fmt.Errorf(
			"daemon authentication currently supports claude and codex; other harnesses require their native authentication workflow",
		)
	}
}

type DaemonHarnessAuthView struct {
	Harness          string                 `json:"harness"`
	SessionID        string                 `json:"session_id,omitempty"`
	State            DaemonHarnessAuthState `json:"state"`
	Method           string                 `json:"method,omitempty"`
	UserCode         string                 `json:"user_code,omitempty"`
	AuthorizationURL string                 `json:"authorization_url,omitempty"`
	ExpiresAt        string                 `json:"expires_at,omitempty"`
	LoggedIn         *bool                  `json:"logged_in,omitempty"`
	AuthMethod       string                 `json:"auth_method,omitempty"`
	ErrorCode        string                 `json:"error_code,omitempty"`
}

type DaemonHarnessAuthOperation string

const (
	DaemonHarnessAuthOperationUnknown DaemonHarnessAuthOperation = "unknown"
	DaemonHarnessAuthStart            DaemonHarnessAuthOperation = "start"
	DaemonHarnessAuthSubmit           DaemonHarnessAuthOperation = "submit"
	DaemonHarnessAuthCancel           DaemonHarnessAuthOperation = "cancel"
)

type DaemonHarnessAuthLoginCommand struct {
	Harness   string                     `json:"harness"`
	Operation DaemonHarnessAuthOperation `json:"operation"`
	Method    string                     `json:"method,omitempty"`
	SessionID string                     `json:"session_id,omitempty"`
	Code      string                     `json:"code,omitempty"`
}

// ValidateDaemonAuthMethod rejects unsupported interactions before contacting paxd.
func ValidateDaemonAuthMethod(harness AgentName, method string, secretInput, codeInput bool) error {
	if secretInput && codeInput {
		return fmt.Errorf("--secret-stdin and --code-stdin cannot be combined")
	}
	if codeInput {
		if harness != AgentNameClaude || method != "" {
			return fmt.Errorf("--code-stdin continues a Claude login without --method")
		}
		return nil
	}
	if harness == AgentNameClaude {
		if secretInput || (method != "" && method != "subscription" && method != "console") {
			return fmt.Errorf(
				"login for Claude supports subscription or console browser authentication",
			)
		}
		return nil
	}
	if harness != AgentNameCodex {
		return fmt.Errorf("unsupported authentication harness")
	}
	switch method {
	case "", "device":
		if secretInput {
			return fmt.Errorf("device login does not accept --secret-stdin")
		}
	case "api-key", "access-token":
		if !secretInput {
			return fmt.Errorf("this method requires --secret-stdin")
		}
	default:
		return fmt.Errorf("login for Codex supports device, api-key, or access-token")
	}
	return nil
}
