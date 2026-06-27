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
	teamID := strings.TrimSpace(req.TeamID)
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userTeamPath(credential.UserID, teamID, ""),
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

type ListAllTeamAgentsRequest struct {
	AgentID     string
	IncludeSelf bool
	OnlineOnly  bool
}

type TeamRef struct {
	TeamID string
	Name   string
}

type AggregatedTeamAgent struct {
	Agent *model.TeamAgent
	Teams []TeamRef
}

type ListAllTeamAgentsResponse struct {
	Agents []*AggregatedTeamAgent
	UserID string
}

// ListAllAgents aggregates team agents across all of the caller's teams,
// de-duplicated by agent ID with the teams each agent belongs to. Agents the
// caller owns are excluded by default; pass IncludeSelf, or an explicit
// AgentID filter, to include them.
func (f *TeamFacade) ListAllAgents(
	ctx context.Context,
	req *ListAllTeamAgentsRequest,
	opts ...func(*Option),
) (*ListAllTeamAgentsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListAllTeamAgentsRequest{}
	}
	teamsResp, err := f.ListTeams(ctx, &ListTeamsRequest{}, opts...)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(req.AgentID)
	index := make(map[string]*AggregatedTeamAgent)
	var order []string
	for _, team := range teamsResp.Teams {
		agentsResp, err := f.ListAgents(
			ctx,
			&ListTeamAgentsRequest{TeamID: team.TeamID},
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("list agents for team %s: %w", team.TeamID, err)
		}
		for _, teamAgent := range agentsResp.Agents {
			if !req.IncludeSelf && agentID == "" && teamAgent.AgentOwnerUserID == teamsResp.UserID {
				continue
			}
			if agentID != "" && teamAgent.AgentID != agentID {
				continue
			}
			if req.OnlineOnly && (teamAgent.Agent == nil || !teamAgent.Agent.Online) {
				continue
			}
			aggregated, ok := index[teamAgent.AgentID]
			if !ok {
				aggregated = &AggregatedTeamAgent{Agent: teamAgent}
				index[teamAgent.AgentID] = aggregated
				order = append(order, teamAgent.AgentID)
			}
			aggregated.Teams = append(aggregated.Teams, TeamRef{
				TeamID: team.TeamID,
				Name:   team.Name,
			})
		}
	}
	out := make([]*AggregatedTeamAgent, 0, len(order))
	for _, id := range order {
		out = append(out, index[id])
	}
	return &ListAllTeamAgentsResponse{Agents: out, UserID: teamsResp.UserID}, nil
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
