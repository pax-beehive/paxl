package facade

import (
	"context"
	"net/http"
	"net/url"

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

func userTeamPath(userID string, teamID string, sub string) string {
	path := "/api/v1/user/" + url.PathEscape(userID) + "/teams"
	if teamID != "" {
		path += "/" + url.PathEscape(teamID)
	}
	if sub != "" {
		path += "/" + sub
	}
	return path
}
