package facade

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

type NodeFacade struct {
	auth *AuthFacade
}

type ListNodesRequest struct{}

type ListNodesResponse struct {
	Nodes         []*model.Node
	CurrentNodeID string
}

type ListNodeAgentsRequest struct {
	NodeID string
}

type ListNodeAgentsResponse struct {
	Agents []*model.NodeAgent
	NodeID string
}

type ListNodeSessionsRequest struct {
	NodeID  string
	AgentID string
}

type ListNodeSessionsResponse struct {
	Sessions []*model.NodeSession
	NodeID   string
	AgentID  string
}

func NewNodeFacade(client AuthHTTPClient, sessionStore *store.Store) *NodeFacade {
	return &NodeFacade{auth: NewAuthFacade(client, sessionStore)}
}

func (f *NodeFacade) List(
	ctx context.Context,
	req *ListNodesRequest,
	opts ...func(*Option),
) (*ListNodesResponse, error) {
	_ = applyOptions(opts)
	_ = req
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Nodes []*model.Node `json:"nodes"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userNodePath(credential.UserID),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListNodesResponse{
		Nodes:         envelope.Data.Nodes,
		CurrentNodeID: credential.NodeID,
	}, nil
}

func (f *NodeFacade) ListAgents(
	ctx context.Context,
	req *ListNodeAgentsRequest,
	opts ...func(*Option),
) (*ListNodeAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.NodeID) == "" {
		return nil, fmt.Errorf("list node agents: node id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Agents []*model.NodeAgent `json:"agents"`
	}]
	nodeID := strings.TrimSpace(req.NodeID)
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userNodePath(credential.UserID)+"/"+url.PathEscape(nodeID)+"/agents",
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListNodeAgentsResponse{Agents: envelope.Data.Agents, NodeID: nodeID}, nil
}

func (f *NodeFacade) ListSessions(
	ctx context.Context,
	req *ListNodeSessionsRequest,
	opts ...func(*Option),
) (*ListNodeSessionsResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.NodeID) == "" ||
		strings.TrimSpace(req.AgentID) == "" {
		return nil, fmt.Errorf("list node sessions: node id and agent id are required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Sessions []*model.NodeSession `json:"sessions"`
	}]
	nodeID := strings.TrimSpace(req.NodeID)
	agentID := strings.TrimSpace(req.AgentID)
	err = f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		nodeAgentSessionsPath(credential.UserID, nodeID, agentID),
		credential.APIKey,
		nil,
		&envelope,
	)
	if err != nil {
		if !strings.Contains(err.Error(), "manager returned HTTP 404") {
			return nil, err
		}
		if fallbackErr := f.auth.managerJSON(
			ctx,
			http.MethodGet,
			credential.ManagerURL,
			legacyAgentSessionsPath(agentID),
			credential.APIKey,
			nil,
			&envelope,
		); fallbackErr != nil {
			return nil, err
		}
		envelope.Data.Sessions = filterNodeSessions(envelope.Data.Sessions, nodeID)
	}
	return &ListNodeSessionsResponse{
		Sessions: envelope.Data.Sessions,
		NodeID:   nodeID,
		AgentID:  agentID,
	}, nil
}

func userNodePath(userID string) string {
	return "/api/v1/user/" + url.PathEscape(userID) + "/nodes"
}

func nodeAgentSessionsPath(userID string, nodeID string, agentID string) string {
	return userNodePath(userID) + "/" + url.PathEscape(nodeID) + "/agents/" +
		url.PathEscape(agentID) + "/sessions"
}

func legacyAgentSessionsPath(agentID string) string {
	return "/api/user/agents/" + url.PathEscape(agentID) + "/sessions"
}

func filterNodeSessions(sessions []*model.NodeSession, nodeID string) []*model.NodeSession {
	filtered := make([]*model.NodeSession, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if session.NodeID == "" || session.NodeID == nodeID {
			filtered = append(filtered, session)
		}
	}
	return filtered
}
