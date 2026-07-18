package adaptor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	_ "modernc.org/sqlite"
)

func NewOpenCodeAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameOpenCode,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"opencode"},
			Reason:     "Run OpenCode locally and install the opencode CLI.",
		},
		cliProbe:     func() bool { return commandExists("opencode") },
		sessionProbe: func() bool { return pathExists(openCodeDBPath()) },
		listSessions: listOpenCodeSessions,
		getSession:   getOpenCodeSession,
		prompt:       promptOpenCodeSession,
		startSession: startOpenCodeSession,
		resume:       nativeSessionResumer("opencode", "--session"),
	}
}

type openCodeMessageData struct {
	Role    string `json:"role"`
	ModelID string `json:"modelID"`
	Model   struct {
		ModelID string `json:"modelID"`
	} `json:"model"`
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
}

type openCodePartData struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openCodeElementBuilder struct {
	element *model.Element
	parts   []string
}

func listOpenCodeSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	path := openCodeDBPath()
	if !pathExists(path) {
		return &ListSessionsResponse{}, nil
	}
	dbURL := &url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dbURL.String())
	if err != nil {
		return nil, fmt.Errorf("open opencode database: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(
		ctx,
		`SELECT id, directory, title, time_created, time_updated FROM session`,
	)
	if err != nil {
		return nil, fmt.Errorf("query opencode sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sessions := make(map[string]*model.Session)
	for rows.Next() {
		var nativeID string
		var directory string
		var title string
		var createdAt int64
		var updatedAt int64
		if err := rows.Scan(&nativeID, &directory, &title, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan opencode session: %w", err)
		}
		id := "opencode:" + nativeID
		updated := time.UnixMilli(updatedAt).UTC().Format(time.RFC3339Nano)
		sessions[id] = &model.Session{
			ID:         id,
			Agent:      model.AgentNameOpenCode,
			NativeID:   nativeID,
			Title:      firstNonEmpty(title, nativeID),
			Status:     "available",
			ProjectID:  directory,
			LastActive: updated,
			UpdatedAt:  updated,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode sessions: %w", err)
	}
	return sortedSessions(sessions, req), nil
}

func getOpenCodeSession(
	ctx context.Context,
	req *GetSessionRequest,
) (*GetSessionResponse, error) {
	if req == nil || req.NativeID == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	path := openCodeDBPath()
	if !pathExists(path) {
		return nil, fmt.Errorf("opencode database is unavailable")
	}
	dbURL := &url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dbURL.String())
	if err != nil {
		return nil, fmt.Errorf("open opencode database: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.time_created, m.time_updated, m.data, p.data
		FROM message AS m
		JOIN part AS p ON p.message_id = m.id
		WHERE m.session_id = ?
		ORDER BY m.time_created, p.time_created`, req.NativeID)
	if err != nil {
		return nil, fmt.Errorf("query opencode timeline: %w", err)
	}
	defer func() { _ = rows.Close() }()

	builders := make(map[string]*openCodeElementBuilder)
	var order []string
	for rows.Next() {
		var messageID string
		var createdAt int64
		var updatedAt int64
		var messageRaw string
		var partRaw string
		if err := rows.Scan(&messageID, &createdAt, &updatedAt, &messageRaw, &partRaw); err != nil {
			return nil, fmt.Errorf("scan opencode timeline: %w", err)
		}
		builder, ok := builders[messageID]
		if !ok {
			builder = buildOpenCodeElement(req.NativeID, messageRaw, createdAt, updatedAt)
			if builder == nil {
				continue
			}
			builders[messageID] = builder
			order = append(order, messageID)
		}
		var part openCodePartData
		if err := json.Unmarshal([]byte(partRaw), &part); err == nil &&
			part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			builder.parts = append(builder.parts, strings.TrimSpace(part.Text))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode timeline: %w", err)
	}

	elements := make([]*model.Element, 0, len(order))
	for _, messageID := range order {
		builder := builders[messageID]
		builder.element.ContentText = strings.Join(builder.parts, "\n")
		if builder.element.ContentText == "" {
			continue
		}
		builder.element.Seq = int64(len(elements) + 1)
		elements = append(elements, builder.element)
	}
	return &GetSessionResponse{Elements: elements}, nil
}

func promptOpenCodeSession(
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
		[]string{"opencode", "run", "--session", req.NativeID},
		req.Text,
		option,
	)
}

func startOpenCodeSession(
	ctx context.Context,
	req *StartSessionRequest,
	option *Option,
) (*StartSessionResponse, error) {
	if req == nil || req.Text == "" {
		return nil, fmt.Errorf("prompt text is required")
	}
	_, err := runArgPromptCommand(
		ctx,
		[]string{"opencode", "run"},
		req.Text,
		option,
	)
	if err != nil {
		return nil, err
	}
	return &StartSessionResponse{}, nil
}

func buildOpenCodeElement(
	nativeID string,
	raw string,
	createdAt int64,
	updatedAt int64,
) *openCodeElementBuilder {
	var message openCodeMessageData
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		return nil
	}
	if message.Role != "user" && message.Role != "assistant" {
		return nil
	}
	startedAt := firstNonZero(message.Time.Created, createdAt)
	completedAt := firstNonZero(message.Time.Completed, updatedAt, startedAt)
	return &openCodeElementBuilder{element: &model.Element{
		SessionID:   "opencode:" + nativeID,
		Type:        "message",
		Role:        message.Role,
		Model:       firstNonEmpty(message.ModelID, message.Model.ModelID),
		StartedAt:   unixMillisTimestamp(startedAt),
		CompletedAt: unixMillisTimestamp(completedAt),
		RawJSON:     raw,
	}}
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func unixMillisTimestamp(value int64) string {
	if value == 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339Nano)
}

func openCodeDBPath() string {
	if root := os.Getenv("OPENCODE_DATA_HOME"); root != "" {
		return filepath.Join(root, "opencode.db")
	}
	if root := os.Getenv("XDG_DATA_HOME"); root != "" {
		return filepath.Join(root, "opencode", "opencode.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}
