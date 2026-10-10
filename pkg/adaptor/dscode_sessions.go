package adaptor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

// NewDSCodeAdapter shares the Harness log codec, but never its storage root.
func NewDSCodeAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{Name: model.AgentNameDSCode, Kind: model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI, Command: []string{"dscode"}},
		cliProbe:     func() bool { return commandExists("dscode") },
		sessionProbe: func() bool { root, err := dscodeSessionsRoot(); return err == nil && pathExists(root) },
		listSessions: listDSCodeSessions,
		getSession:   getDSCodeSession,
		prompt:       promptDSCodeSession,
		resume:       nativeSessionResumer("dscode", "resume"),
	}
}

func dscodeSessionsRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("PAXL_DSCODE_SESSIONS_DIR")); root != "" {
		return filepath.Abs(root)
	}
	home := strings.TrimSpace(os.Getenv("DSCODE_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve DSCODE home: %w", err)
		}
		home = filepath.Join(userHome, ".local", "share", "dscode-hub")
	}
	return filepath.Abs(filepath.Join(home, "sessions"))
}

func listDSCodeSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	root, err := dscodeSessionsRoot()
	if err != nil {
		return nil, err
	}
	resp, err := listDSHSessionsAt(ctx, req, root)
	if err != nil {
		return nil, err
	}
	for _, session := range resp.Sessions {
		session.Agent = model.AgentNameDSCode
		session.ID = "dscode:" + session.NativeID
	}
	return resp, nil
}

func getDSCodeSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	root, err := dscodeSessionsRoot()
	if err != nil {
		return nil, err
	}
	resp, err := getDSHSessionAt(ctx, req, root)
	if err != nil {
		return nil, err
	}
	for _, element := range resp.Elements {
		element.SessionID = "dscode:" + req.NativeID
	}
	return resp, nil
}

// The bridge queues into the running session without opening a second writer.
func promptDSCodeSession(
	ctx context.Context,
	req *PromptRequest,
	option *Option,
) (*PromptResponse, error) {
	if req == nil || strings.TrimSpace(req.NativeID) == "" || strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("native session id and text are required")
	}
	if err := validateNativeSessionID(req.NativeID); err != nil {
		return nil, err
	}
	resp, err := runArgPromptCommand(
		ctx,
		[]string{"dscode", "send", req.NativeID, "--"},
		req.Text,
		option,
	)
	if err != nil {
		return nil, err
	}
	resp.DeliveryMethod = "session_bridge"
	return resp, nil
}
