package facade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/assert"
)

func (s *AuthFacadeSuite) TestRegionalLoginGivenDifferentEmailsAndLostCommitResponseThenRestartKeepsOneIdentity() {
	var mu sync.Mutex
	var commits []string
	starts := 0
	lost := false
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		region := req.URL.Hostname()
		switch req.URL.Path {
		case "/api/v1/paxl/device-login/start":
			starts++
			return jsonResponse(
				fmt.Sprintf(
					`{"data":{"protocol":"client_commit_v1","region":%q,"login_id":%q,"user_code":"ABC123","poll_token":"secret","verification_uri":"https://paxworkspace.net/paxl-login.html","interval":1}}`,
					region,
					region,
				),
			), nil
		case "/api/v1/paxl/device-login/poll":
			var body map[string]string
			s.Require().NoError(json.NewDecoder(req.Body).Decode(&body))
			switch body["action"] {
			case "commit":
				commits = append(commits, region)
				s.Equal("usr_"+region, body["expected_user_id"])
				if !lost {
					lost = true
					return nil, fmt.Errorf("response lost after commit")
				}
				return jsonResponse(
					fmt.Sprintf(
						`{"data":{"status":"approved","region":%q,"api_key":"credential","api_key_meta":{"key_id":"key"},"user":{"user_id":%q,"email":%q}}}`,
						region,
						"usr_"+region,
						region+"@example.com",
					),
				), nil
			case "ack":
				return jsonResponse(`{"data":{"status":"consumed"}}`), nil
			case "cancel":
				return jsonResponse(`{"data":{"status":"cancelled"}}`), nil
			default:
				return jsonResponse(
					fmt.Sprintf(
						`{"data":{"status":"confirmed","region":%q,"user":{"user_id":%q,"email":%q}}}`,
						region,
						"usr_"+region,
						region+"@example.com",
					),
				), nil
			}
		default:
			return nil, fmt.Errorf("unexpected path")
		}
	})
	req := &LoginRequest{
		ClientCommit: true,
		ManagerURLs:  []string{"https://us", "https://hk"},
		Timeout:      time.Second,
	}
	_, err := NewAuthFacade(client, s.store).Login(s.ctx, req)
	s.Require().Error(err)
	result, err := NewAuthFacade(client, s.store).Login(s.ctx, req)
	s.Require().NoError(err)
	s.Equal(2, starts, "restart must reuse the stored regional requests")
	s.Require().Len(commits, 2)
	s.Equal(commits[0], commits[1], "never switch region after a potentially successful commit")
	s.Equal("usr_"+commits[0], result.Credential.UserID)
}

func (s *AuthFacadeSuite) TestRegionalLoginGivenLostAcknowledgementThenResumesSavedReceipt() {
	acked := false
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v1/paxl/device-login/start" {
			return jsonResponse(
				`{"data":{"protocol":"client_commit_v1","region":"hk","login_id":"login","user_code":"ABC123","poll_token":"poll","verification_uri":"https://paxworkspace.net/paxl-login.html"}}`,
			), nil
		}
		var body map[string]string
		s.Require().NoError(json.NewDecoder(req.Body).Decode(&body))
		switch body["action"] {
		case "":
			return jsonResponse(
				`{"data":{"status":"confirmed","region":"hk","user":{"user_id":"usr_hk","email":"owner@example.com"}}}`,
			), nil
		case "commit":
			if acked {
				return jsonResponse(`{"data":{"status":"consumed"}}`), nil
			}
			return jsonResponse(
				`{"data":{"status":"approved","region":"hk","api_key":"key","api_key_meta":{"key_id":"key_id"},"user":{"user_id":"usr_hk","email":"owner@example.com"}}}`,
			), nil
		case "ack":
			if !acked {
				acked = true
				return nil, fmt.Errorf("acknowledgement response lost")
			}
			return jsonResponse(`{"data":{"status":"consumed"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected action")
		}
	})
	req := &LoginRequest{
		ClientCommit: true,
		Admin:        true,
		ManagerURL:   "https://hk",
		OnStart: func(start *LoginStart) error {
			s.Contains(start.VerificationURIComplete, "admin=1")
			s.Contains(start.VerificationURIComplete, "region=hk")
			return nil
		},
	}
	_, err := NewAuthFacade(client, s.store).Login(s.ctx, req)
	s.ErrorContains(err, "credential saved")
	result, err := NewAuthFacade(client, s.store).Login(s.ctx, req)
	s.Require().NoError(err)
	s.Equal("key_id", result.Credential.UserAPIKeyID)
}

func (s *AuthFacadeSuite) TestRegionalLoginGivenUnsafeOrIncompleteServerResponsesThenNeverPersistCredentials() {
	for _, mode := range []string{"legacy", "offline", "bad-link", "callback", "bad-confirmation", "wrong-credential", "expired", "bad-ack"} {
		s.Run(mode, func() {
			client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if mode == "offline" {
					return nil, fmt.Errorf("offline")
				}
				if req.URL.Path == "/api/v1/paxl/device-login/start" {
					protocol := "client_commit_v1"
					uri := "https://paxworkspace.net/paxl-login.html"
					if mode == "legacy" {
						protocol = ""
					}
					if mode == "bad-link" {
						uri = "invalid"
					}
					return jsonResponse(
						fmt.Sprintf(
							`{"data":{"protocol":%q,"region":"hk","login_id":%q,"user_code":"ABC123","poll_token":"poll","verification_uri":%q}}`,
							protocol,
							mode,
							uri,
						),
					), nil
				}
				var body map[string]string
				s.Require().NoError(json.NewDecoder(req.Body).Decode(&body))
				if body["action"] == "" {
					if mode == "expired" {
						return jsonResponse(`{"data":{"status":"expired"}}`), nil
					}
					region := "hk"
					if mode == "bad-confirmation" {
						region = "us"
					}
					return jsonResponse(
						fmt.Sprintf(
							`{"data":{"status":"confirmed","region":%q,"user":{"user_id":"usr_hk","email":"owner@example.com"}}}`,
							region,
						),
					), nil
				}
				if mode == "bad-ack" {
					if body["action"] == "ack" {
						return jsonResponse(`{"data":{"status":"pending"}}`), nil
					}
					return jsonResponse(
						`{"data":{"status":"approved","region":"hk","api_key":"key","api_key_meta":{"key_id":"key_id"},"user":{"user_id":"usr_hk","email":"owner@example.com"}}}`,
					), nil
				}
				return jsonResponse(
					`{"data":{"status":"approved","region":"hk","api_key":"key","api_key_meta":{"key_id":"key_id"},"user":{"user_id":"usr_other","email":"other@example.com"}}}`,
				), nil
			})
			req := &LoginRequest{
				ClientCommit: true,
				ManagerURL:   "https://" + mode,
				Timeout:      20 * time.Millisecond,
			}
			if mode == "callback" {
				req.OnStart = func(*LoginStart) error { return fmt.Errorf("display failed") }
			}
			_, err := NewAuthFacade(client, s.store).Login(s.ctx, req)
			s.Require().Error(err)
		})
	}
	_, err := NewAuthFacade(
		nil,
		s.store,
	).Login(s.ctx, &LoginRequest{ClientCommit: true, Admin: true})
	s.ErrorContains(err, "explicit manager")
}

func TestRegionalLoginGivenCandidateLinksThenOnlyUnambiguousRegionsAreOffered(t *testing.T) {
	for _, tc := range []struct {
		name, region, uri string
		admin             bool
	}{
		{"unknown", "other", "https://pax.example", false},
		{"duplicate", "us", "https://pax.example", false},
		{"admin without region", "", "https://pax.example", true},
		{"invalid URL", "hk", "://", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := []*store.LoginCandidate{
				{Region: tc.region, VerificationURI: tc.uri, UserCode: "ABC123"},
			}
			if !tc.admin {
				candidates = append(
					candidates,
					&store.LoginCandidate{Region: "us", UserCode: "XYZ123"},
				)
			}
			_, err := regionalLoginLink(candidates, tc.admin)
			assert.Error(t, err)
		})
	}
	assert.Equal(t, 30*time.Second, regionalPollInterval([]*store.LoginCandidate{{Interval: 60}}))
	_, err := validateLoginTargets(nil)
	assert.Error(t, err)
	_, err = validateLoginTargets([]string{"https://one", "https://two", "https://three"})
	assert.Error(t, err)
	targets, err := validateLoginTargets([]string{"http://127.0.0.1:123", "http://127.0.0.1:123"})
	assert.NoError(t, err)
	assert.Len(t, targets, 1)
}

func TestRegionalLoginGivenMixedOrUnsafeTargetsThenRejectBeforeStarting(t *testing.T) {
	for _, target := range []string{"http://remote.example", "https://user:pass@example.com", "https://example.com/?token=x"} {
		t.Run(
			target,
			func(t *testing.T) { _, err := validateLoginTargets([]string{target}); assert.Error(t, err) },
		)
	}
}
