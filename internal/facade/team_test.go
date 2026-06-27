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
