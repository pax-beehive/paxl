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

type TeamFacade struct {
	auth *AuthFacade
}

func NewTeamFacade(client AuthHTTPClient, sessionStore *store.Store) *TeamFacade {
	return &TeamFacade{auth: NewAuthFacade(client, sessionStore)}
}

type ListTeamsRequest struct{}

type ListTeamsResponse struct {
	Teams  []*model.TeamSummary
	UserID string
}

func (f *TeamFacade) ListTeams(
	ctx context.Context,
	_ *ListTeamsRequest,
	opts ...func(*Option),
) (*ListTeamsResponse, error) {
	_ = applyOptions(opts)
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Teams []*model.TeamSummary `json:"teams"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, "", ""),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListTeamsResponse{Teams: envelope.Data.Teams, UserID: credential.UserID}, nil
}

type GetTeamRequest struct {
	TeamID string
}

type GetTeamResponse struct {
	Team   *model.Team
	UserID string
}

func (f *TeamFacade) GetTeam(
	ctx context.Context,
	req *GetTeamRequest,
	opts ...func(*Option),
) (*GetTeamResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.TeamID) == "" {
		return nil, fmt.Errorf("get team: team id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Team model.Team `json:"team"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, strings.TrimSpace(req.TeamID), ""),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &GetTeamResponse{Team: &envelope.Data.Team, UserID: credential.UserID}, nil
}

type ListTeamAgentsRequest struct {
	TeamID string
}

type ListTeamAgentsResponse struct {
	Agents []*model.TeamAgent
	TeamID string
	UserID string
}

func (f *TeamFacade) ListAgents(
	ctx context.Context,
	req *ListTeamAgentsRequest,
	opts ...func(*Option),
) (*ListTeamAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.TeamID) == "" {
		return nil, fmt.Errorf("list team agents: team id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	teamID := strings.TrimSpace(req.TeamID)
	var envelope managerEnvelope[struct {
		Agents []*model.TeamAgent `json:"agents"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, teamID, "agents"),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListTeamAgentsResponse{
		Agents: envelope.Data.Agents,
		TeamID: teamID,
		UserID: credential.UserID,
	}, nil
}

func userTeamPath(userID string, teamID string, sub string) string {
	path := "/api/v1/user/" + url.PathEscape(userID) + "/teams"
	if teamID != "" {
		path += "/" + url.PathEscape(teamID)
	}
	// sub must be a fixed segment name (e.g. "agents"), not user-controlled input.
	if sub != "" {
		path += "/" + sub
	}
	return path
}
