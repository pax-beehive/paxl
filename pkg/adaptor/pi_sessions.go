package adaptor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
)

func NewPiAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNamePi,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"pi"},
			Reason:     "Run Pi locally so ~/.pi/agent/sessions exists and install the pi CLI.",
		},
		cliProbe:     piCLIAvailable,
		sessionProbe: piSessionsAvailable,
		listSessions: listPiSessions,
		getSession:   getPiSession,
		prompt:       promptPiSession,
		startSession: startPiSession,
	}
}

func piCLIAvailable() bool {
	return commandExists("pi")
}

func piSessionsAvailable() bool {
	return pathExists(filepath.Join(piRootNoError(), "sessions"))
}

type piLogLine struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Timestamp string    `json:"timestamp"`
	CWD       string    `json:"cwd"`
	Message   piMessage `json:"message"`
}

type piMessage struct {
	Role    string    `json:"role"`
	Model   string    `json:"model"`
	Content []piBlock `json:"content"`
}

type piBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	Name     string `json:"name"`
}

func listPiSessions(ctx context.Context, req *ListSessionsRequest) (*ListSessionsResponse, error) {
	paths, err := piLogPaths(ctx)
	if err != nil {
		if os.IsNotExist(err) {
			return &ListSessionsResponse{}, nil
		}
		return nil, fmt.Errorf("list pi log paths: %w", err)
	}
	sessions := map[string]*model.Session{}
	for _, path := range paths {
		session, err := readPiSession(path)
		if err == nil && session.ID != "" {
			sessions[session.ID] = session
		}
	}
	return sortedSessions(sessions, req), nil
}

func getPiSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	path, err := findPiLog(ctx, req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("find pi log: %w", err)
	}
	elements, err := readPiElements(path, "pi:"+req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("read pi elements: %w", err)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func promptPiSession(
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
	return runPromptCommand(
		ctx,
		[]string{"pi", "--session", req.NativeID, "-p"},
		req.Text,
		option,
	)
}

func startPiSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runPromptCommand(ctx, []string{"pi", "-p"}, req.Text, option); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func piLogPaths(ctx context.Context) ([]string, error) {
	root, err := piRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve pi root: %w", err)
	}
	var paths []string
	err = filepath.WalkDir(filepath.Join(root, "sessions"), func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func findPiLog(ctx context.Context, nativeID string) (string, error) {
	paths, err := piLogPaths(ctx)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		session, err := readPiSession(path)
		if err == nil && session.NativeID == nativeID {
			return path, nil
		}
	}
	return "", fmt.Errorf("pi log %q not found", nativeID)
}

func readPiSession(path string) (*model.Session, error) {
	// The adapter only reads files discovered under the local Pi session root.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	var sessionLine piLogLine
	var lastTimed piLogLine
	var title string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		var line piLogLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if line.Type == "session" && line.ID != "" {
			sessionLine = line
		}
		if line.Timestamp != "" {
			lastTimed = line
		}
		if title == "" && line.Type == "message" && line.Message.Role == "user" {
			title = titleCandidate(piMessageText(&line.Message))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if sessionLine.ID == "" {
		return nil, fmt.Errorf("pi log %s has no session id", path)
	}
	updatedAt := firstNonEmpty(lastTimed.Timestamp, sessionLine.Timestamp)
	return &model.Session{
		ID:         "pi:" + sessionLine.ID,
		Agent:      model.AgentNamePi,
		NativeID:   sessionLine.ID,
		Title:      firstNonEmpty(title, sessionLine.ID),
		Status:     "available",
		ProjectID:  sessionLine.CWD,
		LastActive: updatedAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func readPiElements(path string, sessionID string) ([]*model.Element, error) {
	// The adapter only reads log files discovered under the local Pi session root.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	var elements []*model.Element
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		element, ok := decodePiElement(scanner.Bytes(), int64(len(elements)+1), sessionID)
		if ok {
			elements = append(elements, element)
		}
	}
	return elements, scanner.Err()
}

func decodePiElement(raw []byte, seq int64, sessionID string) (*model.Element, bool) {
	var line piLogLine
	if err := json.Unmarshal(raw, &line); err != nil || line.Type != "message" {
		return nil, false
	}
	if line.Message.Role == "" {
		return nil, false
	}
	element := &model.Element{
		SessionID:   sessionID,
		Seq:         seq,
		Type:        "message",
		Role:        line.Message.Role,
		Model:       line.Message.Model,
		StartedAt:   line.Timestamp,
		CompletedAt: line.Timestamp,
		ContentText: piMessageText(&line.Message),
		RawJSON:     string(raw),
	}
	return element, strings.TrimSpace(element.ContentText) != ""
}

func piRoot() (string, error) {
	if value := os.Getenv("PI_CODING_AGENT_DIR"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

func piRootNoError() string {
	root, err := piRoot()
	if err != nil {
		return ""
	}
	return root
}

func piMessageText(message *piMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, block := range message.Content {
		text := firstNonEmpty(block.Text, block.Name)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
