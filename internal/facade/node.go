package facade

import (
	"context"
	"net/http"
	"net/url"

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

func userNodePath(userID string) string {
	return "/api/v1/user/" + url.PathEscape(userID) + "/nodes"
}
