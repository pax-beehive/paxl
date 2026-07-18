package adaptor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func NewKimiAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameKimi,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"kimi"},
			Reason:     "Run Kimi Code locally and install the kimi CLI.",
		},
		cliProbe: func() bool { return commandExists("kimi") },
		sessionProbe: func() bool {
			return pathExists(filepath.Join(kimiRootNoError(), "session_index.jsonl"))
		},
		listSessions: listKimiSessions,
		getSession:   getKimiSession,
		prompt:       promptKimiSession,
		startSession: startKimiSession,
		resume:       nativeSessionResumer("kimi", "--session"),
	}
}

type kimiSessionIndexEntry struct {
	SessionID  string `json:"sessionId"`
	WorkDir    string `json:"workDir"`
	SessionDir string `json:"sessionDir"`
}

type kimiSessionState struct {
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	WorkDir   string `json:"workDir"`
	Title     string `json:"title"`
}

type kimiWireLine struct {
	Type  string `json:"type"`
	Time  int64  `json:"time"`
	Model string `json:"model"`
	Input []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"input"`
	Event struct {
		Type   string `json:"type"`
		TurnID string `json:"turnId"`
		Step   int64  `json:"step"`
		Part   struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"part"`
	} `json:"event"`
}

type kimiAssistantBuilder struct {
	element *model.Element
	parts   []string
}

func listKimiSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	root, err := kimiRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve kimi root: %w", err)
	}
	indexPath := filepath.Join(root, "session_index.jsonl")
	// The path is restricted to Kimi's configured data root.
	// #nosec G304
	file, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ListSessionsResponse{}, nil
		}
		return nil, fmt.Errorf("open kimi session index: %w", err)
	}
	defer closeFile(file)

	sessions := make(map[string]*model.Session)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var entry kimiSessionIndexEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.SessionID == "" {
			continue
		}
		sessionDir := entry.SessionDir
		if !filepath.IsAbs(sessionDir) {
			sessionDir = filepath.Join(root, sessionDir)
		}
		state, err := readKimiSessionState(filepath.Join(sessionDir, "state.json"))
		if err != nil {
			continue
		}
		id := "kimi:" + entry.SessionID
		sessions[id] = &model.Session{
			ID:         id,
			Agent:      model.AgentNameKimi,
			NativeID:   entry.SessionID,
			Title:      firstNonEmpty(state.Title, entry.SessionID),
			Status:     "available",
			ProjectID:  firstNonEmpty(state.WorkDir, entry.WorkDir),
			LastActive: firstNonEmpty(state.UpdatedAt, state.CreatedAt),
			UpdatedAt:  firstNonEmpty(state.UpdatedAt, state.CreatedAt),
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read kimi session index: %w", err)
	}
	return sortedSessions(sessions, req), nil
}

func getKimiSession(
	ctx context.Context,
	req *GetSessionRequest,
) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	sessionDir, err := findKimiSessionDir(ctx, req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("find kimi session: %w", err)
	}
	elements, err := readKimiWire(
		ctx,
		filepath.Join(sessionDir, "agents", "main", "wire.jsonl"),
		"kimi:"+req.NativeID,
	)
	if err != nil {
		return nil, fmt.Errorf("read kimi timeline: %w", err)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func promptKimiSession(
	ctx context.Context,
	req *PromptRequest,
	option *Option,
) (*PromptResponse, error) {
	if req == nil || req.NativeID == "" || req.Text == "" {
		return nil, fmt.Errorf("native session id and prompt text are required")
	}
	if err := validateNativeSessionID(req.NativeID); err != nil {
		return nil, err
	}
	return runArgPromptCommand(
		ctx,
		[]string{"kimi", "--session", req.NativeID, "--prompt"},
		req.Text,
		option,
	)
}

func startKimiSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	_, err := runArgPromptCommand(
		ctx,
		[]string{"kimi", "--prompt"},
		req.Text,
		option,
	)
	if err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func findKimiSessionDir(ctx context.Context, nativeID string) (string, error) {
	root, err := kimiRoot()
	if err != nil {
		return "", err
	}
	// The path is restricted to Kimi's configured data root.
	// #nosec G304
	file, err := os.Open(filepath.Join(root, "session_index.jsonl"))
	if err != nil {
		return "", err
	}
	defer closeFile(file)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var entry kimiSessionIndexEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil ||
			entry.SessionID != nativeID {
			continue
		}
		if filepath.IsAbs(entry.SessionDir) {
			return entry.SessionDir, nil
		}
		return filepath.Join(root, entry.SessionDir), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("session %q not found", nativeID)
}

func readKimiWire(
	ctx context.Context,
	path string,
	sessionID string,
) ([]*model.Element, error) {
	// The adapter only reads the main-agent wire file under a directory from Kimi's index.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeFile(file)

	var elements []*model.Element
	currentModel := ""
	pending := make(map[string]*kimiAssistantBuilder)
	var pendingOrder []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw := append([]byte(nil), scanner.Bytes()...)
		var line kimiWireLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		switch line.Type {
		case "turn.prompt":
			content := kimiInputText(line.Input)
			if content != "" {
				elements = append(elements, &model.Element{
					SessionID:   sessionID,
					Type:        "message",
					Role:        "user",
					StartedAt:   unixMillisTimestamp(line.Time),
					CompletedAt: unixMillisTimestamp(line.Time),
					ContentText: content,
					RawJSON:     string(raw),
				})
			}
		case "llm.request":
			currentModel = firstNonEmpty(line.Model, currentModel)
		case "context.append_loop_event":
			key := fmt.Sprintf("%s:%d", line.Event.TurnID, line.Event.Step)
			switch line.Event.Type {
			case "content.part":
				if line.Event.Part.Type != "text" || line.Event.Part.Text == "" {
					continue
				}
				builder := pending[key]
				if builder == nil {
					builder = &kimiAssistantBuilder{element: &model.Element{
						SessionID: sessionID,
						Type:      "message",
						Role:      "assistant",
						Model:     currentModel,
						StartedAt: unixMillisTimestamp(line.Time),
						RawJSON:   string(raw),
					}}
					pending[key] = builder
					pendingOrder = append(pendingOrder, key)
				}
				builder.parts = append(builder.parts, line.Event.Part.Text)
			case "step.end":
				if builder := pending[key]; builder != nil {
					builder.element.CompletedAt = unixMillisTimestamp(line.Time)
					appendKimiAssistantElement(&elements, builder)
					delete(pending, key)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, key := range pendingOrder {
		if builder := pending[key]; builder != nil {
			builder.element.CompletedAt = builder.element.StartedAt
			appendKimiAssistantElement(&elements, builder)
		}
	}
	for i, element := range elements {
		element.Seq = int64(i + 1)
	}
	return elements, nil
}

func kimiInputText(input []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var parts []string
	for _, part := range input {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func appendKimiAssistantElement(
	elements *[]*model.Element,
	builder *kimiAssistantBuilder,
) {
	builder.element.ContentText = strings.TrimSpace(strings.Join(builder.parts, ""))
	if builder.element.ContentText != "" {
		*elements = append(*elements, builder.element)
	}
}

func readKimiSessionState(path string) (*kimiSessionState, error) {
	// The adapter reads only state files referenced by Kimi's local session index.
	// #nosec G304
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state kimiSessionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func kimiRoot() (string, error) {
	if root := os.Getenv("KIMI_CODE_HOME"); root != "" {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kimi-code"), nil
}

func kimiRootNoError() string {
	root, _ := kimiRoot()
	return root
}
