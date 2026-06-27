package facade

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type TeamFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestTeamFacadeSuite(t *testing.T) {
	suite.Run(t, new(TeamFacadeSuite))
}

func (s *TeamFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
	s.seedCredential()
}

func (s *TeamFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *TeamFacadeSuite) seedCredential() {
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL: "https://manager.example",
			APIKey:     "paxu_test",
			UserID:     "usr_1",
			Email:      "cli@example.com",
		},
	})
	s.Require().NoError(err)
}

func (s *TeamFacadeSuite) TestListTeamsGetsSummaries() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/teams", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{"teams":[
				{"team_id":"team_1","name":"Core","my_role":"owner","member_count":2,"agent_count":3}
			]},
			"code":200,"message":"ok"
		}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.ListTeams(s.ctx, &ListTeamsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Teams, 1)
	s.Equal("team_1", resp.Teams[0].TeamID)
	s.Equal("owner", resp.Teams[0].MyRole)
	s.Equal("usr_1", resp.UserID)
}

func (s *TeamFacadeSuite) TestGetTeamReturnsTeam() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/teams/team_1", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{"team":{"team_id":"team_1","name":"Core","status":"active"}},
			"code":200,"message":"ok"
		}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.GetTeam(s.ctx, &GetTeamRequest{TeamID: "team_1"})

	s.Require().NoError(err)
	s.Equal("Core", resp.Team.Name)
	s.Equal("usr_1", resp.UserID)
}

func (s *TeamFacadeSuite) TestGetTeamRequiresTeamID() {
	teamFacade := NewTeamFacade(roundTripFunc(func(*http.Request) (*http.Response, error) {
		s.Fail("manager should not be called")
		return nil, fmt.Errorf("unexpected call")
	}), s.store)

	_, err := teamFacade.GetTeam(s.ctx, &GetTeamRequest{})

	s.Require().Error(err)
	s.Contains(err.Error(), "team id is required")
}

func (s *TeamFacadeSuite) TestListAgentsGetsTeamAgents() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/teams/team_1/agents", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{"agents":[
				{"team_id":"team_1","agent_id":"agent_9","agent_owner_user_id":"usr_mate",
				 "agent":{"agent_id":"agent_9","name":"codex-laptop","online":true}}
			]},
			"code":200,"message":"ok"
		}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.ListAgents(s.ctx, &ListTeamAgentsRequest{TeamID: "team_1"})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_9", resp.Agents[0].AgentID)
	s.Equal("team_1", resp.TeamID)
}

func (s *TeamFacadeSuite) TestListAgentsRequiresTeamID() {
	teamFacade := NewTeamFacade(roundTripFunc(func(*http.Request) (*http.Response, error) {
		s.Fail("manager should not be called")
		return nil, fmt.Errorf("unexpected call")
	}), s.store)

	_, err := teamFacade.ListAgents(s.ctx, &ListTeamAgentsRequest{})

	s.Require().Error(err)
	s.Contains(err.Error(), "team id is required")
}

func (s *TeamFacadeSuite) teamGraphClient() roundTripFunc {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/user/usr_1/teams":
			return jsonResponse(`{"data":{"teams":[
				{"team_id":"team_a","name":"Alpha"},
				{"team_id":"team_b","name":"Beta"}
			]},"code":200,"message":"ok"}`), nil
		case "/api/v1/user/usr_1/teams/team_a/agents":
			return jsonResponse(`{"data":{"agents":[
				{"team_id":"team_a","agent_id":"agent_self","agent_owner_user_id":"usr_1",
				 "agent":{"agent_id":"agent_self","name":"my-codex","online":true}},
				{"team_id":"team_a","agent_id":"agent_mate","agent_owner_user_id":"usr_mate",
				 "agent":{"agent_id":"agent_mate","name":"mate-claude","online":false}}
			]},"code":200,"message":"ok"}`), nil
		case "/api/v1/user/usr_1/teams/team_b/agents":
			return jsonResponse(`{"data":{"agents":[
				{"team_id":"team_b","agent_id":"agent_mate","agent_owner_user_id":"usr_mate",
				 "agent":{"agent_id":"agent_mate","name":"mate-claude","online":false}}
			]},"code":200,"message":"ok"}`), nil
		default:
			s.Failf("unexpected path", "path=%s", req.URL.Path)
			return nil, fmt.Errorf("unexpected path: %s", req.URL.Path)
		}
	})
}

func (s *TeamFacadeSuite) TestListAllAgentsExcludesSelfAndDedupes() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1) // self excluded, mate de-duped across two teams
	agg := resp.Agents[0]
	s.Equal("agent_mate", agg.Agent.AgentID)
	s.Require().Len(agg.Teams, 2)
	s.Equal("team_a", agg.Teams[0].TeamID)
	s.Equal("team_b", agg.Teams[1].TeamID)
}

func (s *TeamFacadeSuite) TestListAllAgentsIncludeSelf() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{IncludeSelf: true})

	s.Require().NoError(err)
	s.Len(resp.Agents, 2)
}

func (s *TeamFacadeSuite) TestListAllAgentsFiltersByAgentID() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{
		IncludeSelf: true,
		AgentID:     "agent_self",
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_self", resp.Agents[0].Agent.AgentID)
}

func (s *TeamFacadeSuite) TestListAllAgentsByAgentIDIncludesSelf() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{AgentID: "agent_self"})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_self", resp.Agents[0].Agent.AgentID)
}

func (s *TeamFacadeSuite) TestListAllAgentsEmptyTeams() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal("/api/v1/user/usr_1/teams", req.URL.Path)
		return jsonResponse(`{"data":{"teams":[]},"code":200,"message":"ok"}`), nil
	})
	teamFacade := NewTeamFacade(client, s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{})

	s.Require().NoError(err)
	s.Empty(resp.Agents)
	s.Equal("usr_1", resp.UserID)
}

func (s *TeamFacadeSuite) TestListAllAgentsOnlineOnly() {
	teamFacade := NewTeamFacade(s.teamGraphClient(), s.store)

	resp, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{
		IncludeSelf: true,
		OnlineOnly:  true,
	})

	s.Require().NoError(err)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_self", resp.Agents[0].Agent.AgentID)
}

func (s *TeamFacadeSuite) TestListAllAgentsStrictOnTeamFailure() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/user/usr_1/teams":
			body := `{"data":{"teams":[{"team_id":"team_a","name":"Alpha"}]}` +
				`,"code":200,"message":"ok"}`
			return jsonResponse(body), nil
		default:
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       http.NoBody,
			}, nil
		}
	})
	teamFacade := NewTeamFacade(client, s.store)

	_, err := teamFacade.ListAllAgents(s.ctx, &ListAllTeamAgentsRequest{})

	s.Require().Error(err)
	s.Contains(err.Error(), "team_a")
}
