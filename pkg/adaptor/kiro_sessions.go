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
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

func NewKiroAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameKiro,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"kiro-cli"},
			Reason:     "Run Kiro CLI locally so ~/.kiro/sessions/cli exists and install kiro-cli.",
		},
		cliProbe:     kiroCLIAvailable,
		sessionProbe: kiroSessionsAvailable,
		listSessions: listKiroSessions,
		getSession:   getKiroSession,
		prompt:       promptKiroSession,
		startSession: startKiroSession,
		resume:       nativeSessionResumer("kiro-cli", "chat", "--resume-id"),
	}
}

func kiroCLIAvailable() bool {
	return commandExists("kiro-cli")
}

func kiroSessionsAvailable() bool {
	return pathExists(filepath.Join(kiroRootNoError(), "sessions", "cli"))
}

type kiroSessionMeta struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Title     string `json:"title"`
}

type kiroLogLine struct {
	Kind string       `json:"kind"`
	Data kiroLineData `json:"data"`
}

type kiroLineData struct {
	MessageID string      `json:"message_id"`
	Content   []kiroBlock `json:"content"`
	Meta      struct {
		Timestamp int64 `json:"timestamp"`
	} `json:"meta"`
}

type kiroBlock struct {
	Kind string `json:"kind"`
	Data string `json:"data"`
}

func listKiroSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	paths, err := kiroMetaPaths(ctx)
	if err != nil {
		if os.IsNotExist(err) {
			return &ListSessionsResponse{}, nil
		}
		return nil, fmt.Errorf("list kiro metadata paths: %w", err)
	}
	sessions := map[string]*model.Session{}
	for _, path := range paths {
		session, err := readKiroSession(path)
		if err == nil && session.ID != "" {
			sessions[session.ID] = session
		}
	}
	return sortedSessions(sessions, req), nil
}

func getKiroSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	path, err := findKiroLog(ctx, req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("find kiro log: %w", err)
	}
	elements, err := readKiroElements(path, "kiro:"+req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("read kiro elements: %w", err)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func promptKiroSession(
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
		[]string{"kiro-cli", "chat", "--resume-id", req.NativeID, "--no-interactive"},
		req.Text,
		option,
	)
}

func startKiroSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runArgPromptCommand(
		ctx,
		[]string{"kiro-cli", "chat", "--no-interactive"},
		req.Text,
		option,
	); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func kiroMetaPaths(ctx context.Context) ([]string, error) {
	root, err := kiroRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve kiro root: %w", err)
	}
	var paths []string
	err = filepath.WalkDir(filepath.Join(root, "sessions", "cli"), func(
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
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func findKiroLog(ctx context.Context, nativeID string) (string, error) {
	root, err := kiroRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "sessions", "cli", nativeID+".jsonl")
	if pathExists(path) {
		return path, nil
	}
	metas, err := kiroMetaPaths(ctx)
	if err != nil {
		return "", err
	}
	for _, metaPath := range metas {
		session, err := readKiroSession(metaPath)
		if err == nil && session.NativeID == nativeID {
			return strings.TrimSuffix(metaPath, ".json") + ".jsonl", nil
		}
	}
	return "", fmt.Errorf("kiro log %q not found", nativeID)
}

func readKiroSession(path string) (*model.Session, error) {
	// The adapter only reads metadata files discovered under the local Kiro session root.
	// #nosec G304
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta kiroSessionMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	if meta.SessionID == "" {
		return nil, fmt.Errorf("kiro metadata %s has no session id", path)
	}
	return &model.Session{
		ID:         "kiro:" + meta.SessionID,
		Agent:      model.AgentNameKiro,
		NativeID:   meta.SessionID,
		Title:      firstNonEmpty(meta.Title, meta.SessionID),
		Status:     "available",
		ProjectID:  meta.CWD,
		LastActive: firstNonEmpty(meta.UpdatedAt, meta.CreatedAt),
		UpdatedAt:  firstNonEmpty(meta.UpdatedAt, meta.CreatedAt),
	}, nil
}

func readKiroElements(path string, sessionID string) ([]*model.Element, error) {
	// The adapter only reads log files discovered under the local Kiro session root.
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
		element, ok := decodeKiroElement(scanner.Bytes(), int64(len(elements)+1), sessionID)
		if ok {
			elements = append(elements, element)
		}
	}
	return elements, scanner.Err()
}

func decodeKiroElement(raw []byte, seq int64, sessionID string) (*model.Element, bool) {
	var line kiroLogLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return nil, false
	}
	role := kiroRole(line.Kind)
	if role == "" {
		return nil, false
	}
	timestamp := kiroTimestamp(line.Data.Meta.Timestamp)
	element := &model.Element{
		SessionID:   sessionID,
		Seq:         seq,
		Type:        "message",
		Role:        role,
		StartedAt:   timestamp,
		CompletedAt: timestamp,
		ContentText: kiroContentText(line.Data.Content),
		RawJSON:     string(raw),
	}
	return element, strings.TrimSpace(element.ContentText) != ""
}

func kiroRole(kind string) string {
	switch kind {
	case "Prompt":
		return "user"
	case "AssistantMessage":
		return "assistant"
	default:
		return ""
	}
}

func kiroTimestamp(value int64) string {
	if value == 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func kiroRoot() (string, error) {
	if value := os.Getenv("KIRO_HOME"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kiro"), nil
}

func kiroRootNoError() string {
	root, err := kiroRoot()
	if err != nil {
		return ""
	}
	return root
}

func kiroContentText(blocks []kiroBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Data != "" {
			parts = append(parts, block.Data)
		}
	}
	return strings.Join(parts, "\n")
}
