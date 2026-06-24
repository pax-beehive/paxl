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

const friendStatusAccepted = "accepted"

type FriendFacade struct {
	auth *AuthFacade
}

type RequestFriendRequest struct {
	Email string
	Alias string
}

type RequestFriendResponse struct {
	Friend *model.Friend
}

type ListFriendsRequest struct {
	Status    string
	Direction string
	Alias     string
	Limit     int
}

type ListFriendsResponse struct {
	Friends []*model.Friend
	UserID  string
}

type GetFriendRequest struct {
	FriendID string
}

type GetFriendResponse struct {
	Friend *model.Friend
	UserID string
}

type AcceptFriendRequest struct {
	FriendID string
	Alias    string
}

type AcceptFriendResponse struct {
	Friend *model.Friend
	UserID string
}

type UpdateFriendAliasRequest struct {
	FriendID string
	Alias    string
}

type UpdateFriendAliasResponse struct {
	Friend *model.Friend
	UserID string
}

type RemoveFriendRequest struct {
	FriendID string
}

type RemoveFriendResponse struct {
	Friend *model.Friend
	UserID string
}

type BlockFriendRequest struct {
	FriendID string
}

type BlockFriendResponse struct {
	Friend *model.Friend
	UserID string
}

type ResolveFriendAliasRequest struct {
	Alias string
}

type ResolveFriendAliasResponse struct {
	Friend *model.Friend
	Email  string
}

func NewFriendFacade(client AuthHTTPClient, sessionStore *store.Store) *FriendFacade {
	return &FriendFacade{auth: NewAuthFacade(client, sessionStore)}
}

func (f *FriendFacade) Request(
	ctx context.Context,
	req *RequestFriendRequest,
	opts ...func(*Option),
) (*RequestFriendResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("request friend: request is required")
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		return nil, fmt.Errorf("request friend: email is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"email": email,
		"alias": strings.TrimPrefix(strings.TrimSpace(req.Alias), "@"),
	}
	var envelope managerEnvelope[struct {
		Friend model.Friend `json:"friend"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodPost,
		credential.ManagerURL,
		userFriendPath(credential.UserID, ""),
		credential.APIKey,
		body,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &RequestFriendResponse{Friend: &envelope.Data.Friend}, nil
}

func (f *FriendFacade) List(
	ctx context.Context,
	req *ListFriendsRequest,
	opts ...func(*Option),
) (*ListFriendsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListFriendsRequest{Status: friendStatusAccepted}
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	path := userFriendPath(credential.UserID, "")
	params := url.Values{}
	if req.Status != "" {
		params.Set("status", req.Status)
	}
	if req.Direction != "" {
		params.Set("direction", req.Direction)
	}
	if req.Alias != "" {
		params.Set("alias", strings.TrimPrefix(strings.TrimSpace(req.Alias), "@"))
	}
	if req.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", req.Limit))
	}
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var envelope managerEnvelope[struct {
		Friends []*model.Friend `json:"friends"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		path,
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &ListFriendsResponse{Friends: envelope.Data.Friends, UserID: credential.UserID}, nil
}

func (f *FriendFacade) Get(
	ctx context.Context,
	req *GetFriendRequest,
	opts ...func(*Option),
) (*GetFriendResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.FriendID) == "" {
		return nil, fmt.Errorf("get friend: friend id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Friend model.Friend `json:"friend"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userFriendPath(credential.UserID, req.FriendID),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &GetFriendResponse{Friend: &envelope.Data.Friend, UserID: credential.UserID}, nil
}

func (f *FriendFacade) Accept(
	ctx context.Context,
	req *AcceptFriendRequest,
	opts ...func(*Option),
) (*AcceptFriendResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.FriendID) == "" {
		return nil, fmt.Errorf("accept friend: friend id is required")
	}
	friend, userID, err := f.updateFriend(ctx, req.FriendID, "accept", map[string]any{
		"alias": strings.TrimPrefix(strings.TrimSpace(req.Alias), "@"),
	})
	if err != nil {
		return nil, err
	}
	return &AcceptFriendResponse{Friend: friend, UserID: userID}, nil
}

func (f *FriendFacade) UpdateAlias(
	ctx context.Context,
	req *UpdateFriendAliasRequest,
	opts ...func(*Option),
) (*UpdateFriendAliasResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.FriendID) == "" {
		return nil, fmt.Errorf("update friend alias: friend id is required")
	}
	alias := strings.TrimPrefix(strings.TrimSpace(req.Alias), "@")
	if alias == "" {
		return nil, fmt.Errorf("update friend alias: alias is required")
	}
	friend, userID, err := f.updateFriend(ctx, req.FriendID, "alias", map[string]any{
		"alias": alias,
	})
	if err != nil {
		return nil, err
	}
	return &UpdateFriendAliasResponse{Friend: friend, UserID: userID}, nil
}

func (f *FriendFacade) Remove(
	ctx context.Context,
	req *RemoveFriendRequest,
	opts ...func(*Option),
) (*RemoveFriendResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.FriendID) == "" {
		return nil, fmt.Errorf("remove friend: friend id is required")
	}
	friend, userID, err := f.updateFriend(ctx, req.FriendID, "remove", nil)
	if err != nil {
		return nil, err
	}
	return &RemoveFriendResponse{Friend: friend, UserID: userID}, nil
}

func (f *FriendFacade) Block(
	ctx context.Context,
	req *BlockFriendRequest,
	opts ...func(*Option),
) (*BlockFriendResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.FriendID) == "" {
		return nil, fmt.Errorf("block friend: friend id is required")
	}
	friend, userID, err := f.updateFriend(ctx, req.FriendID, "block", nil)
	if err != nil {
		return nil, err
	}
	return &BlockFriendResponse{Friend: friend, UserID: userID}, nil
}

func (f *FriendFacade) ResolveAlias(
	ctx context.Context,
	req *ResolveFriendAliasRequest,
	opts ...func(*Option),
) (*ResolveFriendAliasResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.Alias) == "" {
		return nil, fmt.Errorf("resolve friend: alias is required")
	}
	resp, err := f.List(ctx, &ListFriendsRequest{
		Status: friendStatusAccepted,
		Alias:  strings.TrimPrefix(strings.TrimSpace(req.Alias), "@"),
		Limit:  2,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Friends) == 0 {
		return nil, fmt.Errorf("resolve friend: no accepted friend for alias %q", req.Alias)
	}
	if len(resp.Friends) > 1 {
		return nil, fmt.Errorf("resolve friend: alias %q is ambiguous", req.Alias)
	}
	friend := resp.Friends[0]
	email := friend.RecipientEmail
	if friend.RecipientUserID == resp.UserID {
		email = friend.RequesterEmail
	}
	return &ResolveFriendAliasResponse{Friend: friend, Email: email}, nil
}

func (f *FriendFacade) updateFriend(
	ctx context.Context,
	friendID string,
	action string,
	body any,
) (*model.Friend, string, error) {
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, "", err
	}
	var envelope managerEnvelope[struct {
		Friend model.Friend `json:"friend"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodPost,
		credential.ManagerURL,
		userFriendPath(credential.UserID, friendID)+"/"+action,
		credential.APIKey,
		body,
		&envelope,
	); err != nil {
		return nil, "", err
	}
	return &envelope.Data.Friend, credential.UserID, nil
}

func userFriendPath(userID string, friendID string) string {
	path := "/api/v1/user/" + url.PathEscape(userID) + "/friends"
	if friendID != "" {
		path += "/" + url.PathEscape(friendID)
	}
	return path
}
