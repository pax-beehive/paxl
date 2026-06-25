package facade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

type AgentHookFacade struct {
	store *store.Store
}

type AgentHookRequest struct {
	Agent       model.AgentName
	Event       string
	SessionID   string
	ProjectPath string
	Prompt      string
}

type AgentHookResponse struct {
	Injection *model.KnowledgeInjection
	Message   string
}

type CompleteAgentHookRequest struct {
	InjectionID string
}

type CompleteAgentHookResponse struct {
	Injection *model.KnowledgeInjection
}

func NewAgentHookFacade(sessionStore *store.Store) *AgentHookFacade {
	return &AgentHookFacade{store: sessionStore}
}

func (f *AgentHookFacade) Run(
	ctx context.Context,
	req *AgentHookRequest,
	opts ...func(*Option),
) (*AgentHookResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("run agent hook: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("run agent hook: store is required")
	}
	if strings.TrimSpace(req.Event) != "user-prompt" {
		return nil, fmt.Errorf("unsupported hook event %q", req.Event)
	}
	claimed, err := f.store.ClaimHookKnowledgeInjection(
		ctx,
		&store.ClaimHookKnowledgeInjectionRequest{
			Agent:       req.Agent,
			SessionID:   req.SessionID,
			ProjectPath: req.ProjectPath,
			Prompt:      req.Prompt,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return &AgentHookResponse{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim hook knowledge injection: %w", err)
	}
	message := renderKnowledgeHandoff(claimed.Capsule, claimed.Injection)
	return &AgentHookResponse{Injection: claimed.Injection, Message: message}, nil
}

func (f *AgentHookFacade) Complete(
	ctx context.Context,
	req *CompleteAgentHookRequest,
	opts ...func(*Option),
) (*CompleteAgentHookResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("complete agent hook: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("complete agent hook: store is required")
	}
	consumed, err := f.store.MarkKnowledgeInjectionConsumed(
		ctx,
		&store.MarkKnowledgeInjectionConsumedRequest{InjectionID: req.InjectionID},
	)
	if err != nil {
		return nil, fmt.Errorf("mark hook knowledge injection consumed: %w", err)
	}
	return &CompleteAgentHookResponse{Injection: consumed.Injection}, nil
}
