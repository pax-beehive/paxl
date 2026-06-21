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

func NewGeminiAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameGemini,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"gemini"},
			Reason:     "Run Gemini CLI locally so ~/.gemini/tmp contains chat history and install the gemini CLI.",
		},
		probe: func() bool {
			return commandExists("gemini") ||
				pathExists(filepath.Join(geminiRootNoError(), "tmp"))
		},
		listSessions: listGeminiSessions,
		getSession:   getGeminiSession,
		prompt:       promptGeminiSession,
		startSession: startGeminiSession,
	}
}

type geminiConversation struct {
	SessionID   string          `json:"sessionId"`
	ProjectHash string          `json:"projectHash"`
	StartTime   string          `json:"startTime"`
	LastUpdated string          `json:"lastUpdated"`
	Messages    []geminiMessage `json:"messages"`
}

type geminiPatchLine struct {
	Set geminiConversation `json:"$set"`
}

type geminiMessage struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Content   geminiContent   `json:"content"`
	Model     string          `json:"model"`
	Raw       json.RawMessage `json:"-"`
}

type geminiContent struct {
	Text string
}

func (c *geminiContent) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		c.Text = text
		return nil
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		c.Text = strings.Join(parts, "\n")
		return nil
	}
	c.Text = string(raw)
	return nil
}

func listGeminiSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	paths, err := geminiSessionPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gemini session paths: %w", err)
	}
	sessions := map[string]*model.Session{}
	for _, path := range paths {
		conversation, err := readGeminiConversation(path)
		if err != nil || conversation.SessionID == "" {
			continue
		}
		session := geminiSession(path, conversation)
		sessions[session.ID] = session
	}
	return sortedSessions(sessions, req), nil
}

func getGeminiSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	path, err := findGeminiSessionPath(ctx, req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("find gemini session: %w", err)
	}
	conversation, err := readGeminiConversation(path)
	if err != nil {
		return nil, fmt.Errorf("read gemini conversation: %w", err)
	}
	return &GetSessionResponse{
		Elements: geminiElements(conversation, "gemini:"+req.NativeID),
	}, nil
}

func promptGeminiSession(
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
		[]string{"gemini", "--resume", req.NativeID, "-p"},
		req.Text,
		option,
	)
}

func startGeminiSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runArgPromptCommand(ctx, []string{"gemini", "-p"}, req.Text, option); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func geminiSessionPaths(ctx context.Context) ([]string, error) {
	root, err := geminiRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve gemini root: %w", err)
	}
	var paths []string
	err = filepath.WalkDir(filepath.Join(root, "tmp"), func(
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
		if entry.IsDir() || filepath.Base(filepath.Dir(path)) != "chats" {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func findGeminiSessionPath(ctx context.Context, nativeID string) (string, error) {
	paths, err := geminiSessionPaths(ctx)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		conversation, err := readGeminiConversation(path)
		if err == nil && conversation.SessionID == nativeID {
			return path, nil
		}
	}
	return "", fmt.Errorf("gemini session %q not found", nativeID)
}

func readGeminiConversation(path string) (*geminiConversation, error) {
	switch filepath.Ext(path) {
	case ".json":
		return readGeminiJSONConversation(path)
	case ".jsonl":
		return readGeminiJSONLConversation(path)
	default:
		return nil, fmt.Errorf("unsupported gemini session file %s", path)
	}
}

func readGeminiJSONConversation(path string) (*geminiConversation, error) {
	// The adapter only reads files discovered under the local Gemini chat root.
	// #nosec G304
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var conversation geminiConversation
	if err := json.Unmarshal(raw, &conversation); err != nil {
		return nil, err
	}
	return &conversation, nil
}

func readGeminiJSONLConversation(path string) (*geminiConversation, error) {
	// The adapter only reads files discovered under the local Gemini chat root.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	conversation := &geminiConversation{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		raw := scanner.Bytes()
		applyGeminiJSONLLine(conversation, raw)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return conversation, nil
}

func applyGeminiJSONLLine(conversation *geminiConversation, raw []byte) {
	var patch geminiPatchLine
	if err := json.Unmarshal(raw, &patch); err == nil {
		mergeGeminiConversation(conversation, &patch.Set)
	}
	var metadata geminiConversation
	if err := json.Unmarshal(raw, &metadata); err == nil {
		mergeGeminiMetadata(conversation, &metadata)
	}
	var message geminiMessage
	if err := json.Unmarshal(raw, &message); err == nil && message.Type != "" {
		message.Raw = append([]byte{}, raw...)
		conversation.Messages = append(conversation.Messages, message)
	}
}

func mergeGeminiConversation(target *geminiConversation, source *geminiConversation) {
	mergeGeminiMetadata(target, source)
	if len(source.Messages) > 0 {
		target.Messages = source.Messages
	}
}

func mergeGeminiMetadata(target *geminiConversation, source *geminiConversation) {
	target.SessionID = firstNonEmpty(source.SessionID, target.SessionID)
	target.ProjectHash = firstNonEmpty(source.ProjectHash, target.ProjectHash)
	target.StartTime = firstNonEmpty(source.StartTime, target.StartTime)
	target.LastUpdated = firstNonEmpty(source.LastUpdated, target.LastUpdated)
}

func geminiSession(path string, conversation *geminiConversation) *model.Session {
	updatedAt := geminiUpdatedAt(conversation)
	return &model.Session{
		ID:         "gemini:" + conversation.SessionID,
		Agent:      model.AgentNameGemini,
		NativeID:   conversation.SessionID,
		Title:      firstNonEmpty(geminiTitle(conversation), conversation.SessionID),
		Status:     "available",
		ProjectID:  geminiProjectRoot(path),
		LastActive: updatedAt,
		UpdatedAt:  updatedAt,
	}
}

func geminiElements(conversation *geminiConversation, sessionID string) []*model.Element {
	var elements []*model.Element
	for _, message := range conversation.Messages {
		role := geminiRole(message.Type)
		content := strings.TrimSpace(message.Content.Text)
		if role == "" || content == "" {
			continue
		}
		elements = append(elements, &model.Element{
			SessionID:   sessionID,
			Seq:         int64(len(elements) + 1),
			Type:        "message",
			Role:        role,
			Model:       message.Model,
			StartedAt:   message.Timestamp,
			CompletedAt: message.Timestamp,
			ContentText: content,
			RawJSON:     string(message.Raw),
		})
	}
	return elements
}

func geminiRole(messageType string) string {
	switch messageType {
	case "user":
		return "user"
	case "gemini":
		return "assistant"
	default:
		return ""
	}
}

func geminiTitle(conversation *geminiConversation) string {
	for _, message := range conversation.Messages {
		if message.Type != "user" {
			continue
		}
		if title := titleCandidate(message.Content.Text); title != "" {
			return title
		}
	}
	return ""
}

func geminiUpdatedAt(conversation *geminiConversation) string {
	updatedAt := conversation.LastUpdated
	for _, message := range conversation.Messages {
		updatedAt = firstNonEmpty(message.Timestamp, updatedAt)
	}
	return firstNonEmpty(updatedAt, conversation.StartTime)
}

func geminiProjectRoot(path string) string {
	projectDir := filepath.Dir(filepath.Dir(path))
	raw, err := os.ReadFile(filepath.Join(projectDir, ".project_root")) // #nosec G304
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func geminiRoot() (string, error) {
	if value := os.Getenv("GEMINI_HOME"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini"), nil
}

func geminiRootNoError() string {
	root, err := geminiRoot()
	if err != nil {
		return ""
	}
	return root
}
