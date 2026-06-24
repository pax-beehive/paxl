package facade

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type FriendFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestFriendFacadeSuite(t *testing.T) {
	suite.Run(t, new(FriendFacadeSuite))
}

func (s *FriendFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
	s.seedCredential()
}

func (s *FriendFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *FriendFacadeSuite) TestRequestPostsFriendRequest() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/api/v1/user/usr_1/friends", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		body, err := io.ReadAll(req.Body)
		s.Require().NoError(err)
		s.Contains(string(body), `"email":"bob@example.com"`)
		s.Contains(string(body), `"alias":"bob"`)
		return jsonResponse(`{
			"data":{
				"friend":{
					"friend_id":"fr_1",
					"requester_user_id":"usr_1",
					"requester_email":"cli@example.com",
					"requester_alias":"bob",
					"recipient_email":"bob@example.com",
					"status":"pending",
					"created_at":"2026-06-22T00:00:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	friendFacade := NewFriendFacade(client, s.store)

	resp, err := friendFacade.Request(s.ctx, &RequestFriendRequest{
		Email: "bob@example.com",
		Alias: "@bob",
	})

	s.Require().NoError(err)
	s.Equal("fr_1", resp.Friend.FriendID)
	s.Equal("bob", resp.Friend.RequesterAlias)
}

func (s *FriendFacadeSuite) TestAcceptPostsAlias() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/api/v1/user/usr_1/friends/fr_1/accept", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		s.Require().NoError(err)
		s.Contains(string(body), `"alias":"alice"`)
		return jsonResponse(`{
			"data":{
				"friend":{
					"friend_id":"fr_1",
					"requester_user_id":"usr_sender",
					"requester_email":"alice@example.com",
					"recipient_user_id":"usr_1",
					"recipient_email":"cli@example.com",
					"recipient_alias":"alice",
					"status":"accepted",
					"created_at":"2026-06-22T00:00:00Z",
					"accepted_at":"2026-06-22T00:01:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	friendFacade := NewFriendFacade(client, s.store)

	resp, err := friendFacade.Accept(s.ctx, &AcceptFriendRequest{FriendID: "fr_1", Alias: "@alice"})

	s.Require().NoError(err)
	s.Equal("accepted", resp.Friend.Status)
	s.Equal("alice", resp.Friend.RecipientAlias)
}

func (s *FriendFacadeSuite) TestUpdateAliasPostsAliasAction() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/api/v1/user/usr_1/friends/fr_1/alias", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		s.Require().NoError(err)
		s.Contains(string(body), `"alias":"teammate"`)
		return jsonResponse(`{
			"data":{
				"friend":{
					"friend_id":"fr_1",
					"requester_user_id":"usr_1",
					"requester_email":"cli@example.com",
					"requester_alias":"teammate",
					"recipient_email":"bob@example.com",
					"status":"accepted",
					"created_at":"2026-06-22T00:00:00Z",
					"accepted_at":"2026-06-22T00:01:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	friendFacade := NewFriendFacade(client, s.store)

	resp, err := friendFacade.UpdateAlias(
		s.ctx,
		&UpdateFriendAliasRequest{FriendID: "fr_1", Alias: "@teammate"},
	)

	s.Require().NoError(err)
	s.Equal("accepted", resp.Friend.Status)
	s.Equal("teammate", resp.Friend.RequesterAlias)
}

func (s *FriendFacadeSuite) TestGetFetchesFriendByID() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/friends/fr_1", req.URL.Path)
		return jsonResponse(`{
			"data":{
				"friend":{
					"friend_id":"fr_1",
					"requester_user_id":"usr_1",
					"requester_email":"cli@example.com",
					"requester_alias":"bob",
					"recipient_email":"bob@example.com",
					"status":"accepted",
					"created_at":"2026-06-22T00:00:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	friendFacade := NewFriendFacade(client, s.store)

	resp, err := friendFacade.Get(s.ctx, &GetFriendRequest{FriendID: "fr_1"})

	s.Require().NoError(err)
	s.Equal("fr_1", resp.Friend.FriendID)
	s.Equal("usr_1", resp.UserID)
}

func (s *FriendFacadeSuite) TestRemoveAndBlockPostActions() {
	seen := make(map[string]bool)
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		seen[req.URL.Path] = true
		return jsonResponse(`{
			"data":{
				"friend":{
					"friend_id":"fr_1",
					"requester_user_id":"usr_1",
					"requester_email":"cli@example.com",
					"requester_alias":"bob",
					"recipient_email":"bob@example.com",
					"status":"removed",
					"created_at":"2026-06-22T00:00:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	friendFacade := NewFriendFacade(client, s.store)

	removed, err := friendFacade.Remove(s.ctx, &RemoveFriendRequest{FriendID: "fr_1"})
	s.Require().NoError(err)
	blocked, err := friendFacade.Block(s.ctx, &BlockFriendRequest{FriendID: "fr_1"})
	s.Require().NoError(err)

	s.Equal("fr_1", removed.Friend.FriendID)
	s.Equal("fr_1", blocked.Friend.FriendID)
	s.True(seen["/api/v1/user/usr_1/friends/fr_1/remove"])
	s.True(seen["/api/v1/user/usr_1/friends/fr_1/block"])
}

func (s *FriendFacadeSuite) TestResolveAliasReturnsCounterpartyEmail() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/friends", req.URL.Path)
		s.Equal("accepted", req.URL.Query().Get("status"))
		s.Equal("bob", req.URL.Query().Get("alias"))
		return jsonResponse(`{
			"data":{
				"friends":[{
					"friend_id":"fr_1",
					"requester_user_id":"usr_1",
					"requester_email":"cli@example.com",
					"requester_alias":"bob",
					"recipient_user_id":"usr_bob",
					"recipient_email":"bob@example.com",
					"status":"accepted",
					"created_at":"2026-06-22T00:00:00Z"
				}]
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	friendFacade := NewFriendFacade(client, s.store)

	resp, err := friendFacade.ResolveAlias(s.ctx, &ResolveFriendAliasRequest{Alias: "@bob"})

	s.Require().NoError(err)
	s.Equal("bob@example.com", resp.Email)
}

func (s *FriendFacadeSuite) seedCredential() {
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:  "https://manager.example",
			APIKey:      "paxu_test",
			UserID:      "usr_1",
			Email:       "cli@example.com",
			DisplayName: "CLI",
			Role:        "user",
		},
	})
	s.Require().NoError(err)
}
