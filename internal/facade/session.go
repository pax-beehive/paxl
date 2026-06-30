package facade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

type SessionFacade struct {
	registry *adaptor.Registry
	store    *store.Store
}

type ListSessionsRequest struct {
	Agents       []model.AgentName
	UpdatedSince *time.Time
	NoSync       bool
	Limit        int
}

type ListSessionsResponse struct {
	Sessions []*model.Session
}

type GetSessionRequest struct {
	ID    string
	Agent model.AgentName
}

type GetSessionResponse struct {
	Session  *model.Session
	Elements []*model.Element
}

func NewSessionFacade(registry *adaptor.Registry, sessionStore *store.Store) *SessionFacade {
	if registry == nil {
		registry = adaptor.NewDefaultRegistry()
	}
	return &SessionFacade{registry: registry, store: sessionStore}
}

func (f *SessionFacade) List(
	ctx context.Context,
	req *ListSessionsRequest,
	opts ...func(*Option),
) (*ListSessionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list sessions: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("list sessions: store is required")
	}
	option := applyOptions(opts)
	if !req.NoSync {
		sessions, err := f.syncSessions(ctx, req, option)
		if err != nil {
			return nil, fmt.Errorf("sync sessions: %w", err)
		}
		sortSessionsNewestFirst(sessions)
		return &ListSessionsResponse{
			Sessions: filterListedSessions(sessions, req.UpdatedSince, req.Limit),
		}, nil
	}
	storeLimit := req.Limit
	if req.UpdatedSince != nil {
		storeLimit = 0
	}
	resp, err := f.store.ListSessions(ctx, &store.ListSessionsRequest{
		Agents: req.Agents,
		Limit:  storeLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list stored sessions: %w", err)
	}
	return &ListSessionsResponse{
		Sessions: filterListedSessions(resp.Sessions, req.UpdatedSince, req.Limit),
	}, nil
}

func (f *SessionFacade) Get(
	ctx context.Context,
	req *GetSessionRequest,
	opts ...func(*Option),
) (*GetSessionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("get session: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("get session: store is required")
	}
	option := applyOptions(opts)
	session, err := f.findOrLoadSession(ctx, req, option)
	if err != nil {
		return nil, fmt.Errorf("find session %q: %w", req.ID, err)
	}
	var syncErr error
	if err := f.syncSessionElements(ctx, session.Session, option); err != nil {
		syncErr = err
		if session.Session.CurrentSyncVersion == 0 {
			return nil, fmt.Errorf("sync session elements: %w", err)
		}
	} else {
		session, err = f.store.FindSession(ctx, &store.FindSessionRequest{ID: session.Session.ID})
		if err != nil {
			return nil, fmt.Errorf("reload session %q: %w", req.ID, err)
		}
	}
	elements, err := f.store.Elements(ctx, &store.ElementsRequest{Session: session.Session})
	if err != nil {
		return nil, fmt.Errorf("load session elements: %w", err)
	}
	if syncErr != nil && len(elements.Elements) == 0 {
		return nil, fmt.Errorf("sync session elements: %w", syncErr)
	}
	return &GetSessionResponse{Session: session.Session, Elements: elements.Elements}, nil
}

func (f *SessionFacade) findOrLoadSession(
	ctx context.Context,
	req *GetSessionRequest,
	option *Option,
) (*store.FindSessionResponse, error) {
	session, err := f.store.FindSession(
		ctx,
		&store.FindSessionRequest{ID: req.ID, Agent: req.Agent},
	)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return session, err
	}
	agent, nativeID, ok, parseErr := resolveSessionLookup(req.ID, req.Agent)
	if parseErr != nil {
		return nil, parseErr
	}
	if !ok {
		return nil, err
	}
	return f.loadUncachedSession(ctx, agent, nativeID, option)
}

func resolveSessionLookup(
	id string,
	agent model.AgentName,
) (model.AgentName, string, bool, error) {
	if strings.Contains(id, ":") {
		parts := strings.SplitN(id, ":", 2)
		parsed, err := model.ParseAgentName(parts[0])
		if err != nil {
			return model.AgentNameUnknown, "", false, err
		}
		return parsed, parts[1], true, nil
	}
	if agent == model.AgentNameUnknown || agent == "" {
		return model.AgentNameUnknown, "", false, nil
	}
	return agent, id, true, nil
}

func (f *SessionFacade) loadUncachedSession(
	ctx context.Context,
	agent model.AgentName,
	nativeID string,
	option *Option,
) (*store.FindSessionResponse, error) {
	adapter, err := f.registry.Lookup(ctx, &adaptor.LookupRequest{Name: agent})
	if err != nil {
		return nil, fmt.Errorf("lookup %s adapter: %w", agent, err)
	}
	transcript, err := adapter.Adapter.GetSession(
		ctx,
		&adaptor.GetSessionRequest{NativeID: nativeID},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		return nil, fmt.Errorf("get %s session %s: %w", agent, nativeID, err)
	}
	session := &model.Session{
		NativeID:  nativeID,
		Title:     nativeID,
		UpdatedAt: latestElementTimestamp(transcript.Elements),
	}
	if _, err := f.store.UpsertSessions(ctx, &store.UpsertSessionsRequest{
		Agent:    agent,
		Sessions: []*model.Session{session},
	}); err != nil {
		return nil, fmt.Errorf("store uncached session: %w", err)
	}
	if _, err := f.store.ReplaceSessionElements(ctx, &store.ReplaceSessionElementsRequest{
		SessionID: string(agent) + ":" + nativeID,
		Elements:  transcript.Elements,
	}); err != nil {
		return nil, fmt.Errorf("store uncached session elements: %w", err)
	}
	found, err := f.store.FindSession(
		ctx,
		&store.FindSessionRequest{ID: string(agent) + ":" + nativeID},
	)
	if err != nil {
		return nil, fmt.Errorf("reload uncached session: %w", err)
	}
	return found, nil
}

func latestElementTimestamp(elements []*model.Element) string {
	latest := ""
	for _, element := range elements {
		if element == nil {
			continue
		}
		for _, value := range []string{element.CompletedAt, element.StartedAt} {
			if value > latest {
				latest = value
			}
		}
	}
	return latest
}

func (f *SessionFacade) syncSessions(
	ctx context.Context,
	req *ListSessionsRequest,
	option *Option,
) ([]*model.Session, error) {
	agents := req.Agents
	if len(agents) == 0 {
		list, err := f.registry.List(
			ctx,
			&adaptor.ListRequest{},
			adaptor.WithVerboseWriter(option.VerboseWriter),
		)
		if err != nil {
			return nil, fmt.Errorf("list adapters: %w", err)
		}
		for _, agent := range list.Agents {
			agents = append(agents, agent.Name)
		}
	}
	var synced []*model.Session
	for _, agent := range agents {
		sessions, err := f.syncAgentSessions(ctx, agent, req.Limit, option)
		if err != nil {
			return nil, err
		}
		synced = append(synced, sessions...)
	}
	return synced, nil
}

func filterListedSessions(
	sessions []*model.Session,
	updatedSince *time.Time,
	limit int,
) []*model.Session {
	out := make([]*model.Session, 0, len(sessions))
	for _, session := range sessions {
		if session == nil || !sessionUpdatedAfter(session, updatedSince) {
			continue
		}
		out = append(out, session)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func sessionUpdatedAfter(session *model.Session, updatedSince *time.Time) bool {
	if updatedSince == nil {
		return true
	}
	value := firstNonEmpty(session.UpdatedAt, session.LastActive, session.LastListedAt)
	if value == "" {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return false
	}
	return !updatedAt.Before(*updatedSince)
}

const (
	syncThrottleDuration = 30 * time.Minute
	syncTimeoutDuration  = 60 * time.Second
)

func (f *SessionFacade) syncSessionElements(
	ctx context.Context,
	session *model.Session,
	option *Option,
) error {
	return f.syncSessionElementsWithPolicy(ctx, session, option, false)
}

// SyncSessionAsync fires a goroutine that syncs a single session.
// It returns immediately; the caller (hook handler) is never blocked.
// All errors are swallowed; verbose log only.
func (f *SessionFacade) SyncSessionAsync(
	ctx context.Context,
	agent model.AgentName,
	sessionID string,
) {
	go func() {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncTimeoutDuration)
		defer cancel()
		session, err := f.store.FindSession(syncCtx, &store.FindSessionRequest{
			ID: sessionID, Agent: agent,
		})
		if err != nil {
			return
		}
		_ = f.syncSessionElementsWithPolicy(syncCtx, session.Session, &Option{}, true)
	}()
}

func (f *SessionFacade) syncSessionElementsWithPolicy(
	ctx context.Context,
	session *model.Session,
	option *Option,
	throttle bool,
) error {
	if throttle && recentlySynced(session.LastSyncedAt) {
		return nil
	}
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeoutDuration)
	defer cancel()
	adapter, err := f.registry.Lookup(syncCtx, &adaptor.LookupRequest{Name: session.Agent})
	if err != nil {
		return fmt.Errorf("lookup %s adapter: %w", session.Agent, err)
	}
	transcript, err := adapter.Adapter.GetSession(
		syncCtx,
		&adaptor.GetSessionRequest{NativeID: session.NativeID},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("get %s session %s: %w", session.Agent, session.NativeID, err)
	}
	if _, err := f.store.ReplaceSessionElements(syncCtx, &store.ReplaceSessionElementsRequest{
		SessionID: session.ID,
		Elements:  transcript.Elements,
	}); err != nil {
		return fmt.Errorf("store session elements: %w", err)
	}
	return nil
}

func recentlySynced(lastSyncedAt string) bool {
	if lastSyncedAt == "" {
		return false
	}
	lastSynced, err := time.Parse(time.RFC3339, lastSyncedAt)
	if err != nil {
		return false
	}
	return time.Since(lastSynced) < syncThrottleDuration
}

func (f *SessionFacade) syncAgentSessions(
	ctx context.Context,
	agent model.AgentName,
	limit int,
	option *Option,
) ([]*model.Session, error) {
	adapter, err := f.registry.Lookup(ctx, &adaptor.LookupRequest{Name: agent})
	if err != nil {
		return nil, fmt.Errorf("lookup %s adapter: %w", agent, err)
	}
	sessions, err := adapter.Adapter.ListSessions(
		ctx,
		&adaptor.ListSessionsRequest{Limit: limit},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		return nil, fmt.Errorf("list %s sessions: %w", agent, err)
	}
	// Sort newest-first so recent sessions are prioritised for sync
	sortSessionsNewestFirst(sessions.Sessions)
	if _, err := f.store.UpsertSessions(ctx, &store.UpsertSessionsRequest{
		Agent:    agent,
		Sessions: sessions.Sessions,
	}); err != nil {
		return nil, fmt.Errorf("store %s sessions: %w", agent, err)
	}
	return sessions.Sessions, nil
}

type SearchRequest struct {
	Query  string
	Limit  int
	NoSync bool
	Agent  model.AgentName
}

type SearchResponse struct {
	Results []*store.SearchResult
}

func (f *SessionFacade) Search(
	ctx context.Context,
	req *SearchRequest,
	opts ...func(*Option),
) (*SearchResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("search sessions: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("search sessions: store is required")
	}
	option := applyOptions(opts)
	if !req.NoSync {
		if err := f.syncSearchSessions(
			ctx,
			req.Agent,
			searchSyncLimit(req.Limit),
			option,
		); err != nil {
			return nil, fmt.Errorf("sync search sessions: %w", err)
		}
	}
	resp, err := f.store.SearchElements(ctx, &store.SearchElementsRequest{
		Query: sanitizeFTSQuery(req.Query),
		Limit: req.Limit,
		Agent: req.Agent,
	})
	if err != nil {
		return nil, fmt.Errorf("search session elements: %w", err)
	}
	return &SearchResponse{Results: resp.Results}, nil
}

const defaultSearchSyncLimit = 50

func searchSyncLimit(resultLimit int) int {
	if resultLimit > defaultSearchSyncLimit {
		return resultLimit
	}
	return defaultSearchSyncLimit
}

func (f *SessionFacade) syncSearchSessions(
	ctx context.Context,
	agent model.AgentName,
	limit int,
	option *Option,
) error {
	var agents []model.AgentName
	if agent != "" && agent != model.AgentNameUnknown {
		agents = []model.AgentName{agent}
	}
	sessions, err := f.syncSessions(ctx, &ListSessionsRequest{Agents: agents, Limit: limit}, option)
	if err != nil {
		return err
	}
	sortSessionsNewestFirst(sessions)
	for _, session := range filterListedSessions(sessions, nil, limit) {
		if session == nil {
			continue
		}
		_ = f.syncSessionElementsWithPolicy(ctx, session, option, true)
	}
	return nil
}

func sortSessionsNewestFirst(sessions []*model.Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessionSortTimestamp(sessions[i]) > sessionSortTimestamp(sessions[j])
	})
}

func sessionSortTimestamp(session *model.Session) string {
	if session == nil {
		return ""
	}
	return firstNonEmpty(session.UpdatedAt, session.LastActive, session.LastListedAt)
}

// sanitizeFTSQuery strips FTS5-special characters that cause syntax errors
// when passed raw to a MATCH clause. Keeps boolean operators (AND, OR, NOT)
// and quoted phrases intact. This is a minimal port of Hermes's _sanitize_fts5_query.
func sanitizeFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	// Protect balanced double-quoted phrases.
	var quoted []string
	sanitized := replaceAllQuoted(query, func(m string) string {
		quoted = append(quoted, m)
		return fmt.Sprintf("\x00Q%d\x00", len(quoted)-1)
	})
	// Strip FTS5-special chars that cause parse errors.
	sanitized = stripSpecial(sanitized)
	// Collapse repeated * and remove leading *.
	sanitized = strings.ReplaceAll(sanitized, "*", "")
	// Remove dangling boolean operators at start/end.
	sanitized = trimDanglingBooleans(sanitized)
	// Restore preserved quoted phrases.
	for i, q := range quoted {
		sanitized = strings.ReplaceAll(sanitized, fmt.Sprintf("\x00Q%d\x00", i), q)
	}
	return strings.TrimSpace(sanitized)
}

func replaceAllQuoted(s string, fn func(string) string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '"' {
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j < len(s) {
				result.WriteString(fn(s[i : j+1]))
				i = j + 1
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

func stripSpecial(s string) string {
	var result strings.Builder
	for _, ch := range s {
		switch ch {
		case '+', '{', '}', '(', ')', ':', '^':
			result.WriteByte(' ')
		default:
			result.WriteRune(ch)
		}
	}
	return result.String()
}

func trimDanglingBooleans(s string) string {
	s = strings.TrimSpace(s)
	// Remove leading AND/OR/NOT
	for _, op := range []string{"AND", "OR", "NOT"} {
		if strings.HasPrefix(strings.ToUpper(s), op+" ") {
			s = strings.TrimSpace(s[len(op)+1:])
		}
	}
	// Remove trailing AND/OR/NOT
	for _, op := range []string{"AND", "OR", "NOT"} {
		if strings.HasSuffix(strings.ToUpper(s), " "+op) {
			s = strings.TrimSpace(s[:len(s)-len(op)-1])
		}
	}
	return s
}
