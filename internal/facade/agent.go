package facade

import (
	"context"
	"fmt"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

type AgentFacade struct {
	registry *adaptor.Registry
}

type ListAgentsRequest struct{}

type ListAgentsResponse struct {
	Agents []*model.AgentInfo
}

func NewAgentFacade(registry *adaptor.Registry) *AgentFacade {
	if registry == nil {
		registry = adaptor.NewDefaultRegistry()
	}
	return &AgentFacade{registry: registry}
}

func (f *AgentFacade) List(
	ctx context.Context,
	req *ListAgentsRequest,
	opts ...func(*Option),
) (*ListAgentsResponse, error) {
	_ = req
	option := applyOptions(opts)
	resp, err := f.registry.List(
		ctx,
		&adaptor.ListRequest{},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	return &ListAgentsResponse{Agents: resp.Agents}, nil
}
