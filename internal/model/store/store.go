package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type OpenRequest struct {
	Path string
}

type OpenResponse struct {
	Store *Store
}

type UpsertSessionsRequest struct {
	Agent    model.AgentName
	Sessions []*model.Session
}

type UpsertSessionsResponse struct{}

type ListSessionsRequest struct {
	Agents []model.AgentName
	Limit  int
}

type ListSessionsResponse struct {
	Sessions []*model.Session
}

type FindSessionRequest struct {
	ID    string
	Agent model.AgentName
}

type FindSessionResponse struct {
	Session *model.Session
}

type ReplaceSessionElementsRequest struct {
	SessionID string
	Elements  []*model.Element
}

type ReplaceSessionElementsResponse struct {
	SyncVersion int64
}

type ElementsRequest struct {
	Session *model.Session
}

type ElementsResponse struct {
	Elements []*model.Element
}

type CreateKnowledgeCapsuleRequest struct {
	Capsule *model.KnowledgeCapsule
}

type CreateKnowledgeCapsuleResponse struct {
	Capsule *model.KnowledgeCapsule
}

type ListKnowledgeCapsulesRequest struct {
	Status          string
	Keyword         string
	SourceSessionID string
	Limit           int
}

type ListKnowledgeCapsulesResponse struct {
	Capsules []*model.KnowledgeCapsule
}

type GetKnowledgeCapsuleRequest struct {
	CapsuleID string
}

type GetKnowledgeCapsuleResponse struct {
	Capsule *model.KnowledgeCapsule
}

type ArchiveKnowledgeCapsuleRequest struct {
	CapsuleID string
}

type ArchiveKnowledgeCapsuleResponse struct {
	Capsule *model.KnowledgeCapsule
}

type CreateKnowledgeInjectionRequest struct {
	Injection *model.KnowledgeInjection
}

type CreateKnowledgeInjectionResponse struct {
	Injection *model.KnowledgeInjection
}

type ListKnowledgeInjectionsRequest struct {
	TargetSessionID string
	Limit           int
}

type ListKnowledgeInjectionsResponse struct {
	Injections []*model.KnowledgeInjection
}

type SaveAuthCredentialRequest struct {
	Credential *model.AuthCredential
}

type SaveAuthCredentialResponse struct {
	Credential *model.AuthCredential
}

type GetAuthCredentialResponse struct {
	Credential *model.AuthCredential
}

type DeleteAuthCredentialResponse struct{}

func Open(ctx context.Context, req *OpenRequest) (*OpenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("open store: request is required")
	}
	path, err := resolvePath(req.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve store path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite database: %w", err)
	}
	return &OpenResponse{Store: &Store{db: db}}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

func (s *Store) SaveAuthCredential(
	ctx context.Context,
	req *SaveAuthCredentialRequest,
) (*SaveAuthCredentialResponse, error) {
	if req == nil || req.Credential == nil {
		return nil, fmt.Errorf("save auth credential: credential is required")
	}
	credential := req.Credential
	now := time.Now().UTC().Format(time.RFC3339)
	if credential.CreatedAt == "" {
		createdAt, err := s.authCredentialCreatedAt(ctx)
		if err != nil {
			return nil, err
		}
		if createdAt == "" {
			createdAt = now
		}
		credential.CreatedAt = createdAt
	}
	credential.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_credentials (
			id, manager_url, api_key, user_api_key_id, node_id, user_id, email, display_name,
			role, created_at, updated_at
		)
		VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			manager_url = excluded.manager_url,
			api_key = excluded.api_key,
			user_api_key_id = excluded.user_api_key_id,
			node_id = excluded.node_id,
			user_id = excluded.user_id,
			email = excluded.email,
			display_name = excluded.display_name,
			role = excluded.role,
			updated_at = excluded.updated_at
	`, credential.ManagerURL, credential.APIKey, credential.UserAPIKeyID, credential.NodeID,
		credential.UserID, credential.Email, credential.DisplayName, credential.Role,
		credential.CreatedAt, credential.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("save auth credential: %w", err)
	}
	return &SaveAuthCredentialResponse{Credential: credential}, nil
}

func (s *Store) GetAuthCredential(ctx context.Context) (*GetAuthCredentialResponse, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT manager_url, api_key, user_api_key_id, node_id, user_id, email, display_name,
			role, created_at, updated_at
		FROM auth_credentials
		WHERE id = 'default'
	`)
	credential := &model.AuthCredential{}
	var userAPIKeyID, nodeID, displayName, role sql.NullString
	err := row.Scan(
		&credential.ManagerURL,
		&credential.APIKey,
		&userAPIKeyID,
		&nodeID,
		&credential.UserID,
		&credential.Email,
		&displayName,
		&role,
		&credential.CreatedAt,
		&credential.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return &GetAuthCredentialResponse{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get auth credential: %w", err)
	}
	credential.UserAPIKeyID = userAPIKeyID.String
	credential.NodeID = nodeID.String
	credential.DisplayName = displayName.String
	credential.Role = role.String
	return &GetAuthCredentialResponse{Credential: credential}, nil
}

func (s *Store) authCredentialCreatedAt(ctx context.Context) (string, error) {
	var createdAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT created_at FROM auth_credentials WHERE id = 'default'`,
	).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read auth credential created_at: %w", err)
	}
	return createdAt, nil
}

func (s *Store) DeleteAuthCredential(ctx context.Context) (*DeleteAuthCredentialResponse, error) {
	if _, err := s.db.ExecContext(
		ctx,
		`DELETE FROM auth_credentials WHERE id = 'default'`,
	); err != nil {
		return nil, fmt.Errorf("delete auth credential: %w", err)
	}
	return &DeleteAuthCredentialResponse{}, nil
}

func (s *Store) UpsertSessions(
	ctx context.Context,
	req *UpsertSessionsRequest,
) (*UpsertSessionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("upsert sessions: request is required")
	}
	agent, err := model.ParseAgentName(string(req.Agent))
	if err != nil {
		return nil, fmt.Errorf("upsert sessions: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin session upsert: %w", err)
	}
	defer rollbackTx(tx)
	for _, session := range req.Sessions {
		if session == nil {
			continue
		}
		if err := upsertSession(ctx, tx, agent, session, now); err != nil {
			return nil, fmt.Errorf("upsert session %q: %w", session.NativeID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session upsert: %w", err)
	}
	return &UpsertSessionsResponse{}, nil
}

func (s *Store) ListSessions(
	ctx context.Context,
	req *ListSessionsRequest,
) (*ListSessionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list sessions: request is required")
	}
	query, args, err := listSessionsQuery(req)
	if err != nil {
		return nil, fmt.Errorf("build list sessions query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer closeRows(rows)
	sessions, err := scanSessions(rows)
	if err != nil {
		return nil, fmt.Errorf("scan sessions: %w", err)
	}
	return &ListSessionsResponse{Sessions: sessions}, nil
}

func (s *Store) FindSession(
	ctx context.Context,
	req *FindSessionRequest,
) (*FindSessionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("find session: request is required")
	}
	id := req.ID
	if req.Agent != model.AgentNameUnknown && req.Agent != "" && !strings.Contains(id, ":") {
		id = string(req.Agent) + ":" + id
	}
	query := sessionByIDQuery()
	args := []any{id}
	if !strings.Contains(id, ":") {
		query = sessionByNativeIDQuery()
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query session %q: %w", req.ID, err)
	}
	defer closeRows(rows)
	sessions, err := scanSessions(rows)
	if err != nil {
		return nil, fmt.Errorf("scan session %q: %w", req.ID, err)
	}
	if len(sessions) == 0 {
		return nil, sql.ErrNoRows
	}
	if len(sessions) > 1 {
		return nil, fmt.Errorf("find session %q: ambiguous native id", req.ID)
	}
	return &FindSessionResponse{Session: sessions[0]}, nil
}

func (s *Store) ReplaceSessionElements(
	ctx context.Context,
	req *ReplaceSessionElementsRequest,
) (*ReplaceSessionElementsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("replace session elements: request is required")
	}
	version := time.Now().UTC().UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin replace session elements: %w", err)
	}
	defer rollbackTx(tx)
	for _, element := range req.Elements {
		if element == nil {
			continue
		}
		if err := insertElement(ctx, tx, req.SessionID, version, element); err != nil {
			return nil, fmt.Errorf("insert session element %d: %w", element.Seq, err)
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE sessions SET current_sync_version = ?, last_synced_at = ? WHERE id = ?`,
		version,
		time.Now().UTC().Format(time.RFC3339),
		req.SessionID,
	); err != nil {
		return nil, fmt.Errorf("update session sync version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit replace session elements: %w", err)
	}
	return &ReplaceSessionElementsResponse{SyncVersion: version}, nil
}

func (s *Store) Elements(ctx context.Context, req *ElementsRequest) (*ElementsResponse, error) {
	if req == nil || req.Session == nil {
		return nil, fmt.Errorf("elements: session is required")
	}
	if req.Session.CurrentSyncVersion == 0 {
		return &ElementsResponse{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, sync_version, seq, type, COALESCE(role, ''), COALESCE(model, ''),
			COALESCE(started_at, ''), COALESCE(completed_at, ''), COALESCE(duration_ms, 0),
			COALESCE(usage_json, ''), COALESCE(content_text, ''), COALESCE(raw_json, '')
		FROM session_elements
		WHERE session_id = ? AND sync_version = ?
		ORDER BY seq
	`, req.Session.ID, req.Session.CurrentSyncVersion)
	if err != nil {
		return nil, fmt.Errorf("query session elements: %w", err)
	}
	defer closeRows(rows)
	elements, err := scanElements(rows)
	if err != nil {
		return nil, fmt.Errorf("scan session elements: %w", err)
	}
	return &ElementsResponse{Elements: elements}, nil
}

func (s *Store) CreateKnowledgeCapsule(
	ctx context.Context,
	req *CreateKnowledgeCapsuleRequest,
) (*CreateKnowledgeCapsuleResponse, error) {
	if req == nil || req.Capsule == nil {
		return nil, fmt.Errorf("create knowledge capsule: capsule is required")
	}
	capsule := *req.Capsule
	defaultCapsuleFields(&capsule)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_capsules (
			capsule_id, source_node_id, source_session_id, source_agent, keyword, title, summary, content,
			status, truncated, original_estimated_chars, created_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, capsule.CapsuleID, capsule.SourceNodeID, capsule.SourceSessionID, capsule.SourceAgent,
		capsule.Keyword, capsule.Title, capsule.Summary, capsule.Content, capsule.Status,
		boolInt(capsule.Truncated), capsule.OriginalEstimatedChars, capsule.CreatedAt,
		nullString(capsule.ArchivedAt))
	if err != nil {
		return nil, fmt.Errorf("insert knowledge capsule %q: %w", capsule.CapsuleID, err)
	}
	return &CreateKnowledgeCapsuleResponse{Capsule: &capsule}, nil
}

func (s *Store) ListKnowledgeCapsules(
	ctx context.Context,
	req *ListKnowledgeCapsulesRequest,
) (*ListKnowledgeCapsulesResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list knowledge capsules: request is required")
	}
	query, args := listKnowledgeCapsulesQuery(req)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query knowledge capsules: %w", err)
	}
	defer closeRows(rows)
	capsules, err := scanKnowledgeCapsules(rows)
	if err != nil {
		return nil, fmt.Errorf("scan knowledge capsules: %w", err)
	}
	return &ListKnowledgeCapsulesResponse{Capsules: capsules}, nil
}

func (s *Store) GetKnowledgeCapsule(
	ctx context.Context,
	req *GetKnowledgeCapsuleRequest,
) (*GetKnowledgeCapsuleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("get knowledge capsule: request is required")
	}
	capsule, err := s.getKnowledgeCapsule(ctx, req.CapsuleID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge capsule %q: %w", req.CapsuleID, err)
	}
	return &GetKnowledgeCapsuleResponse{Capsule: capsule}, nil
}

func (s *Store) ArchiveKnowledgeCapsule(
	ctx context.Context,
	req *ArchiveKnowledgeCapsuleRequest,
) (*ArchiveKnowledgeCapsuleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("archive knowledge capsule: request is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_capsules
		SET status = 'archived', archived_at = COALESCE(archived_at, ?)
		WHERE capsule_id = ?
	`, now, req.CapsuleID)
	if err != nil {
		return nil, fmt.Errorf("archive knowledge capsule %q: %w", req.CapsuleID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("archive knowledge capsule %q rows affected: %w", req.CapsuleID, err)
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	capsule, err := s.getKnowledgeCapsule(ctx, req.CapsuleID)
	if err != nil {
		return nil, fmt.Errorf("load archived knowledge capsule %q: %w", req.CapsuleID, err)
	}
	return &ArchiveKnowledgeCapsuleResponse{Capsule: capsule}, nil
}

func (s *Store) CreateKnowledgeInjection(
	ctx context.Context,
	req *CreateKnowledgeInjectionRequest,
) (*CreateKnowledgeInjectionResponse, error) {
	if req == nil || req.Injection == nil {
		return nil, fmt.Errorf("create knowledge injection: injection is required")
	}
	injection := *req.Injection
	defaultInjectionFields(&injection)
	_, err := s.db.ExecContext(
		ctx,
		`
		INSERT INTO session_knowledge_injections (
			injection_id, capsule_id, source_node_id, source_agent, source_session_id,
			target_node_id, target_session_id, target_agent, delivery_method,
			delivery_message_type, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		injection.InjectionID,
		injection.CapsuleID,
		injection.SourceNodeID,
		injection.SourceAgent,
		injection.SourceSessionID,
		injection.TargetNodeID,
		injection.TargetSessionID,
		injection.TargetAgent,
		injection.DeliveryMethod,
		injection.DeliveryMessageType,
		injection.Status,
		injection.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert knowledge injection %q: %w", injection.InjectionID, err)
	}
	return &CreateKnowledgeInjectionResponse{Injection: &injection}, nil
}

func (s *Store) ListKnowledgeInjections(
	ctx context.Context,
	req *ListKnowledgeInjectionsRequest,
) (*ListKnowledgeInjectionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list knowledge injections: request is required")
	}
	query, args := listKnowledgeInjectionsQuery(req)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query knowledge injections: %w", err)
	}
	defer closeRows(rows)
	injections, err := scanKnowledgeInjections(rows)
	if err != nil {
		return nil, fmt.Errorf("scan knowledge injections: %w", err)
	}
	return &ListKnowledgeInjectionsResponse{Injections: injections}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		agent TEXT NOT NULL,
		native_id TEXT NOT NULL,
		title TEXT,
		status TEXT,
		preview TEXT,
		project_id TEXT,
		workspace_roots_json TEXT,
		last_active TEXT,
		updated_at TEXT,
		last_listed_at TEXT NOT NULL,
		last_synced_at TEXT,
		current_sync_version INTEGER DEFAULT 0,
		raw_json TEXT,
		UNIQUE(agent, native_id)
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_agent_updated ON sessions(agent, updated_at);

	CREATE TABLE IF NOT EXISTS session_elements (
		session_id TEXT NOT NULL,
		sync_version INTEGER NOT NULL,
		seq INTEGER NOT NULL,
		type TEXT NOT NULL,
		role TEXT,
		model TEXT,
		started_at TEXT,
		completed_at TEXT,
		duration_ms INTEGER DEFAULT 0,
		usage_json TEXT,
		content_text TEXT,
		normalized_json TEXT,
		raw_json TEXT,
		PRIMARY KEY(session_id, sync_version, seq)
	);

	CREATE TABLE IF NOT EXISTS knowledge_capsules (
		capsule_id TEXT PRIMARY KEY,
		source_session_id TEXT NOT NULL,
		source_agent TEXT NOT NULL,
		source_node_id TEXT,
		keyword TEXT NOT NULL,
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		content TEXT NOT NULL,
		status TEXT NOT NULL,
		truncated INTEGER NOT NULL DEFAULT 0,
		original_estimated_chars INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		archived_at TEXT
	);

	CREATE TABLE IF NOT EXISTS session_knowledge_injections (
		injection_id TEXT PRIMARY KEY,
		capsule_id TEXT NOT NULL,
		source_node_id TEXT,
		source_agent TEXT,
		source_session_id TEXT,
		target_node_id TEXT,
		target_session_id TEXT NOT NULL,
		target_agent TEXT NOT NULL,
		delivery_method TEXT NOT NULL,
		delivery_message_type TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS auth_credentials (
		id TEXT PRIMARY KEY,
		manager_url TEXT NOT NULL,
		api_key TEXT NOT NULL,
		user_api_key_id TEXT,
		node_id TEXT,
		user_id TEXT NOT NULL,
		email TEXT NOT NULL,
		display_name TEXT,
		role TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	`)
	if err != nil {
		return err
	}
	columns := []struct {
		table      string
		column     string
		definition string
	}{
		{table: "auth_credentials", column: "node_id", definition: "node_id TEXT"},
		{table: "knowledge_capsules", column: "source_node_id", definition: "source_node_id TEXT"},
		{
			table:      "session_knowledge_injections",
			column:     "source_node_id",
			definition: "source_node_id TEXT",
		},
		{
			table:      "session_knowledge_injections",
			column:     "source_agent",
			definition: "source_agent TEXT",
		},
		{
			table:      "session_knowledge_injections",
			column:     "source_session_id",
			definition: "source_session_id TEXT",
		},
		{
			table:      "session_knowledge_injections",
			column:     "target_node_id",
			definition: "target_node_id TEXT",
		},
	}
	for _, column := range columns {
		if err := ensureColumn(
			ctx,
			db,
			column.table,
			column.column,
			column.definition,
		); err != nil {
			return err
		}
	}
	return nil
}

func upsertSession(
	ctx context.Context,
	tx *sql.Tx,
	agent model.AgentName,
	session *model.Session,
	now string,
) error {
	nativeID := firstNonEmpty(session.NativeID, trimAgentPrefix(string(agent), session.ID))
	id := string(agent) + ":" + nativeID
	roots := firstNonEmpty(session.WorkspaceRootsJSON, "[]")
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (
			id, agent, native_id, title, status, preview, project_id, workspace_roots_json,
			last_active, updated_at, last_listed_at, raw_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			status = excluded.status,
			preview = excluded.preview,
			project_id = excluded.project_id,
			workspace_roots_json = excluded.workspace_roots_json,
			last_active = excluded.last_active,
			updated_at = excluded.updated_at,
			last_listed_at = excluded.last_listed_at,
			raw_json = excluded.raw_json
	`, id, agent, nativeID, session.Title, session.Status, session.Preview, session.ProjectID, roots,
		session.LastActive, session.UpdatedAt, now, session.RawJSON)
	return err
}

func insertElement(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
	version int64,
	element *model.Element,
) error {
	elementType := firstNonEmpty(element.Type, "message")
	_, err := tx.ExecContext(
		ctx,
		`
		INSERT INTO session_elements (
			session_id, sync_version, seq, type, role, model, started_at, completed_at,
			duration_ms, usage_json, content_text, raw_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID,
		version,
		element.Seq,
		elementType,
		element.Role,
		element.Model,
		element.StartedAt,
		element.CompletedAt,
		element.DurationMS,
		element.UsageJSON,
		element.ContentText,
		element.RawJSON,
	)
	return err
}

func listSessionsQuery(req *ListSessionsRequest) (string, []any, error) {
	args := []any{}
	where := ""
	if len(req.Agents) > 0 {
		placeholders := make([]string, 0, len(req.Agents))
		for _, rawAgent := range req.Agents {
			agent, err := model.ParseAgentName(string(rawAgent))
			if err != nil {
				return "", nil, fmt.Errorf("parse agent filter: %w", err)
			}
			placeholders = append(placeholders, "?")
			args = append(args, agent)
		}
		where = " WHERE agent IN (" + strings.Join(placeholders, ",") + ")"
	}
	query := sessionSelectQuery() + where + " ORDER BY COALESCE(updated_at, last_active, last_listed_at) DESC, id"
	if req.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, req.Limit)
	}
	return query, args, nil
}

func sessionSelectQuery() string {
	return `SELECT id, agent, native_id, COALESCE(title, ''), COALESCE(status, ''),
		COALESCE(preview, ''), COALESCE(project_id, ''), COALESCE(workspace_roots_json, '[]'),
		COALESCE(last_active, ''), COALESCE(updated_at, ''), last_listed_at,
		COALESCE(last_synced_at, ''), COALESCE(current_sync_version, 0), COALESCE(raw_json, '')
		FROM sessions`
}

func sessionByIDQuery() string {
	return `SELECT id, agent, native_id, COALESCE(title, ''), COALESCE(status, ''),
		COALESCE(preview, ''), COALESCE(project_id, ''), COALESCE(workspace_roots_json, '[]'),
		COALESCE(last_active, ''), COALESCE(updated_at, ''), last_listed_at,
		COALESCE(last_synced_at, ''), COALESCE(current_sync_version, 0), COALESCE(raw_json, '')
		FROM sessions WHERE id = ?`
}

func sessionByNativeIDQuery() string {
	return `SELECT id, agent, native_id, COALESCE(title, ''), COALESCE(status, ''),
		COALESCE(preview, ''), COALESCE(project_id, ''), COALESCE(workspace_roots_json, '[]'),
		COALESCE(last_active, ''), COALESCE(updated_at, ''), last_listed_at,
		COALESCE(last_synced_at, ''), COALESCE(current_sync_version, 0), COALESCE(raw_json, '')
		FROM sessions WHERE native_id = ?`
}

func scanSessions(rows *sql.Rows) ([]*model.Session, error) {
	var sessions []*model.Session
	for rows.Next() {
		session := &model.Session{}
		var rawAgent string
		if err := rows.Scan(
			&session.ID,
			&rawAgent,
			&session.NativeID,
			&session.Title,
			&session.Status,
			&session.Preview,
			&session.ProjectID,
			&session.WorkspaceRootsJSON,
			&session.LastActive,
			&session.UpdatedAt,
			&session.LastListedAt,
			&session.LastSyncedAt,
			&session.CurrentSyncVersion,
			&session.RawJSON,
		); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		agent, err := model.ParseAgentName(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("parse stored agent: %w", err)
		}
		session.Agent = agent
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func scanElements(rows *sql.Rows) ([]*model.Element, error) {
	var elements []*model.Element
	for rows.Next() {
		element := &model.Element{}
		if err := rows.Scan(
			&element.SessionID,
			&element.SyncVersion,
			&element.Seq,
			&element.Type,
			&element.Role,
			&element.Model,
			&element.StartedAt,
			&element.CompletedAt,
			&element.DurationMS,
			&element.UsageJSON,
			&element.ContentText,
			&element.RawJSON,
		); err != nil {
			return nil, fmt.Errorf("scan element row: %w", err)
		}
		elements = append(elements, element)
	}
	return elements, rows.Err()
}

func (s *Store) getKnowledgeCapsule(
	ctx context.Context,
	capsuleID string,
) (*model.KnowledgeCapsule, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT capsule_id, COALESCE(source_node_id, ''), source_session_id, source_agent, keyword, title, summary,
		content, status, truncated, original_estimated_chars, created_at, COALESCE(archived_at, '')
		FROM knowledge_capsules WHERE capsule_id = ?`,
		capsuleID,
	)
	capsule, err := scanKnowledgeCapsule(row)
	if err != nil {
		return nil, fmt.Errorf("scan knowledge capsule %q: %w", capsuleID, err)
	}
	return capsule, nil
}

type capsuleScanner interface {
	Scan(dest ...any) error
}

func scanKnowledgeCapsule(scanner capsuleScanner) (*model.KnowledgeCapsule, error) {
	capsule := &model.KnowledgeCapsule{}
	var sourceAgent string
	var truncated int
	if err := scanner.Scan(
		&capsule.CapsuleID,
		&capsule.SourceNodeID,
		&capsule.SourceSessionID,
		&sourceAgent,
		&capsule.Keyword,
		&capsule.Title,
		&capsule.Summary,
		&capsule.Content,
		&capsule.Status,
		&truncated,
		&capsule.OriginalEstimatedChars,
		&capsule.CreatedAt,
		&capsule.ArchivedAt,
	); err != nil {
		return nil, fmt.Errorf("scan capsule row: %w", err)
	}
	agent, err := model.ParseAgentName(sourceAgent)
	if err != nil {
		return nil, fmt.Errorf("parse capsule source agent: %w", err)
	}
	capsule.SourceAgent = agent
	capsule.Truncated = truncated != 0
	return capsule, nil
}

func listKnowledgeCapsulesQuery(req *ListKnowledgeCapsulesRequest) (string, []any) {
	var where []string
	var args []any
	if req.Status != "" {
		where = append(where, "status = ?")
		args = append(args, req.Status)
	}
	if req.Keyword != "" {
		where = append(where, "keyword = ?")
		args = append(args, req.Keyword)
	}
	if req.SourceSessionID != "" {
		where = append(where, "source_session_id = ?")
		args = append(args, req.SourceSessionID)
	}
	query := `SELECT capsule_id, COALESCE(source_node_id, ''), source_session_id, source_agent, keyword, title, summary,
		content, status, truncated, original_estimated_chars, created_at, COALESCE(archived_at, '')
		FROM knowledge_capsules`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC, capsule_id"
	if req.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, req.Limit)
	}
	return query, args
}

func scanKnowledgeCapsules(rows *sql.Rows) ([]*model.KnowledgeCapsule, error) {
	var capsules []*model.KnowledgeCapsule
	for rows.Next() {
		capsule, err := scanKnowledgeCapsule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan knowledge capsule: %w", err)
		}
		capsules = append(capsules, capsule)
	}
	return capsules, rows.Err()
}

func listKnowledgeInjectionsQuery(req *ListKnowledgeInjectionsRequest) (string, []any) {
	args := []any{}
	query := `SELECT injection_id, capsule_id, COALESCE(source_node_id, ''), COALESCE(source_agent, ''),
		COALESCE(source_session_id, ''), COALESCE(target_node_id, ''), target_session_id, target_agent, delivery_method,
		delivery_message_type, status, created_at FROM session_knowledge_injections`
	if req.TargetSessionID != "" {
		query += " WHERE target_session_id = ?"
		args = append(args, req.TargetSessionID)
	}
	query += " ORDER BY created_at DESC, injection_id"
	if req.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, req.Limit)
	}
	return query, args
}

func scanKnowledgeInjections(rows *sql.Rows) ([]*model.KnowledgeInjection, error) {
	var injections []*model.KnowledgeInjection
	for rows.Next() {
		injection := &model.KnowledgeInjection{}
		var targetAgent string
		var sourceAgent string
		if err := rows.Scan(
			&injection.InjectionID,
			&injection.CapsuleID,
			&injection.SourceNodeID,
			&sourceAgent,
			&injection.SourceSessionID,
			&injection.TargetNodeID,
			&injection.TargetSessionID,
			&targetAgent,
			&injection.DeliveryMethod,
			&injection.DeliveryMessageType,
			&injection.Status,
			&injection.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan injection row: %w", err)
		}
		source, err := parseOptionalAgentName(sourceAgent)
		if err != nil {
			return nil, fmt.Errorf("parse injection source agent: %w", err)
		}
		agent, err := model.ParseAgentName(targetAgent)
		if err != nil {
			return nil, fmt.Errorf("parse injection target agent: %w", err)
		}
		injection.SourceAgent = source
		injection.TargetAgent = agent
		injections = append(injections, injection)
	}
	return injections, rows.Err()
}

func defaultCapsuleFields(capsule *model.KnowledgeCapsule) {
	if capsule.Status == "" {
		capsule.Status = "active"
	}
	if capsule.CreatedAt == "" {
		capsule.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
}

func defaultInjectionFields(injection *model.KnowledgeInjection) {
	if injection.DeliveryMessageType == "" {
		injection.DeliveryMessageType = "system_handoff"
	}
	if injection.Status == "" {
		injection.Status = "rendered"
	}
	if injection.CreatedAt == "" {
		injection.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
}

func ensureColumn(
	ctx context.Context,
	db *sql.DB,
	table string,
	column string,
	definition string,
) error {
	if !knownMigrationColumn(table, column) {
		return fmt.Errorf("ensure column: unknown migration column %s.%s", table, column)
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table)) // #nosec G201
	if err != nil {
		return fmt.Errorf("query table info for %s: %w", table, err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			return fmt.Errorf("scan table info for %s: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table info for %s: %w", table, err)
	}
	query := fmt.Sprintf(
		"ALTER TABLE %s ADD COLUMN %s",
		table,
		definition,
	) // #nosec G201
	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}

func knownMigrationColumn(table string, column string) bool {
	switch table + "." + column {
	case "auth_credentials.node_id",
		"knowledge_capsules.source_node_id",
		"session_knowledge_injections.source_node_id",
		"session_knowledge_injections.source_agent",
		"session_knowledge_injections.source_session_id",
		"session_knowledge_injections.target_node_id":
		return true
	default:
		return false
	}
}

func parseOptionalAgentName(raw string) (model.AgentName, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return model.ParseAgentName(raw)
}

func resolvePath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "paxl", "paxl.sqlite"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "paxl", "paxl.sqlite"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func trimAgentPrefix(agent string, id string) string {
	return strings.TrimPrefix(id, agent+":")
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func rollbackTx(tx *sql.Tx) {
	_ = tx.Rollback()
}

func closeRows(rows *sql.Rows) {
	_ = rows.Close()
}
