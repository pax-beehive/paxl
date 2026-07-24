package adaptor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

func NewCodexAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameCodex,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"codex"},
			Reason:     "Run Codex locally so ~/.codex/sessions exists and install the codex CLI.",
		},
		cliProbe:     codexCLIAvailable,
		sessionProbe: codexSessionsAvailable,
		listSessions: listCodexSessions,
		getSession:   getCodexSession,
		prompt:       promptCodexSession,
		startSession: startCodexSession,
		resume:       nativeSessionResumer("codex", "resume"),
	}
}

func codexCLIAvailable() bool {
	return commandExists("codex")
}

func codexSessionsAvailable() bool {
	root, err := codexRoot()
	if err != nil {
		return false
	}
	return pathExists(filepath.Join(root, "sessions")) ||
		pathExists(filepath.Join(root, "session_index.jsonl"))
}

type codexIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

type codexMetaLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID             string          `json:"id"`
		SessionID      string          `json:"session_id"`
		ParentThreadID string          `json:"parent_thread_id"`
		Timestamp      string          `json:"timestamp"`
		CWD            string          `json:"cwd"`
		Originator     string          `json:"originator"`
		Source         json.RawMessage `json:"source"`
		ThreadSource   string          `json:"thread_source"`
	} `json:"payload"`
}

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexUserMessageEventLine struct {
	Type    string                  `json:"type"`
	Payload codexUserMessagePayload `json:"payload"`
}

type codexUserMessagePayload struct {
	Type         string          `json:"type"`
	Message      string          `json:"message"`
	TextElements json.RawMessage `json:"text_elements"`
}

func listCodexSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	root, err := codexRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve codex root: %w", err)
	}
	byID, err := readCodexIndex(filepath.Join(root, "session_index.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read codex session index: %w", err)
	}
	if err := readCodexRollouts(
		ctx,
		filepath.Join(root, "sessions"),
		byID,
		req.IncludeSubagents,
	); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read codex rollouts: %w", err)
	}
	return sortedSessions(byID, req), nil
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

func promptCodexSession(
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
	if isCodexAppSession(req.NativeID) {
		resp, err := promptCodexAppServer(ctx, req, option)
		if err == nil {
			return resp, nil
		}
		writeCommandOutput(
			option,
			"stderr",
			"Codex app-server delivery failed; falling back to CLI resume: "+err.Error(),
		)
	}
	return runPromptCommand(
		ctx,
		[]string{"codex", "exec", "resume", "--all", req.NativeID, "-"},
		req.Text,
		option,
	)
}

func startCodexSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := runPromptCommand(
		ctx,
		[]string{"codex", "exec", "-"},
		req.Text,
		option,
	); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
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
	includeSubagents bool,
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
		if !includeSubagents && meta.Payload.ThreadSource == "subagent" {
			// Index entries do not expose the thread source, so remove a matching
			// cached entry once its rollout identifies it as an internal thread.
			delete(sessions, id)
			return nil
		}
		session := sessions[id]
		if session == nil {
			session = &model.Session{ID: id, Agent: model.AgentNameCodex, NativeID: meta.Payload.ID}
		}
		if session.Title == "" {
			session.Title = firstNonEmpty(
				codexIndexedThreadTitle(sessions, meta),
				readCodexTitle(path),
				sessionProjectTitle(meta.Payload.CWD),
				meta.Payload.ID,
			)
		}
		activityAt, activityErr := readCodexLatestTimestamp(path)
		if activityErr != nil {
			return activityErr
		}
		session.UpdatedAt = laterCodexTimestamp(
			session.UpdatedAt,
			firstNonEmpty(activityAt, meta.Payload.Timestamp),
		)
		session.LastActive = laterCodexTimestamp(session.LastActive, session.UpdatedAt)
		session.ProjectID = firstNonEmpty(session.ProjectID, meta.Payload.CWD)
		session.Status = firstNonEmpty(session.Status, "available")
		sessions[id] = session
		return nil
	})
}

func readCodexLatestTimestamp(path string) (string, error) {
	// Rollouts can be very large, so inspect complete lines from the tail
	// instead of rescanning the whole transcript for every session listing.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer closeFile(file)
	info, err := file.Stat()
	if err != nil {
		return "", err
	}

	const blockSize int64 = 64 * 1024
	offset := info.Size()
	var suffix []byte
	for offset > 0 {
		readSize := min(blockSize, offset)
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := file.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		data := make([]byte, 0, len(chunk)+len(suffix))
		data = append(data, chunk...)
		data = append(data, suffix...)
		lines := bytes.Split(data, []byte{'\n'})
		firstCompleteLine := 0
		if offset > 0 {
			firstCompleteLine = 1
		}
		for index := len(lines) - 1; index >= firstCompleteLine; index-- {
			if timestamp := codexLineTimestamp(lines[index]); timestamp != "" {
				return timestamp, nil
			}
		}
		suffix = append(suffix[:0], lines[0]...)
	}
	return "", nil
}

func codexLineTimestamp(line []byte) string {
	if len(bytes.TrimSpace(line)) == 0 {
		return ""
	}
	var envelope codexEnvelope
	if err := json.Unmarshal(line, &envelope); err == nil && envelope.Timestamp != "" {
		return envelope.Timestamp
	}
	var meta codexMetaLine
	if err := json.Unmarshal(line, &meta); err == nil && meta.Type == "session_meta" {
		return meta.Payload.Timestamp
	}
	return ""
}

func laterCodexTimestamp(left string, right string) string {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	leftTime, leftErr := time.Parse(time.RFC3339Nano, left)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, right)
	if leftErr == nil && rightErr == nil {
		if rightTime.After(leftTime) {
			return right
		}
		return left
	}
	if right > left {
		return right
	}
	return left
}

func codexIndexedThreadTitle(
	sessions map[string]*model.Session,
	meta *codexMetaLine,
) string {
	if meta == nil {
		return ""
	}
	for _, id := range []string{meta.Payload.ParentThreadID, meta.Payload.SessionID} {
		if id == "" || id == meta.Payload.ID {
			continue
		}
		if session := sessions["codex:"+id]; session != nil {
			return session.Title
		}
	}
	return ""
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
	if title := readCodexUserMessageEventTitle(path); title != "" {
		return title
	}
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

func readCodexUserMessageEventTitle(path string) string {
	// Codex event messages represent user-visible input more directly than
	// response_item messages, which may include injected model context.
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer closeFile(file)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		var line codexUserMessageEventLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil ||
			line.Type != "event_msg" ||
			line.Payload.Type != "user_message" {
			continue
		}
		if title := titleCandidate(codexUserMessageText(&line.Payload)); title != "" {
			return title
		}
	}
	return ""
}

func codexUserMessageText(payload *codexUserMessagePayload) string {
	if payload == nil {
		return ""
	}
	if strings.TrimSpace(payload.Message) != "" {
		return payload.Message
	}
	var texts []string
	if err := json.Unmarshal(payload.TextElements, &texts); err == nil {
		return strings.Join(texts, "\n")
	}
	var elements []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(payload.TextElements, &elements); err != nil {
		return ""
	}
	for _, element := range elements {
		text := firstNonEmpty(element.Text, element.Content)
		if text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
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

func shouldSkipCodexMeta(meta *codexMetaLine, err error) bool {
	return err != nil || meta.Payload.ID == ""
}
