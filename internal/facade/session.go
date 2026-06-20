package facade

import (
	"context"
	"fmt"
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
		if err := f.syncSessions(ctx, req, option); err != nil {
			return nil, fmt.Errorf("sync sessions: %w", err)
		}
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
	session, err := f.store.FindSession(
		ctx,
		&store.FindSessionRequest{ID: req.ID, Agent: req.Agent},
	)
	if err != nil {
		return nil, fmt.Errorf("find session %q: %w", req.ID, err)
	}
	option := applyOptions(opts)
	if session.Session.CurrentSyncVersion == 0 {
		if err := f.syncSessionElements(ctx, session.Session, option); err != nil {
			return nil, fmt.Errorf("sync session elements: %w", err)
		}
		session, err = f.store.FindSession(ctx, &store.FindSessionRequest{ID: session.Session.ID})
		if err != nil {
			return nil, fmt.Errorf("reload session %q: %w", req.ID, err)
		}
	}
	elements, err := f.store.Elements(ctx, &store.ElementsRequest{Session: session.Session})
	if err != nil {
		return nil, fmt.Errorf("load session elements: %w", err)
	}
	return &GetSessionResponse{Session: session.Session, Elements: elements.Elements}, nil
}

func (f *SessionFacade) syncSessions(
	ctx context.Context,
	req *ListSessionsRequest,
	option *Option,
) error {
	agents := req.Agents
	if len(agents) == 0 {
		list, err := f.registry.List(
			ctx,
			&adaptor.ListRequest{},
			adaptor.WithVerboseWriter(option.VerboseWriter),
		)
		if err != nil {
			return fmt.Errorf("list adapters: %w", err)
		}
		for _, agent := range list.Agents {
			agents = append(agents, agent.Name)
		}
	}
	for _, agent := range agents {
		if err := f.syncAgentSessions(ctx, agent, req.Limit, option); err != nil {
			return err
		}
	}
	return nil
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

func (f *SessionFacade) syncSessionElements(
	ctx context.Context,
	session *model.Session,
	option *Option,
) error {
	adapter, err := f.registry.Lookup(ctx, &adaptor.LookupRequest{Name: session.Agent})
	if err != nil {
		return fmt.Errorf("lookup %s adapter: %w", session.Agent, err)
	}
	transcript, err := adapter.Adapter.GetSession(
		ctx,
		&adaptor.GetSessionRequest{NativeID: session.NativeID},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		return fmt.Errorf("get %s session %s: %w", session.Agent, session.NativeID, err)
	}
	if _, err := f.store.ReplaceSessionElements(ctx, &store.ReplaceSessionElementsRequest{
		SessionID: session.ID,
		Elements:  transcript.Elements,
	}); err != nil {
		return fmt.Errorf("store session elements: %w", err)
	}
	return nil
}

func (f *SessionFacade) syncAgentSessions(
	ctx context.Context,
	agent model.AgentName,
	limit int,
	option *Option,
) error {
	adapter, err := f.registry.Lookup(ctx, &adaptor.LookupRequest{Name: agent})
	if err != nil {
		return fmt.Errorf("lookup %s adapter: %w", agent, err)
	}
	sessions, err := adapter.Adapter.ListSessions(
		ctx,
		&adaptor.ListSessionsRequest{Limit: limit},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		return fmt.Errorf("list %s sessions: %w", agent, err)
	}
	if _, err := f.store.UpsertSessions(ctx, &store.UpsertSessionsRequest{
		Agent:    agent,
		Sessions: sessions.Sessions,
	}); err != nil {
		return fmt.Errorf("store %s sessions: %w", agent, err)
	}
	return nil
}
