package facade

import (
	"context"
	"fmt"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

type ReadAgentConfigurationRequest struct {
	Agent            model.AgentName
	CWD              string
	Kind             model.AgentConfigurationKind
	IncludeContent   bool
	IncludeSensitive bool
}

type AgentConfigurationFacade struct {
	read func(context.Context, *adaptor.ReadConfigurationRequest, ...func(*adaptor.Option)) (*model.AgentConfiguration, error)
}

func NewAgentConfigurationFacade() *AgentConfigurationFacade {
	return &AgentConfigurationFacade{read: adaptor.ReadConfiguration}
}

func (f *AgentConfigurationFacade) Read(
	ctx context.Context,
	req *ReadAgentConfigurationRequest,
	opts ...func(*Option),
) (*model.AgentConfiguration, error) {
	if req == nil {
		return nil, fmt.Errorf("configuration read request is required")
	}
	if _, err := model.ParseAgentName(string(req.Agent)); err != nil {
		return nil, fmt.Errorf("validate configuration agent: %w", err)
	}
	if _, err := model.ParseAgentConfigurationKind(string(req.Kind)); err != nil {
		return nil, fmt.Errorf("validate configuration kind: %w", err)
	}
	option := applyOptions(opts)
	result, err := f.read(ctx, &adaptor.ReadConfigurationRequest{
		Agent: req.Agent, CWD: req.CWD, Kind: req.Kind,
		IncludeContent: req.IncludeContent, IncludeSensitive: req.IncludeSensitive,
	}, adaptor.WithVerboseWriter(option.VerboseWriter))
	if err != nil {
		return nil, fmt.Errorf("read native agent configuration: %w", err)
	}
	return result, nil
}

func AgentConfigurationSchema() []byte {
	return model.AgentConfigurationSchema()
}
