package model_test

import (
	"encoding/json"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/suite"
)

type TeamSuite struct {
	suite.Suite
}

func TestTeamSuite(t *testing.T) {
	suite.Run(t, new(TeamSuite))
}

func (s *TeamSuite) TestTeamSummaryUnmarshalsEmbeddedTeamAndCounts() {
	const raw = `{
		"team_id":"team_1",
		"owner_user_id":"usr_owner",
		"name":"Core",
		"status":"active",
		"created_at":"2026-06-27T00:00:00Z",
		"my_role":"operator",
		"member_count":3,
		"agent_count":5
	}`
	var summary model.TeamSummary
	s.Require().NoError(json.Unmarshal([]byte(raw), &summary))
	s.Equal("team_1", summary.TeamID)
	s.Equal("Core", summary.Name)
	s.Equal("operator", summary.MyRole)
	s.Equal(3, summary.MemberCount)
	s.Equal(5, summary.AgentCount)
}

func (s *TeamSuite) TestTeamAgentUnmarshalsEmbeddedAgent() {
	const raw = `{
		"team_id":"team_1",
		"agent_id":"agent_9",
		"agent_owner_user_id":"usr_mate",
		"added_at":"2026-06-27T00:00:00Z",
		"agent":{"agent_id":"agent_9","name":"codex-laptop","online":true}
	}`
	var teamAgent model.TeamAgent
	s.Require().NoError(json.Unmarshal([]byte(raw), &teamAgent))
	s.Equal("team_1", teamAgent.TeamID)
	s.Equal("agent_9", teamAgent.AgentID)
	s.Equal("usr_mate", teamAgent.AgentOwnerUserID)
	s.Require().NotNil(teamAgent.Agent)
	s.Equal("codex-laptop", teamAgent.Agent.Name)
	s.True(teamAgent.Agent.Online)
}
