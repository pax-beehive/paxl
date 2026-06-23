package facade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/suite"
)

type EnvelopeFacadeSuite struct {
	suite.Suite
	ctx   context.Context
	store *store.Store
}

func TestEnvelopeFacadeSuite(t *testing.T) {
	suite.Run(t, new(EnvelopeFacadeSuite))
}

func (s *EnvelopeFacadeSuite) SetupTest() {
	s.ctx = context.Background()
	opened, err := store.Open(
		s.ctx,
		&store.OpenRequest{Path: filepath.Join(s.T().TempDir(), "paxl.sqlite")},
	)
	s.Require().NoError(err)
	s.store = opened.Store
	s.seedCredential()
}

func (s *EnvelopeFacadeSuite) TearDownTest() {
	s.Require().NoError(s.store.Close())
}

func (s *EnvelopeFacadeSuite) TestSendPostsLocalCapsuleAsEnvelope() {
	s.seedCapsule("kcap_local")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/api/v1/user/usr_1/envelopes", req.URL.Path)
		s.Equal("Bearer paxu_test", req.Header.Get("Authorization"))
		body, err := io.ReadAll(req.Body)
		s.Require().NoError(err)
		s.Contains(string(body), `"recipient_email":"other@example.com"`)
		s.Contains(string(body), `"payload_type":"knowledge_capsule"`)
		s.Contains(string(body), `"capsule_id":"kcap_local"`)
		return jsonResponse(`{
			"data":{
				"envelope":{
					"envelope_id":"env_1",
					"sender_user_id":"usr_1",
					"sender_email":"cli@example.com",
					"recipient_email":"other@example.com",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"pending",
					"created_at":"2026-06-22T00:00:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.Send(s.ctx, &SendEnvelopeRequest{
		CapsuleID:      "kcap_local",
		RecipientEmail: "other@example.com",
		Message:        "please review",
	})

	s.Require().NoError(err)
	s.Equal("env_1", resp.Envelope.EnvelopeID)
	s.Equal("pending", resp.Envelope.Status)
}

func (s *EnvelopeFacadeSuite) TestAcceptStoresEnvelopePayloadAsLocalCapsule() {
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v1",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"handoff",
			"title":"Remote handoff",
			"summary":"summary",
			"content":"content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		}
	}`, "\n", "")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1":
			return jsonResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"sender_user_id":"usr_sender",
						"sender_email":"sender@example.com",
						"recipient_user_id":"usr_1",
						"recipient_email":"cli@example.com",
						"payload_type":"knowledge_capsule",
						"payload_json":%s,
						"status":"pending",
						"created_at":"2026-06-22T00:00:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`, payload)), nil
		case req.Method == http.MethodPost &&
			req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/accept":
			return jsonResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"accepted",
						"created_at":"2026-06-22T00:00:00Z",
						"accepted_at":"2026-06-22T00:01:00Z"
					}
				},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
		}
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.Accept(s.ctx, &AcceptEnvelopeRequest{EnvelopeID: "env_1"})

	s.Require().NoError(err)
	s.Equal("accepted", resp.Envelope.Status)
	s.Equal("Remote handoff", resp.Capsule.Title)
	s.Equal("remote_envelope:env_1", resp.Capsule.SourceSessionID)
	got, err := s.store.GetKnowledgeCapsule(
		s.ctx,
		&store.GetKnowledgeCapsuleRequest{CapsuleID: resp.Capsule.CapsuleID},
	)
	s.Require().NoError(err)
	s.Equal("content", got.Capsule.Content)
}

func (s *EnvelopeFacadeSuite) TestListInboxBuildsStatusAndLimitQuery() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/envelopes", req.URL.Path)
		s.Equal("accepted", req.URL.Query().Get("status"))
		s.Equal("10", req.URL.Query().Get("limit"))
		return jsonResponse(`{
			"data":{
				"envelopes":[{
					"envelope_id":"env_1",
					"sender_email":"sender@example.com",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"accepted",
					"created_at":"2026-06-22T00:00:00Z"
				}]
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.ListInbox(s.ctx, &ListInboxRequest{Status: "accepted", Limit: 10})

	s.Require().NoError(err)
	s.Len(resp.Envelopes, 1)
	s.Equal("env_1", resp.Envelopes[0].EnvelopeID)
}

func (s *EnvelopeFacadeSuite) TestListOutboxBuildsSentDirectionQuery() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/envelopes", req.URL.Path)
		s.Equal("sent", req.URL.Query().Get("direction"))
		s.Equal("accepted", req.URL.Query().Get("status"))
		s.Equal("10", req.URL.Query().Get("limit"))
		return jsonResponse(`{
			"data":{
				"envelopes":[{
					"envelope_id":"env_1",
					"recipient_email":"recipient@example.com",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"accepted",
					"created_at":"2026-06-22T00:00:00Z"
				}]
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.ListOutbox(
		s.ctx,
		&ListOutboxRequest{Status: "accepted", Limit: 10},
	)

	s.Require().NoError(err)
	s.Len(resp.Envelopes, 1)
	s.Equal("env_1", resp.Envelopes[0].EnvelopeID)
}

func (s *EnvelopeFacadeSuite) TestGetFetchesEnvelopeByID() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		s.Equal("/api/v1/user/usr_1/envelopes/env_1", req.URL.Path)
		return jsonResponse(`{
			"data":{
				"envelope":{
					"envelope_id":"env_1",
					"sender_email":"sender@example.com",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"pending",
					"created_at":"2026-06-22T00:00:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.Get(s.ctx, &GetEnvelopeRequest{EnvelopeID: "env_1"})

	s.Require().NoError(err)
	s.Equal("env_1", resp.Envelope.EnvelopeID)
	s.Equal("pending", resp.Envelope.Status)
}

func (s *EnvelopeFacadeSuite) TestArchivePostsArchiveAction() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/api/v1/user/usr_1/envelopes/env_1/archive", req.URL.Path)
		return jsonResponse(`{
			"data":{
				"envelope":{
					"envelope_id":"env_1",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"archived",
					"created_at":"2026-06-22T00:00:00Z",
					"archived_at":"2026-06-22T00:01:00Z"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.Archive(s.ctx, &ArchiveEnvelopeRequest{EnvelopeID: "env_1"})

	s.Require().NoError(err)
	s.Equal("archived", resp.Envelope.Status)
	s.Equal("2026-06-22T00:01:00Z", resp.Envelope.ArchivedAt)
}

func (s *EnvelopeFacadeSuite) TestAcceptRejectsUnsupportedPayloadType() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodGet, req.Method)
		return jsonResponse(`{
			"data":{
				"envelope":{
					"envelope_id":"env_1",
					"payload_type":"unknown",
					"payload_json":{},
					"status":"pending"
				}
			},
			"code":200,
			"message":"ok"
		}`), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	_, err := envelopeFacade.Accept(s.ctx, &AcceptEnvelopeRequest{EnvelopeID: "env_1"})

	s.Require().Error(err)
	s.Contains(err.Error(), "unsupported payload type")
}

func (s *EnvelopeFacadeSuite) seedCredential() {
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

func (s *EnvelopeFacadeSuite) seedCapsule(capsuleID string) {
	_, err := s.store.CreateKnowledgeCapsule(
		s.ctx,
		&store.CreateKnowledgeCapsuleRequest{Capsule: &model.KnowledgeCapsule{
			CapsuleID:       capsuleID,
			SourceSessionID: "codex:source",
			SourceAgent:     model.AgentNameCodex,
			Keyword:         "handoff",
			Title:           "Local handoff",
			Summary:         "summary",
			Content:         "content",
		}},
	)
	s.Require().NoError(err)
}
