package adaptor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

const scannerMaxTokenSize = 16 * 1024 * 1024

type codexIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

type codexMetaLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		CWD       string `json:"cwd"`
	} `json:"payload"`
}

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
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

func listCodexSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	_ = ctx
	root, err := codexRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve codex root: %w", err)
	}
	byID, err := readCodexIndex(filepath.Join(root, "session_index.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read codex session index: %w", err)
	}
	if err := readCodexRollouts(ctx, filepath.Join(root, "sessions"), byID); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read codex rollouts: %w", err)
	}
	return sortedSessions(byID, req), nil
}

func listClaudeSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	_ = ctx
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

func listPiSessions(ctx context.Context, req *ListSessionsRequest) (*ListSessionsResponse, error) {
	paths, err := piLogPaths(ctx)
	if err != nil {
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

func listKiroSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	paths, err := kiroMetaPaths(ctx)
	if err != nil {
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

func getCodexSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	_ = ctx
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	path, err := findCodexRollout(req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("find codex rollout: %w", err)
	}
	elements, err := readCodexElements(path, "codex:"+req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("read codex elements: %w", err)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func getClaudeSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	_ = ctx
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

func readCodexIndex(path string) (map[string]*model.Session, error) {
	// The adapter only reads the index path resolved from the local Codex root.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return map[string]*model.Session{}, err
	}
	defer closeFile(file)
	out := map[string]*model.Session{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		var entry codexIndexEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.ID == "" {
			continue
		}
		out["codex:"+entry.ID] = &model.Session{
			ID:         "codex:" + entry.ID,
			Agent:      model.AgentNameCodex,
			NativeID:   entry.ID,
			Title:      entry.ThreadName,
			Status:     "available",
			LastActive: entry.UpdatedAt,
			UpdatedAt:  entry.UpdatedAt,
		}
	}
	return out, scanner.Err()
}

func findCodexRollout(nativeID string) (string, error) {
	root, err := codexRoot()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"sessions", "archived_sessions"} {
		found, err := findCodexRolloutInDir(filepath.Join(root, name), nativeID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if found != "" {
			return found, nil
		}
	}
	return "", fmt.Errorf("codex rollout %q not found", nativeID)
}

func findCodexRolloutInDir(root string, nativeID string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if found != "" || entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), "rollout-") &&
			strings.HasSuffix(entry.Name(), nativeID+".jsonl") {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

func readCodexElements(path string, sessionID string) ([]*model.Element, error) {
	// The adapter only reads rollout files discovered under the local Codex root.
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
		element, ok := decodeCodexElement(scanner.Bytes(), int64(len(elements)+1), sessionID)
		if ok {
			elements = append(elements, element)
		}
	}
	return elements, scanner.Err()
}

func decodeCodexElement(raw []byte, seq int64, sessionID string) (*model.Element, bool) {
	var envelope codexEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Type != "response_item" {
		return nil, false
	}
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(envelope.Payload, &kind); err != nil || kind.Type != "message" {
		return nil, false
	}
	var payload struct {
		Role    string `json:"role"`
		Content []struct {
			Text       string `json:"text"`
			InputText  string `json:"input_text"`
			OutputText string `json:"output_text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, false
	}
	element := &model.Element{
		SessionID:   sessionID,
		Seq:         seq,
		Type:        "message",
		Role:        payload.Role,
		StartedAt:   envelope.Timestamp,
		CompletedAt: envelope.Timestamp,
		ContentText: codexContentText(payload.Content),
		RawJSON:     string(raw),
	}
	return element, strings.TrimSpace(element.ContentText) != ""
}

func codexContentText(blocks []struct {
	Text       string `json:"text"`
	InputText  string `json:"input_text"`
	OutputText string `json:"output_text"`
}) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := firstNonEmpty(block.Text, block.InputText, block.OutputText)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func readCodexRollouts(
	ctx context.Context,
	root string,
	sessions map[string]*model.Session,
) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "rollout-") ||
			!strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		meta, metaErr := readCodexMeta(path)
		if shouldSkipCodexMeta(meta, metaErr) {
			return nil
		}
		id := "codex:" + meta.Payload.ID
		session := sessions[id]
		if session == nil {
			session = &model.Session{ID: id, Agent: model.AgentNameCodex, NativeID: meta.Payload.ID}
		}
		if session.Title == "" {
			session.Title = firstNonEmpty(readCodexTitle(path), entry.Name())
		}
		if session.UpdatedAt == "" {
			session.UpdatedAt = meta.Payload.Timestamp
		}
		session.LastActive = firstNonEmpty(session.LastActive, session.UpdatedAt)
		session.ProjectID = firstNonEmpty(session.ProjectID, meta.Payload.CWD)
		session.Status = firstNonEmpty(session.Status, "available")
		sessions[id] = session
		return nil
	})
}

func readCodexMeta(path string) (*codexMetaLine, error) {
	// The adapter only reads files discovered under the local Codex session root.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		var line codexMetaLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err == nil &&
			line.Type == "session_meta" {
			return &line, nil
		}
	}
	return nil, scanner.Err()
}

func readCodexTitle(path string) string {
	// Rollout filenames are stable identifiers, not useful titles. The first
	// non-bootstrap user message is a better local-only approximation.
	elements, err := readCodexElements(path, "")
	if err != nil {
		return ""
	}
	for _, element := range elements {
		if element != nil && element.Role == "user" {
			if title := titleCandidate(element.ContentText); title != "" {
				return title
			}
		}
	}
	return ""
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

func sortedSessions(
	sessions map[string]*model.Session,
	req *ListSessionsRequest,
) *ListSessionsResponse {
	out := make([]*model.Session, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session)
	}
	sort.Slice(out, func(i int, j int) bool {
		return sessionUpdatedAtAfter(out[i], out[j])
	})
	if req != nil && req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return &ListSessionsResponse{Sessions: out}
}

func sessionUpdatedAtAfter(left *model.Session, right *model.Session) bool {
	leftTime, leftOK := parseSessionTime(left.UpdatedAt)
	rightTime, rightOK := parseSessionTime(right.UpdatedAt)
	if leftOK && rightOK {
		return leftTime.After(rightTime)
	}
	return left.UpdatedAt > right.UpdatedAt
}

func parseSessionTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func codexRoot() (string, error) {
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
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

func claudeMessageText(message *claudeMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, block := range message.Content {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func piMessageText(message *piMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, block := range message.Content {
		text := firstNonEmpty(block.Text, block.Thinking, block.Name)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
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

func titleCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || isNoisyTitleText(value) {
		return ""
	}
	if command := xmlTagValue(value, "command-name"); command != "" {
		return trimOneLine(command, 80)
	}
	return trimOneLine(value, 80)
}

func isNoisyTitleText(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<local-command-caveat>") ||
		strings.HasPrefix(lower, "system_handoff") ||
		strings.HasPrefix(lower, "<environment_context>") ||
		strings.HasPrefix(
			lower,
			"the following is the codex agent history whose request action you are assessing.",
		) ||
		strings.HasPrefix(lower, "assess the exact planned action below.") ||
		strings.HasPrefix(trimmed, "# AGENTS.md instructions for ") ||
		strings.HasPrefix(trimmed, "AGENTS.md instructions for ") ||
		strings.HasPrefix(trimmed, "<INSTRUCTIONS>")
}

func xmlTagValue(value string, tag string) string {
	start := "<" + tag + ">"
	end := "</" + tag + ">"
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		return ""
	}
	contentStart := startIndex + len(start)
	contentEnd := strings.Index(value[contentStart:], end)
	if contentEnd < 0 {
		return ""
	}
	return strings.TrimSpace(value[contentStart : contentStart+contentEnd])
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

func trimOneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func closeFile(file *os.File) {
	_ = file.Close()
}

func shouldSkipCodexMeta(meta *codexMetaLine, err error) bool {
	return err != nil || meta.Payload.ID == ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
