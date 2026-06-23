package adaptor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

var openClawDefaultACPCommand = []string{"openclaw", "acp"}

func NewOpenClawAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameOpenClaw,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityGateway,
			Command:    openClawDefaultACPCommand,
			Reason:     "Install OpenClaw and expose its ACP server, usually with openclaw acp.",
		},
		probe:        openClawACPAvailable,
		cliProbe:     openClawCLIAvailable,
		sessionProbe: openClawACPAvailable,
		listSessions: listOpenClawSessions,
		getSession:   getOpenClawSession,
		prompt:       promptOpenClawSession,
	}
}

func openClawCLIAvailable() bool {
	command := openClawACPCommand()
	return len(command) > 0 && commandExists(command[0])
}

func openClawACPAvailable() bool {
	if !openClawCLIAvailable() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return openClawACPClient(2*time.Second).initialize(ctx) == nil
}

func listOpenClawSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	if !openClawCLIAvailable() {
		return &ListSessionsResponse{}, nil
	}
	sessions, err := openClawACPClient(10 * time.Second).listSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list openclaw acp sessions: %w", err)
	}
	byID := make(map[string]*model.Session, len(sessions))
	for _, session := range sessions {
		modelSession := openClawModelSession(&session)
		if modelSession.ID != "" {
			byID[modelSession.ID] = modelSession
		}
	}
	return sortedSessions(byID, req), nil
}

func getOpenClawSession(
	ctx context.Context,
	req *GetSessionRequest,
) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	if !openClawCLIAvailable() {
		return nil, fmt.Errorf("openclaw ACP command is unavailable")
	}
	sessions, err := openClawACPClient(10 * time.Second).listSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list openclaw acp sessions: %w", err)
	}
	for _, session := range sessions {
		nativeID := firstNonEmpty(session.NativeID, session.SessionID)
		if nativeID != req.NativeID {
			continue
		}
		return &GetSessionResponse{
			Elements: []*model.Element{openClawSessionElement(&session)},
		}, nil
	}
	return nil, fmt.Errorf("openclaw session %q not found", req.NativeID)
}

func promptOpenClawSession(
	ctx context.Context,
	req *PromptRequest,
	option *Option,
) (*PromptResponse, error) {
	_ = option
	if req == nil || req.NativeID == "" || req.Text == "" {
		return nil, fmt.Errorf("native session id and prompt text are required")
	}
	if err := validateNativeSessionID(req.NativeID); err != nil {
		return nil, err
	}
	if !openClawCLIAvailable() {
		return nil, fmt.Errorf("openclaw ACP command is unavailable")
	}
	if err := openClawACPClient(30*time.Second).prompt(ctx, req.NativeID, req.Text); err != nil {
		return nil, err
	}
	return &PromptResponse{DeliveryMethod: "acp_session_prompt"}, nil
}

func openClawACPClient(timeout time.Duration) *acpClient {
	return &acpClient{
		command: openClawACPCommand(),
		timeout: timeout,
	}
}

func openClawACPCommand() []string {
	if raw := strings.TrimSpace(os.Getenv("PAXL_OPENCLAW_ACP_COMMAND")); raw != "" {
		return strings.Fields(raw)
	}
	return append([]string{}, openClawDefaultACPCommand...)
}

func openClawModelSession(session *hermesSessionInfo) *model.Session {
	nativeID := firstNonEmpty(session.NativeID, session.SessionID)
	if nativeID == "" {
		return &model.Session{}
	}
	roots, _ := json.Marshal(session.WorkspaceRoots)
	return &model.Session{
		ID:                 "openclaw:" + strings.TrimPrefix(nativeID, "openclaw:"),
		Agent:              model.AgentNameOpenClaw,
		NativeID:           strings.TrimPrefix(nativeID, "openclaw:"),
		Title:              firstNonEmpty(session.Name, titleCandidate(session.Preview), nativeID),
		Status:             firstNonEmpty(session.Status, "available"),
		Preview:            session.Preview,
		ProjectID:          session.ProjectID,
		WorkspaceRootsJSON: string(roots),
		LastActive:         session.LastActive,
		UpdatedAt:          firstNonEmpty(session.UpdatedAt, session.LastActive),
		RawJSON:            session.RawJSON,
	}
}

func openClawSessionElement(session *hermesSessionInfo) *model.Element {
	modelSession := openClawModelSession(session)
	content := fmt.Sprintf(
		"OpenClaw session %s\nStatus: %s\nTitle: %s\nPreview: %s\nCurrent task: %s",
		modelSession.NativeID,
		firstNonEmpty(modelSession.Status, "available"),
		firstNonEmpty(modelSession.Title, modelSession.NativeID),
		firstNonEmpty(modelSession.Preview, "-"),
		firstNonEmpty(session.CurrentTask, "-"),
	)
	return &model.Element{
		SessionID:   modelSession.ID,
		Seq:         1,
		Type:        "message",
		Role:        "assistant",
		StartedAt:   modelSession.UpdatedAt,
		CompletedAt: modelSession.UpdatedAt,
		ContentText: content,
		RawJSON:     session.RawJSON,
	}
}
