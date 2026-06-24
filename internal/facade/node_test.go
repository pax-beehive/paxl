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

func (s *NodeFacadeSuite) TestListAgentsFetchesNodeAgents() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet ||
			req.URL.Path != "/api/v1/user/usr_1/nodes/node_1/agents" {
			return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{"agents":[{
				"agent_id":"agent_1",
				"node_id":"node_1",
				"name":"hermes",
				"agent_type":"hermes",
				"status":"online"
			}]},
			"code":200,
			"message":"ok"
		}`), nil
	})
	nodeFacade := NewNodeFacade(client, s.store)

	resp, err := nodeFacade.ListAgents(s.ctx, &ListNodeAgentsRequest{NodeID: "node_1"})

	s.Require().NoError(err)
	s.Equal("node_1", resp.NodeID)
	s.Require().Len(resp.Agents, 1)
	s.Equal("agent_1", resp.Agents[0].AgentID)
}

func (s *NodeFacadeSuite) TestListSessionsFetchesNodeAgentSessions() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet ||
			req.URL.Path != "/api/v1/user/usr_1/nodes/node_1/agents/agent_1/sessions" {
			return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		return jsonResponse(`{
			"data":{"sessions":[{
				"node_id":"node_1",
				"agent_id":"agent_1",
				"session_id":"sess_1",
				"name":"Debugging",
				"status":"active",
				"preview":"Investigating"
			}]},
			"code":200,
			"message":"ok"
		}`), nil
	})
	nodeFacade := NewNodeFacade(client, s.store)

	resp, err := nodeFacade.ListSessions(
		s.ctx,
		&ListNodeSessionsRequest{NodeID: "node_1", AgentID: "agent_1"},
	)

	s.Require().NoError(err)
	s.Equal("node_1", resp.NodeID)
	s.Equal("agent_1", resp.AgentID)
	s.Require().Len(resp.Sessions, 1)
	s.Equal("sess_1", resp.Sessions[0].SessionID)
}

func (s *NodeFacadeSuite) TestListSessionsFallsBackToLegacyAgentSessions() {
	requests := 0
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		s.Equal(http.MethodGet, req.Method)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		switch req.URL.Path {
		case "/api/v1/user/usr_1/nodes/node_1/agents/agent_1/sessions":
			resp := jsonResponse(`{"message":"not found"}`)
			resp.StatusCode = http.StatusNotFound
			return resp, nil
		case "/api/user/agents/agent_1/sessions":
			return jsonResponse(`{
				"data":{"sessions":[
					{
						"node_id":"node_1",
						"agent_id":"agent_1",
						"session_id":"sess_1",
						"name":"Debugging",
						"status":"active"
					},
					{
						"node_id":"node_2",
						"agent_id":"agent_1",
						"session_id":"sess_other",
						"name":"Other",
						"status":"active"
					},
					{
						"agent_id":"agent_1",
						"session_id":"sess_legacy",
						"name":"Legacy",
						"status":"idle"
					}
				]},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})
	nodeFacade := NewNodeFacade(client, s.store)

	resp, err := nodeFacade.ListSessions(
		s.ctx,
		&ListNodeSessionsRequest{NodeID: "node_1", AgentID: "agent_1"},
	)

	s.Require().NoError(err)
	s.Equal(2, requests)
	s.Equal("node_1", resp.NodeID)
	s.Equal("agent_1", resp.AgentID)
	s.Require().Len(resp.Sessions, 2)
	s.Equal("sess_1", resp.Sessions[0].SessionID)
	s.Equal("sess_legacy", resp.Sessions[1].SessionID)
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
