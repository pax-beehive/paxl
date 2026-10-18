package adaptor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/pax-oss/paxl/internal/model"
)

func openCodeAuthArgs(req *NativeAuthRequest) ([]string, error) {
	if len(req.Provider) > 128 || strings.TrimSpace(req.Provider) != req.Provider ||
		strings.HasPrefix(req.Provider, "-") ||
		strings.IndexFunc(req.Provider, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf(
			"invalid OpenCode provider; use a provider ID or omit --provider for native selection",
		)
	}
	if req.Operation != model.HarnessAuthLogin && req.Method != model.HarnessAuthMethodUnknown {
		return nil, fmt.Errorf("method is only valid for login")
	}
	//exhaustive:ignore
	switch req.Operation {
	case model.HarnessAuthLogin:
		if _, err := model.ParseHarnessAuthMethod(string(req.Method), req.Harness); err != nil {
			return nil, err
		}
		if req.Method != "" && req.Provider == "" {
			return nil, fmt.Errorf(
				"OpenCode --method requires --provider; omit both to choose interactively",
			)
		}
		args := []string{"auth", "login"}
		if req.Provider != "" {
			args = append(args, "--provider", req.Provider)
		}
		if req.Method != "" {
			args = append(args, "--method", string(req.Method))
		}
		return args, nil
	case model.HarnessAuthLogout:
		args := []string{"auth", "logout"}
		if req.Provider != "" {
			args = append(args, req.Provider)
		}
		return args, nil
	case model.HarnessAuthStatus:
		if req.Provider != "" {
			return nil, fmt.Errorf("OpenCode native status lists all providers; omit --provider")
		}
		return []string{"auth", "list"}, nil
	default:
		return nil, fmt.Errorf(
			"OpenCode supports login, status, and logout; choose methods interactively with paxl harness auth login --harness opencode",
		)
	}
}

var openCodeStatusANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

var openCodeCredentialCount = regexp.MustCompile(
	`^[[:space:]│└┘╰]*([0-9]+) credentials?[[:space:]]*$`,
)

func parseOpenCodeAuthStatus(data []byte) (*model.HarnessAuthResult, error) {
	text := openCodeStatusANSI.ReplaceAllString(string(data), "")
	count := -1
	for _, line := range strings.Split(text, "\n") {
		match := openCodeCredentialCount.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if count >= 0 {
			return nil, fmt.Errorf("ambiguous OpenCode credential summary")
		}
		n, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid OpenCode credential summary")
		}
		count = n
	}
	if count < 0 || !strings.Contains(text, "Credentials ") {
		return nil, fmt.Errorf("could not determine OpenCode stored credential status")
	}
	state := model.HarnessAuthStateUnknown
	if count > 0 {
		state = model.HarnessAuthConfigured
	}
	return &model.HarnessAuthResult{
		Harness: model.AgentNameOpenCode,
		State:   state,
		Message: fmt.Sprintf(
			"Native CLI reports %d stored credentials. This does not verify provider access; environment credentials may also apply.",
			count,
		),
	}, nil
}
