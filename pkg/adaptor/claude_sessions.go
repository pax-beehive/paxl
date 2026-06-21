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

func NewClaudeAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameClaude,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"claude"},
			Reason:     "Run Claude Code locally so ~/.claude/projects exists and install the claude CLI.",
		},
		probe: func() bool {
			return commandExists("claude") ||
				pathExists(filepath.Join(homeDir(), ".claude", "projects"))
		},
		listSessions: listClaudeSessions,
		getSession:   getClaudeSession,
		prompt:       promptClaudeSession,
		startSession: startClaudeSession,
	}
}

type claudeLogLine struct {
	Type      string        `json:"type"`
	SessionID string        `json:"sessionId"`
	Timestamp string        `json:"timestamp"`
	CWD       string        `json:"cwd"`
	Message   claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role    string        `json:"role"`
	Model   string        `json:"model"`
	Content claudeContent `json:"content"`
}

type claudeContent []claudeContentBlock

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *claudeContent) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*c = []claudeContentBlock{{Type: "text", Text: text}}
		return nil
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		*c = blocks
		return nil
	}
	*c = []claudeContentBlock{{Type: "raw", Text: string(raw)}}
	return nil
}

func listClaudeSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	paths, err := claudeLogPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("list claude log paths: %w", err)
	}
	sessions := map[string]*model.Session{}
	for _, path := range paths {
		session, err := readClaudeSession(path)
		if err == nil && session.ID != "" {
			sessions[session.ID] = session
		}
	}
	return sortedSessions(sessions, req), nil
}

func getClaudeSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	path, err := findClaudeLog(ctx, req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("find claude log: %w", err)
	}
	elements, err := readClaudeElements(path, "claude:"+req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("read claude elements: %w", err)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func promptClaudeSession(
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
		[]string{"claude", "--print", "--resume", req.NativeID},
		req.Text,
		option,
	)
}

func startClaudeSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runPromptCommand(
		ctx,
		[]string{"claude", "--print"},
		req.Text,
		option,
	); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func claudeLogPaths(ctx context.Context) ([]string, error) {
	root, err := claudeRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve claude root: %w", err)
	}
	var paths []string
	err = filepath.WalkDir(
		filepath.Join(root, "projects"),
		func(path string, entry fs.DirEntry, walkErr error) error {
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
		},
	)
	return paths, err
}

func readClaudeSession(path string) (*model.Session, error) {
	// The adapter only reads files discovered under the local Claude projects root.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	var first, lastTimed claudeLogLine
	var title string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		var line claudeLogLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil || line.SessionID == "" {
			continue
		}
		if first.SessionID == "" {
			first = line
		}
		if line.Timestamp != "" {
			lastTimed = line
		}
		if title == "" && line.Type == "user" {
			title = titleCandidate(claudeMessageText(&line.Message))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if first.SessionID == "" {
		return nil, fmt.Errorf("claude log %s has no session id", path)
	}
	updatedAt := firstNonEmpty(lastTimed.Timestamp, first.Timestamp)
	return &model.Session{
		ID:         "claude:" + first.SessionID,
		Agent:      model.AgentNameClaude,
		NativeID:   first.SessionID,
		Title:      firstNonEmpty(title, first.SessionID),
		Status:     "available",
		ProjectID:  firstNonEmpty(lastTimed.CWD, first.CWD, claudeProjectFromPath(path)),
		LastActive: updatedAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func findClaudeLog(ctx context.Context, nativeID string) (string, error) {
	paths, err := claudeLogPaths(ctx)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		if strings.TrimSuffix(filepath.Base(path), ".jsonl") == nativeID {
			return path, nil
		}
	}
	return "", fmt.Errorf("claude log %q not found", nativeID)
}

func readClaudeElements(path string, sessionID string) ([]*model.Element, error) {
	// The adapter only reads log files discovered under the local Claude projects root.
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
		element, ok := decodeClaudeElement(scanner.Bytes(), int64(len(elements)+1), sessionID)
		if ok {
			elements = append(elements, element)
		}
	}
	return elements, scanner.Err()
}

func decodeClaudeElement(raw []byte, seq int64, sessionID string) (*model.Element, bool) {
	var line claudeLogLine
	if err := json.Unmarshal(raw, &line); err != nil || line.SessionID == "" {
		return nil, false
	}
	if line.Type != "user" && line.Type != "assistant" {
		return nil, false
	}
	element := &model.Element{
		SessionID:   sessionID,
		Seq:         seq,
		Type:        "message",
		Role:        firstNonEmpty(line.Message.Role, line.Type),
		Model:       line.Message.Model,
		StartedAt:   line.Timestamp,
		CompletedAt: line.Timestamp,
		ContentText: claudeMessageText(&line.Message),
		RawJSON:     string(raw),
	}
	return element, strings.TrimSpace(element.ContentText) != ""
}

func claudeRoot() (string, error) {
	if value := os.Getenv("CLAUDE_HOME"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func claudeMessageText(message *claudeMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, block := range message.Content {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func claudeProjectFromPath(path string) string {
	encoded := filepath.Base(filepath.Dir(path))
	if encoded == "" || encoded == "." || encoded == "projects" {
		return ""
	}
	absolute := strings.HasPrefix(encoded, "-")
	segments := strings.Split(strings.TrimPrefix(encoded, "-"), "-")
	if absolute {
		return firstNonEmpty(
			resolveExistingEncodedPath(string(os.PathSeparator), segments),
			decodeClaudeProjectPath(encoded),
		)
	}
	return decodeClaudeProjectPath(encoded)
}

func resolveExistingEncodedPath(root string, segments []string) string {
	current := root
	for index := 0; index < len(segments); {
		next, consumed := longestExistingPathComponent(current, segments[index:])
		if next == "" {
			return ""
		}
		current = next
		index += consumed
	}
	return current
}

func longestExistingPathComponent(current string, segments []string) (string, int) {
	for width := len(segments); width > 0; width-- {
		for _, component := range encodedPathComponentCandidates(segments[:width]) {
			candidate := filepath.Join(current, component)
			if pathExists(candidate) {
				return candidate, width
			}
		}
	}
	return "", 0
}

func encodedPathComponentCandidates(segments []string) []string {
	return []string{
		strings.Join(segments, "-"),
		strings.Join(segments, " "),
	}
}

func decodeClaudeProjectPath(encoded string) string {
	project := strings.TrimPrefix(encoded, "-")
	if project == "" {
		return ""
	}
	decoded := strings.ReplaceAll(project, "-", string(os.PathSeparator))
	if strings.HasPrefix(encoded, "-") {
		return string(os.PathSeparator) + decoded
	}
	return decoded
}
