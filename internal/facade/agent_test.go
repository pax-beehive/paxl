package facade_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type AgentFacadeSuite struct {
	suite.Suite
}

func TestAgentFacadeSuite(t *testing.T) {
	suite.Run(t, new(AgentFacadeSuite))
}

func (s *AgentFacadeSuite) TestListUsesDefaultRegistryWhenRegistryIsNil() {
	agentFacade := facade.NewAgentFacade(nil)

	resp, err := agentFacade.List(context.Background(), &facade.ListAgentsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 6)
	s.Equal(model.AgentNameCodex, resp.Agents[0].Name)
	s.Equal(model.AgentNameClaude, resp.Agents[1].Name)
	s.Equal(model.AgentNamePi, resp.Agents[2].Name)
	s.Equal(model.AgentNameKiro, resp.Agents[3].Name)
	s.Equal(model.AgentNameGemini, resp.Agents[4].Name)
	s.Equal(model.AgentNameHermes, resp.Agents[5].Name)
}

func (s *AgentFacadeSuite) TestListAcceptsVerboseWriterOption() {
	agentFacade := facade.NewAgentFacade(nil)
	var verbose bytes.Buffer

	resp, err := agentFacade.List(
		context.Background(),
		&facade.ListAgentsRequest{},
		facade.WithVerboseWriter(&verbose),
	)

	s.Require().NoError(err)
	s.Len(resp.Agents, 6)
}
