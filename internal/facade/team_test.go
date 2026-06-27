package facade

import (
	"context"
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
