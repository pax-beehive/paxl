package adaptor

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	_ "modernc.org/sqlite"
)

const defaultHermesURL = "http://localhost:8642"

var errHermesUnavailable = errors.New("hermes unavailable")
var errHermesLocalNotFound = errors.New("hermes local session not found")
var hermesACPCommand = []string{"hermes", "acp"}
var hermesHTTPClient = &http.Client{Timeout: 5 * time.Minute}

func NewHermesAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameHermes,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityGateway,
			Command:    []string{"hermes"},
			Reason:     "Run Hermes locally with its HTTP API server available.",
		},
		probe:        hermesOnlineAvailable,
		cliProbe:     hermesCLIAvailable,
		sessionProbe: hermesSessionsAvailable,
		listSessions: listHermesSessions,
		getSession:   getHermesSession,
		prompt:       promptHermesSession,
		startSession: startHermesSession,
	}
}

func hermesCLIAvailable() bool {
	if len(hermesACPCommand) == 0 {
		return false
	}
	return commandExists(hermesACPCommand[0])
}

func hermesSessionsAvailable() bool {
	return hermesStateDBAvailable() || hermesLocalSessionsAvailable() || hermesCLIAvailable()
}

func hermesOnlineAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if hermesACPAvailable(ctx) {
		return true
	}
	return hermesHealth(ctx) == nil
}

type hermesSessionInfo struct {
	SessionID      string   `json:"sessionId"`
	AgentType      string   `json:"agentType"`
	NativeID       string   `json:"nativeId"`
	Name           string   `json:"name"`
	ProjectID      string   `json:"projectId"`
	LastActive     string   `json:"lastActive"`
	Preview        string   `json:"preview"`
	WorkspaceRoots []string `json:"workspaceRoots"`
	Status         string   `json:"status"`
	CurrentTask    string   `json:"currentTask"`
	TokenUsage     int64    `json:"tokenUsage"`
	UpdatedAt      string   `json:"updatedAt"`
	RawJSON        string   `json:"-"`
}

type hermesChatRequest struct {
	Model    string              `json:"model"`
	Messages []hermesChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type hermesChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func listHermesSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	stateDB, err := listHermesStateDBSessions(ctx, req)
	if err == nil {
		if len(stateDB.Sessions) > 0 || hermesStateDBAvailable() {
			return stateDB, nil
		}
	} else if !errors.Is(err, errHermesLocalNotFound) {
		return nil, fmt.Errorf("list hermes state database sessions: %w", err)
	}
	local, err := listHermesLocalSessions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("list local hermes sessions: %w", err)
	}
	if len(local.Sessions) > 0 || hermesLocalSessionsAvailable() {
		return local, nil
	}
	acp, err := listHermesACPSessions(ctx, req)
	if err == nil {
		return acp, nil
	}
	var sessions []hermesSessionInfo
	if err := hermesJSON(ctx, http.MethodGet, "/api/sessions", nil, &sessions, ""); err != nil {
		if errors.Is(err, errHermesUnavailable) {
			return &ListSessionsResponse{}, nil
		}
		return nil, fmt.Errorf("list hermes sessions: %w", err)
	}
	byID := make(map[string]*model.Session, len(sessions))
	for _, session := range sessions {
		modelSession := hermesModelSession(&session)
		if modelSession.ID != "" {
			byID[modelSession.ID] = modelSession
		}
	}
	return sortedSessions(byID, req), nil
}

func getHermesSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	stateDB, err := getHermesStateDBSession(ctx, req.NativeID)
	if err == nil {
		return stateDB, nil
	}
	if err != nil && !errors.Is(err, errHermesLocalNotFound) {
		return nil, fmt.Errorf("get hermes state database session %s: %w", req.NativeID, err)
	}
	local, err := getHermesLocalSession(ctx, req.NativeID)
	if err == nil {
		return local, nil
	}
	if err != nil && !errors.Is(err, errHermesLocalNotFound) {
		return nil, fmt.Errorf("get local hermes session %s: %w", req.NativeID, err)
	}
	var session hermesSessionInfo
	path := "/api/sessions/" + url.PathEscape(req.NativeID)
	if err := hermesJSON(ctx, http.MethodGet, path, nil, &session, ""); err != nil {
		return nil, fmt.Errorf("get hermes session %s: %w", req.NativeID, err)
	}
	element := hermesSessionElement(&session)
	return &GetSessionResponse{Elements: []*model.Element{element}}, nil
}

func promptHermesSession(
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
	if err := promptHermesACPSession(ctx, req.NativeID, req.Text); err == nil {
		return &PromptResponse{DeliveryMethod: "acp_session_prompt"}, nil
	}
	return postHermesPrompt(ctx, req.Text, req.NativeID)
}

func startHermesSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	_ = option
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	if _, err := postHermesPrompt(ctx, req.Text, ""); err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func hermesHealth(ctx context.Context) error {
	return hermesJSON(ctx, http.MethodGet, "/health", nil, nil, "")
}

func hermesACPAvailable(ctx context.Context) bool {
	if !hermesCLIAvailable() {
		return false
	}
	client := hermesACPClient(2 * time.Second)
	return client.initialize(ctx) == nil
}

func listHermesACPSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	if !hermesCLIAvailable() {
		return nil, errHermesUnavailable
	}
	client := hermesACPClient(10 * time.Second)
	sessions, err := client.listSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list hermes acp sessions: %w", err)
	}
	byID := make(map[string]*model.Session, len(sessions))
	for _, session := range sessions {
		modelSession := hermesModelSession(&session)
		if modelSession.ID != "" {
			byID[modelSession.ID] = modelSession
		}
	}
	return sortedSessions(byID, req), nil
}

func promptHermesACPSession(ctx context.Context, nativeID string, text string) error {
	if !hermesCLIAvailable() {
		return errHermesUnavailable
	}
	client := hermesACPClient(30 * time.Second)
	return client.prompt(ctx, nativeID, text)
}

func hermesACPClient(timeout time.Duration) *acpClient {
	command := append([]string{}, hermesACPCommand...)
	return &acpClient{
		command: command,
		timeout: timeout,
	}
}

func decodeHermesACPSession(raw json.RawMessage) hermesSessionInfo {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return hermesSessionInfo{}
	}
	workspaceRoots := stringSliceMapValue(fields, "workspaceRoots", "workspace_roots")
	cwd := stringMapValue(fields, "cwd")
	if len(workspaceRoots) == 0 && cwd != "" {
		workspaceRoots = []string{cwd}
	}
	return hermesSessionInfo{
		SessionID: firstNonEmpty(
			stringMapValue(fields, "sessionId", "session_id"),
			stringMapValue(fields, "id"),
			stringMapValue(fields, "nativeId", "native_id"),
		),
		AgentType: firstNonEmpty(stringMapValue(fields, "agentType", "agent_type"), "hermes"),
		NativeID: firstNonEmpty(
			stringMapValue(fields, "nativeId", "native_id"),
			stringMapValue(fields, "sessionId", "session_id"),
			stringMapValue(fields, "id"),
		),
		Name: firstNonEmpty(
			stringMapValue(fields, "name"),
			stringMapValue(fields, "title"),
		),
		ProjectID: firstNonEmpty(
			stringMapValue(fields, "projectId", "project_id"),
			cwd,
		),
		LastActive: firstNonEmpty(
			timestampMapValue(fields, "lastActive", "last_active"),
			timestampMapValue(fields, "timestamp"),
		),
		Preview:        stringMapValue(fields, "preview"),
		WorkspaceRoots: workspaceRoots,
		Status:         stringMapValue(fields, "status"),
		CurrentTask:    stringMapValue(fields, "currentTask", "current_task"),
		UpdatedAt: firstNonEmpty(
			timestampMapValue(fields, "updatedAt", "updated_at"),
			timestampMapValue(fields, "lastUpdated", "last_updated"),
			timestampMapValue(fields, "lastActive", "last_active"),
			timestampMapValue(fields, "timestamp"),
		),
		RawJSON: string(raw),
	}
}

type hermesLocalSession struct {
	info     hermesSessionInfo
	elements []*model.Element
}

func listHermesStateDBSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	db, err := openHermesStateDB()
	if err != nil {
		return nil, err
	}
	defer closeDB(db)
	rows, err := db.QueryContext(ctx, `
SELECT
	s.id,
	s.source,
	s.model,
	s.title,
	s.cwd,
	s.started_at,
	s.ended_at,
	s.message_count,
	s.input_tokens,
	s.output_tokens,
	(
		SELECT m.content
		FROM messages m
		WHERE m.session_id = s.id
			AND COALESCE(m.active, 1) = 1
			AND TRIM(COALESCE(m.content, '')) != ''
		ORDER BY m.timestamp, m.id
		LIMIT 1
	) AS preview,
	(
		SELECT MAX(m.timestamp)
		FROM messages m
		WHERE m.session_id = s.id
			AND COALESCE(m.active, 1) = 1
	) AS last_message_at
FROM sessions s
WHERE COALESCE(s.archived, 0) = 0`)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer closeRows(rows)
	sessions := map[string]*model.Session{}
	for rows.Next() {
		info, err := scanHermesStateSession(rows)
		if err != nil {
			return nil, err
		}
		session := hermesModelSession(info)
		if session.ID != "" {
			sessions[session.ID] = session
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sortedSessions(sessions, req), nil
}

func getHermesStateDBSession(ctx context.Context, nativeID string) (*GetSessionResponse, error) {
	db, err := openHermesStateDB()
	if err != nil {
		return nil, err
	}
	defer closeDB(db)
	var exists int
	if err := db.QueryRowContext(
		ctx,
		`SELECT 1 FROM sessions WHERE id = ? LIMIT 1`,
		nativeID,
	).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errHermesLocalNotFound
		}
		return nil, fmt.Errorf("query session: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, role, content, timestamp, token_count
FROM messages
WHERE session_id = ?
	AND COALESCE(active, 1) = 1
ORDER BY timestamp, id`, nativeID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer closeRows(rows)
	var elements []*model.Element
	for rows.Next() {
		element, err := scanHermesStateElement(rows, nativeID, int64(len(elements)+1))
		if err != nil {
			return nil, err
		}
		elements = append(elements, element)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func scanHermesStateSession(rows *sql.Rows) (*hermesSessionInfo, error) {
	var (
		id            string
		source        string
		modelName     sql.NullString
		title         sql.NullString
		cwd           sql.NullString
		startedAt     float64
		endedAt       sql.NullFloat64
		messageCount  sql.NullInt64
		inputTokens   sql.NullInt64
		outputTokens  sql.NullInt64
		preview       sql.NullString
		lastMessageAt sql.NullFloat64
	)
	if err := rows.Scan(
		&id,
		&source,
		&modelName,
		&title,
		&cwd,
		&startedAt,
		&endedAt,
		&messageCount,
		&inputTokens,
		&outputTokens,
		&preview,
		&lastMessageAt,
	); err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}
	updatedAt := hermesNumericTimestamp(startedAt)
	if endedAt.Valid {
		updatedAt = hermesNumericTimestamp(endedAt.Float64)
	}
	if lastMessageAt.Valid {
		updatedAt = hermesNumericTimestamp(lastMessageAt.Float64)
	}
	raw := map[string]any{
		"id":             id,
		"source":         source,
		"model":          nullStringValue(modelName),
		"title":          nullStringValue(title),
		"cwd":            nullStringValue(cwd),
		"started_at":     startedAt,
		"message_count":  nullInt64Value(messageCount),
		"input_tokens":   nullInt64Value(inputTokens),
		"output_tokens":  nullInt64Value(outputTokens),
		"last_active":    updatedAt,
		"preview":        nullStringValue(preview),
		"hermes_storage": "state.db",
	}
	rawJSON, _ := json.Marshal(raw)
	return &hermesSessionInfo{
		SessionID:  id,
		NativeID:   id,
		AgentType:  "hermes",
		Name:       firstNonEmpty(nullStringValue(title), titleCandidate(nullStringValue(preview))),
		ProjectID:  nullStringValue(cwd),
		LastActive: updatedAt,
		Preview:    titleCandidate(nullStringValue(preview)),
		Status:     "available",
		TokenUsage: nullInt64Value(inputTokens) + nullInt64Value(outputTokens),
		UpdatedAt:  updatedAt,
		RawJSON:    string(rawJSON),
	}, nil
}

func scanHermesStateElement(rows *sql.Rows, nativeID string, seq int64) (*model.Element, error) {
	var (
		id         int64
		role       string
		content    sql.NullString
		timestamp  float64
		tokenCount sql.NullInt64
	)
	if err := rows.Scan(&id, &role, &content, &timestamp, &tokenCount); err != nil {
		return nil, fmt.Errorf("scan message: %w", err)
	}
	raw := map[string]any{
		"id":          id,
		"session_id":  nativeID,
		"role":        role,
		"content":     nullStringValue(content),
		"timestamp":   timestamp,
		"token_count": nullInt64Value(tokenCount),
	}
	rawJSON, _ := json.Marshal(raw)
	startedAt := hermesNumericTimestamp(timestamp)
	return &model.Element{
		SessionID:   "hermes:" + nativeID,
		Seq:         seq,
		Type:        "message",
		Role:        role,
		StartedAt:   startedAt,
		CompletedAt: startedAt,
		ContentText: strings.TrimSpace(nullStringValue(content)),
		RawJSON:     string(rawJSON),
	}, nil
}

func openHermesStateDB() (*sql.DB, error) {
	path, err := hermesStateDBPath()
	if err != nil {
		return nil, err
	}
	if !pathExists(path) {
		return nil, errHermesLocalNotFound
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	return db, nil
}

func hermesStateDBAvailable() bool {
	path, err := hermesStateDBPath()
	return err == nil && pathExists(path)
}

func hermesStateDBPath() (string, error) {
	root, err := hermesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state.db"), nil
}

func listHermesLocalSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	paths, err := hermesLocalSessionPaths(ctx)
	if err != nil {
		return nil, err
	}
	sessions := map[string]*model.Session{}
	for _, path := range paths {
		local, err := readHermesLocalSession(path)
		if err != nil {
			continue
		}
		session := hermesModelSession(&local.info)
		if session.ID != "" {
			sessions[session.ID] = session
		}
	}
	return sortedSessions(sessions, req), nil
}

func getHermesLocalSession(ctx context.Context, nativeID string) (*GetSessionResponse, error) {
	paths, err := hermesLocalSessionPaths(ctx)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		local, err := readHermesLocalSession(path)
		if err != nil {
			continue
		}
		if firstNonEmpty(local.info.SessionID, local.info.NativeID) == nativeID {
			return &GetSessionResponse{Elements: local.elements}, nil
		}
	}
	return nil, errHermesLocalNotFound
}

func hermesLocalSessionsAvailable() bool {
	for _, path := range hermesCandidateSessionDirs() {
		if pathExists(path) {
			return true
		}
	}
	return false
}

func hermesLocalSessionPaths(ctx context.Context) ([]string, error) {
	root, err := hermesRoot()
	if err != nil {
		return nil, err
	}
	if !pathExists(root) {
		return nil, nil
	}
	var paths []string
	for _, dir := range hermesCandidateSessionDirs() {
		if !pathExists(dir) {
			continue
		}
		found, err := hermesJSONPaths(ctx, dir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, found...)
	}
	if len(paths) == 0 {
		found, err := hermesJSONPaths(ctx, root)
		if err != nil {
			return nil, err
		}
		paths = append(paths, found...)
	}
	return paths, nil
}

func hermesCandidateSessionDirs() []string {
	root := hermesRootNoError()
	return []string{
		filepath.Join(root, "sessions"),
		filepath.Join(root, "conversations"),
		filepath.Join(root, "history"),
		filepath.Join(root, "chats"),
	}
}

func hermesJSONPaths(ctx context.Context, root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "paxl" && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if isHermesRoutingIndexPath(path) {
			return nil
		}
		switch filepath.Ext(entry.Name()) {
		case ".json", ".jsonl":
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func isHermesRoutingIndexPath(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "sessions" && filepath.Base(path) == "sessions.json"
}

func readHermesLocalSession(path string) (*hermesLocalSession, error) {
	local := &hermesLocalSession{}
	if filepath.Ext(path) == ".jsonl" {
		if err := readHermesLocalJSONL(path, local); err != nil {
			return nil, err
		}
	} else {
		if err := readHermesLocalJSON(path, local); err != nil {
			return nil, err
		}
	}
	finalizeHermesLocalSession(local, path)
	return local, nil
}

func readHermesLocalJSONL(path string, local *hermesLocalSession) error {
	file, err := os.Open(path) // #nosec G304
	if err != nil {
		return err
	}
	defer closeFile(file)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scannerMaxTokenSize)
	for scanner.Scan() {
		var line map[string]any
		raw := append([]byte(nil), scanner.Bytes()...)
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		mergeHermesLocalMetadata(&local.info, line, raw)
		appendHermesLocalMessage(local, line, raw)
		appendHermesLocalMessages(local, line, raw)
	}
	return scanner.Err()
}

func readHermesLocalJSON(path string, local *hermesLocalSession) error {
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil {
		mergeHermesLocalMetadata(&local.info, object, raw)
		appendHermesLocalMessage(local, object, raw)
		appendHermesLocalMessages(local, object, raw)
		return nil
	}
	var lines []map[string]any
	if err := json.Unmarshal(raw, &lines); err != nil {
		return err
	}
	for _, line := range lines {
		lineRaw, _ := json.Marshal(line)
		mergeHermesLocalMetadata(&local.info, line, lineRaw)
		appendHermesLocalMessage(local, line, lineRaw)
	}
	return nil
}

func mergeHermesLocalMetadata(info *hermesSessionInfo, fields map[string]any, raw []byte) {
	info.SessionID = firstNonEmpty(
		info.SessionID,
		stringMapValue(fields, "sessionId", "sessionID", "session_id", "id"),
	)
	info.NativeID = firstNonEmpty(info.NativeID, stringMapValue(fields, "nativeId", "nativeID"))
	info.Name = firstNonEmpty(info.Name, stringMapValue(fields, "title", "name", "threadName"))
	info.ProjectID = firstNonEmpty(
		info.ProjectID,
		stringMapValue(fields, "projectId", "projectID", "cwd"),
	)
	info.Preview = firstNonEmpty(info.Preview, stringMapValue(fields, "preview", "summary"))
	info.Status = firstNonEmpty(info.Status, stringMapValue(fields, "status"))
	info.LastActive = firstNonEmpty(
		info.LastActive,
		timestampMapValue(fields, "lastActive", "last_active", "timestamp"),
	)
	info.UpdatedAt = firstNonEmpty(
		timestampMapValue(
			fields,
			"updatedAt",
			"updated_at",
			"lastUpdated",
			"last_updated",
			"lastActive",
			"last_active",
			"timestamp",
		),
		info.UpdatedAt,
	)
	if info.RawJSON == "" && len(raw) > 0 {
		info.RawJSON = string(raw)
	}
}

func appendHermesLocalMessages(local *hermesLocalSession, fields map[string]any, raw []byte) {
	messages, ok := fields["messages"].([]any)
	if !ok {
		return
	}
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		messageRaw, _ := json.Marshal(message)
		appendHermesLocalMessage(local, message, firstNonEmptyBytes(messageRaw, raw))
	}
}

func appendHermesLocalMessage(local *hermesLocalSession, fields map[string]any, raw []byte) {
	message := fields
	if nested, ok := fields["message"].(map[string]any); ok {
		message = nested
	}
	role := firstNonEmpty(
		stringMapValue(message, "role"),
		stringMapValue(fields, "role", "type", "kind"),
	)
	content := firstNonEmpty(
		contentText(message["content"]),
		contentText(fields["content"]),
		stringMapValue(message, "text"),
		stringMapValue(fields, "text"),
	)
	if strings.TrimSpace(content) == "" || strings.TrimSpace(role) == "" {
		return
	}
	timestamp := firstNonEmpty(
		timestampMapValue(
			fields,
			"timestamp",
			"createdAt",
			"created_at",
			"updatedAt",
			"updated_at",
		),
		timestampMapValue(
			message,
			"timestamp",
			"createdAt",
			"created_at",
			"updatedAt",
			"updated_at",
		),
	)
	if local.info.Name == "" && role == "user" {
		local.info.Name = titleCandidate(content)
	}
	if local.info.Preview == "" && role == "assistant" {
		local.info.Preview = titleCandidate(content)
	}
	if timestamp != "" {
		local.info.UpdatedAt = timestamp
	}
	sessionID := "hermes:" + firstNonEmpty(local.info.SessionID, local.info.NativeID)
	local.elements = append(local.elements, &model.Element{
		SessionID:   sessionID,
		Seq:         int64(len(local.elements) + 1),
		Type:        "message",
		Role:        role,
		StartedAt:   timestamp,
		CompletedAt: timestamp,
		ContentText: strings.TrimSpace(content),
		RawJSON:     string(raw),
	})
}

func finalizeHermesLocalSession(local *hermesLocalSession, path string) {
	nativeID := firstNonEmpty(local.info.SessionID, local.info.NativeID, hermesIDFromPath(path))
	local.info.SessionID = nativeID
	local.info.NativeID = firstNonEmpty(local.info.NativeID, nativeID)
	local.info.Status = firstNonEmpty(local.info.Status, "available")
	if local.info.Name == "" {
		local.info.Name = nativeID
	}
	if local.info.UpdatedAt == "" && len(local.elements) > 0 {
		local.info.UpdatedAt = local.elements[len(local.elements)-1].CompletedAt
	}
	local.info.LastActive = firstNonEmpty(local.info.LastActive, local.info.UpdatedAt)
	for _, element := range local.elements {
		element.SessionID = "hermes:" + nativeID
	}
}

func hermesIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func contentText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			text := contentText(item)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return firstNonEmpty(
			stringMapValue(typed, "text", "content", "value", "data"),
			contentText(typed["message"]),
		)
	default:
		return ""
	}
}

func stringMapValue(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case float64:
			return fmt.Sprintf("%.0f", typed)
		case int64:
			return fmt.Sprintf("%d", typed)
		case int:
			return fmt.Sprintf("%d", typed)
		}
	}
	return ""
}

func timestampMapValue(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case float64:
			if formatted := hermesNumericTimestamp(typed); formatted != "" {
				return formatted
			}
		case int64:
			if formatted := hermesNumericTimestamp(float64(typed)); formatted != "" {
				return formatted
			}
		case int:
			if formatted := hermesNumericTimestamp(float64(typed)); formatted != "" {
				return formatted
			}
		}
	}
	return ""
}

func hermesNumericTimestamp(value float64) string {
	if value <= 0 {
		return ""
	}
	switch {
	case value > 1e17:
		return time.Unix(0, int64(value)).UTC().Format(time.RFC3339Nano)
	case value > 1e12:
		return unixSeconds(value / 1000)
	case value > 600_000_000 && value < 1_000_000_000:
		return unixSeconds(value + 978_307_200)
	default:
		return unixSeconds(value)
	}
}

func unixSeconds(value float64) string {
	sec := int64(value)
	nsec := int64((value - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
}

func stringSliceMapValue(fields map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		raw, ok := value.([]any)
		if !ok {
			continue
		}
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func firstNonEmptyBytes(values ...[]byte) []byte {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullInt64Value(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func closeRows(rows *sql.Rows) {
	_ = rows.Close()
}

func closeDB(db *sql.DB) {
	_ = db.Close()
}

func hermesRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("PAXL_HERMES_HOME")); root != "" {
		return root, nil
	}
	if root := strings.TrimSpace(os.Getenv("HERMES_HOME")); root != "" {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hermes"), nil
}

func hermesRootNoError() string {
	root, err := hermesRoot()
	if err != nil {
		return ""
	}
	return root
}

func postHermesPrompt(ctx context.Context, text string, sessionID string) (*PromptResponse, error) {
	payload := &hermesChatRequest{
		Model:    "deepseek-v4-pro",
		Messages: []hermesChatMessage{{Role: "user", Content: text}},
		Stream:   true,
	}
	if err := hermesJSON(
		ctx,
		http.MethodPost,
		"/v1/chat/completions",
		payload,
		nil,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("post hermes prompt: %w", err)
	}
	return &PromptResponse{DeliveryMethod: "hermes_http"}, nil
}

func hermesJSON(
	ctx context.Context,
	method string,
	path string,
	requestValue any,
	responseValue any,
	sessionID string,
) error {
	var body io.Reader
	if requestValue != nil {
		raw, err := json.Marshal(requestValue)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, hermesURL()+path, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if requestValue != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost {
		req.Header.Set("Accept", "text/event-stream")
	}
	if sessionID != "" {
		req.Header.Set("X-Hermes-Session-Id", sessionID)
	}
	applyHermesAuth(req)
	resp, err := hermesHTTPClient.Do(req)
	if err != nil {
		if isHermesUnavailable(err) {
			return fmt.Errorf("%w: %w", errHermesUnavailable, err)
		}
		return fmt.Errorf("send request: %w", err)
	}
	defer closeResponse(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if responseValue == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(raw, responseValue); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	captureHermesRawJSON(responseValue, raw)
	return nil
}

func hermesURL() string {
	raw := strings.TrimSpace(os.Getenv("PAXL_HERMES_URL"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("HERMES_API_URL"))
	}
	if raw == "" {
		raw = defaultHermesURL
	}
	return strings.TrimRight(raw, "/")
}

func applyHermesAuth(req *http.Request) {
	apiKey := strings.TrimSpace(os.Getenv("PAXL_HERMES_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("HERMES_API_KEY"))
	}
	if apiKey == "" {
		return
	}
	req.Header.Set("X-API-Key", apiKey)
	if req.Method == http.MethodPost {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func captureHermesRawJSON(value any, raw []byte) {
	switch typed := value.(type) {
	case *hermesSessionInfo:
		typed.RawJSON = string(raw)
	case *[]hermesSessionInfo:
		var raws []json.RawMessage
		if err := json.Unmarshal(raw, &raws); err != nil {
			return
		}
		for i := range *typed {
			if i < len(raws) {
				(*typed)[i].RawJSON = string(raws[i])
			}
		}
	}
}

func hermesModelSession(session *hermesSessionInfo) *model.Session {
	nativeID := firstNonEmpty(session.SessionID, session.NativeID)
	if nativeID == "" {
		return &model.Session{}
	}
	roots, _ := json.Marshal(session.WorkspaceRoots)
	return &model.Session{
		ID:                 "hermes:" + nativeID,
		Agent:              model.AgentNameHermes,
		NativeID:           nativeID,
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

func hermesSessionElement(session *hermesSessionInfo) *model.Element {
	nativeID := firstNonEmpty(session.SessionID, session.NativeID)
	content := strings.TrimSpace(strings.Join([]string{
		"Status: " + firstNonEmpty(session.Status, "unknown"),
		"Current task: " + session.CurrentTask,
		"Preview: " + session.Preview,
	}, "\n"))
	return &model.Element{
		SessionID:   "hermes:" + nativeID,
		Seq:         1,
		Type:        "session_status",
		Role:        "assistant",
		StartedAt:   firstNonEmpty(session.UpdatedAt, session.LastActive),
		CompletedAt: firstNonEmpty(session.UpdatedAt, session.LastActive),
		ContentText: content,
		RawJSON:     session.RawJSON,
	}
}

func closeResponse(body io.Closer) {
	_ = body.Close()
}

func isHermesUnavailable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
