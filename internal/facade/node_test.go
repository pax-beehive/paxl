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

type NodeFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestNodeFacadeSuite(t *testing.T) {
	suite.Run(t, new(NodeFacadeSuite))
}

func (s *NodeFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
	s.seedCredential()
}

func (s *NodeFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *NodeFacadeSuite) TestListFetchesUserNodes() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v1/user/usr_1/nodes" {
			return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{
				"nodes":[{
					"node_id":"node_paxl",
					"owner_user_id":"usr_1",
					"kind":"paxl",
					"name":"paxl-mac",
					"hostname":"paxl-mac",
					"os":"darwin",
					"arch":"arm64",
					"status":"offline",
					"registered_at":"2026-06-24T00:00:00Z"
				}]
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	nodeFacade := NewNodeFacade(client, s.store)

	resp, err := nodeFacade.List(s.ctx, &ListNodesRequest{})

	s.Require().NoError(err)
	s.Equal("node_paxl", resp.CurrentNodeID)
	s.Require().Len(resp.Nodes, 1)
	s.Equal("paxl", resp.Nodes[0].Kind)
}

func (s *NodeFacadeSuite) seedCredential() {
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:   "https://manager.example",
			APIKey:       "paxu_test",
			UserAPIKeyID: "key_1",
			NodeID:       "node_paxl",
			UserID:       "usr_1",
			Email:        "cli@example.com",
			DisplayName:  "CLI",
			Role:         "user",
		},
	})
	s.Require().NoError(err)
}
