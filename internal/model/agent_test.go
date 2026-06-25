package model_test

import (
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type AgentSuite struct {
	suite.Suite
}

func TestAgentSuite(t *testing.T) {
	suite.Run(t, new(AgentSuite))
}

func (s *AgentSuite) TestParseAgentNameAcceptsSupportedAgents() {
	cases := []struct {
		raw  string
		want model.AgentName
	}{
		{raw: "codex", want: model.AgentNameCodex},
		{raw: "claude", want: model.AgentNameClaude},
		{raw: "pi", want: model.AgentNamePi},
		{raw: "kiro", want: model.AgentNameKiro},
		{raw: "gemini", want: model.AgentNameGemini},
		{raw: "hermes", want: model.AgentNameHermes},
		{raw: "openclaw", want: model.AgentNameOpenClaw},
		{raw: "paxl", want: model.AgentNamePaxl},
	}

	for _, tc := range cases {
		s.Run(tc.raw, func() {
			got, err := model.ParseAgentName(tc.raw)
			s.Require().NoError(err)
			s.Equal(tc.want, got)
		})
	}
}

func (s *AgentSuite) TestParseAgentNameRejectsUnsupportedAgentsBeforeBusinessLogic() {
	cases := []string{"qwen", "unknown"}

	for _, raw := range cases {
		s.Run(raw, func() {
			got, err := model.ParseAgentName(raw)
			s.Error(err)
			s.Equal(model.AgentNameUnknown, got)
		})
	}
}
