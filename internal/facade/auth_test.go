package facade

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type AuthFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestAuthFacadeSuite(t *testing.T) {
	suite.Run(t, new(AuthFacadeSuite))
}

func (s *AuthFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
}

func (s *AuthFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *AuthFacadeSuite) TestLoginWhoamiAndLogoutUseStoredBearerCredential() {
	var deleted bool
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/paxl/device-login/start":
			return jsonResponse(`{
				"data":{
					"login_id":"login-1",
					"user_code":"ABC123",
					"poll_token":"poll-1",
					"verification_uri":"https://manager.example/paxl-login.html",
					"verification_uri_complete":"https://manager.example/paxl-login.html?code=ABC123",
					"interval":0
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/paxl/device-login/poll":
			return jsonResponse(`{
				"data":{
					"status":"approved",
					"api_key":"paxu_test",
					"api_key_meta":{"key_id":"key-1"},
					"user":{"user_id":"usr_1","email":"cli@example.com","display_name":"CLI","role":"user"}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/self/me":
			s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
			return jsonResponse(`{
				"data":{
					"user":{"user_id":"usr_1","email":"cli@example.com","display_name":"CLI","role":"user"}
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v1/user/self/api-keys/key-1":
			s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
			deleted = true
			return jsonResponse(`{"data":{"ok":true},"code":200,"message":"ok"}`), nil
		default:
			return nil, fmt.Errorf(
				"unexpected manager request: %s %s",
				req.Method,
				req.URL.String(),
			)
		}
	})
	authFacade := NewAuthFacade(client, s.store)

	login, err := authFacade.Login(s.ctx, &LoginRequest{ManagerURL: "https://manager.example"})
	s.Require().NoError(err)
	s.Equal("ABC123", login.UserCode)
	s.Equal("cli@example.com", login.Credential.Email)

	whoami, err := authFacade.Whoami(s.ctx)
	s.Require().NoError(err)
	s.Equal("cli@example.com", whoami.User.Email)

	logout, err := authFacade.Logout(s.ctx)
	s.Require().NoError(err)
	s.Equal("cli@example.com", logout.Email)
	s.True(deleted)

	_, err = authFacade.Whoami(s.ctx)
	s.Require().Error(err)
	s.Contains(err.Error(), "not logged in")
}

func (s *AuthFacadeSuite) TestLoginRejectsTerminalStatus() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/paxl/device-login/start":
			return jsonResponse(`{
				"data":{"login_id":"login-1","user_code":"ABC123","poll_token":"poll-1","interval":0},
				"code":200,
				"message":"ok"
			}`), nil
		case "/api/v1/paxl/device-login/poll":
			return jsonResponse(`{"data":{"status":"expired"},"code":200,"message":"ok"}`), nil
		default:
			return nil, fmt.Errorf("unexpected manager request: %s", req.URL.Path)
		}
	})
	authFacade := NewAuthFacade(client, s.store)

	_, err := authFacade.Login(s.ctx, &LoginRequest{ManagerURL: "https://manager.example"})

	s.Require().Error(err)
	s.Contains(err.Error(), `status "expired"`)
}

func (s *AuthFacadeSuite) TestLoginUsesDefaultIntervalWhenManagerReturnsZeroInterval() {
	var pollCount atomic.Int64
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/paxl/device-login/start":
			return jsonResponse(`{
				"data":{"login_id":"login-1","user_code":"ABC123","poll_token":"poll-1","interval":0},
				"code":200,
				"message":"ok"
			}`), nil
		case "/api/v1/paxl/device-login/poll":
			pollCount.Add(1)
			return jsonResponse(`{"data":{"status":"pending"},"code":200,"message":"ok"}`), nil
		default:
			return nil, fmt.Errorf("unexpected manager request: %s", req.URL.Path)
		}
	})
	authFacade := NewAuthFacade(client, s.store)

	_, err := authFacade.Login(
		s.ctx,
		&LoginRequest{ManagerURL: "https://manager.example", Timeout: 25 * time.Millisecond},
	)

	s.Require().Error(err)
	s.Contains(err.Error(), "timed out")
	s.LessOrEqual(pollCount.Load(), int64(2))
}

func (s *AuthFacadeSuite) TestLogoutReturnsRevokeErrorAndKeepsCredential() {
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:   "https://manager.example",
			APIKey:       "paxu_test",
			UserAPIKeyID: "key-1",
			UserID:       "usr_1",
			Email:        "cli@example.com",
		},
	})
	s.Require().NoError(err)
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete && req.URL.Path == "/api/v1/user/self/api-keys/key-1" {
			resp := jsonResponse(`{}`)
			resp.StatusCode = http.StatusInternalServerError
			return resp, nil
		}
		return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
	})
	authFacade := NewAuthFacade(client, s.store)

	_, err = authFacade.Logout(s.ctx)

	s.Require().Error(err)
	s.Contains(err.Error(), "revoke api key")
	credential, err := s.store.GetAuthCredential(s.ctx)
	s.Require().NoError(err)
	s.Require().NotNil(credential.Credential)
	s.Equal("paxu_test", credential.Credential.APIKey)
}
