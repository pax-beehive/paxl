package facade

import (
	"context"
	"encoding/json"
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

func (s *EnvelopeFacadeSuite) TestSendPostsRoutedCapsuleEnvelope() {
	s.seedCapsule("kcap_local")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal(http.MethodPost, req.Method)
		s.Equal("/api/v1/user/usr_1/envelopes", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		s.Require().NoError(err)
		var posted struct {
			PayloadJSON json.RawMessage `json:"payload_json"`
		}
		s.Require().NoError(json.Unmarshal(body, &posted))
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Capsule       struct {
				CapsuleID       string          `json:"capsule_id"`
				SourceSessionID string          `json:"source_session_id"`
				SourceAgent     model.AgentName `json:"source_agent"`
				Status          string          `json:"status"`
			} `json:"capsule"`
			Route struct {
				MatchType   string          `json:"match_type"`
				MatchValue  string          `json:"match_value"`
				TargetAgent model.AgentName `json:"target_agent"`
			} `json:"route"`
		}
		s.Require().NoError(json.Unmarshal(posted.PayloadJSON, &payload))
		s.Equal("paxl.envelope_payload.knowledge_capsule.v2", payload.SchemaVersion)
		s.Equal("kcap_local", payload.Capsule.CapsuleID)
		s.Equal("codex:source", payload.Capsule.SourceSessionID)
		s.Equal(model.AgentNameCodex, payload.Capsule.SourceAgent)
		s.Equal("active", payload.Capsule.Status)
		s.Equal("project", payload.Route.MatchType)
		s.Equal("pax-manager", payload.Route.MatchValue)
		s.Equal(model.AgentNameCodex, payload.Route.TargetAgent)
		return jsonResponse(`{
			"data":{
				"envelope":{
					"envelope_id":"env_1",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"pending"
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
		MatchType:      "project",
		MatchValue:     "pax-manager",
		TargetAgent:    model.AgentNameCodex,
	})

	s.Require().NoError(err)
	s.Equal("env_1", resp.Envelope.EnvelopeID)
}

func (s *EnvelopeFacadeSuite) TestSendWithCallerAgentPostsAgentEnvelope() {
	s.seedCredentialWithNode("node_paxl")
	s.seedCapsule("kcap_local")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet &&
			req.URL.Path == "/api/v1/user/usr_1/nodes/node_paxl/agents":
			return jsonResponse(`{
				"data":{
					"agents":[{
						"agent_id":"agent_from",
						"node_id":"node_paxl",
						"name":"local-codex",
						"agent_type":"codex",
						"status":"active"
					}]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			body, err := io.ReadAll(req.Body)
			s.Require().NoError(err)
			s.Contains(string(body), `"from_agent_id":"agent_from"`)
			s.Contains(string(body), `"to_agent_id":"agent_to"`)
			s.NotContains(string(body), "recipient_email")
			return jsonResponse(`{
				"data":{
					"envelope":{
						"envelope_id":"env_1",
						"from_agent_id":"agent_from",
						"to_agent_id":"agent_to",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"pending"
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

	resp, err := envelopeFacade.Send(s.ctx, &SendEnvelopeRequest{
		CapsuleID:   "kcap_local",
		CallerAgent: model.AgentNameCodex,
		ToAgentID:   "agent_to",
	})

	s.Require().NoError(err)
	s.Equal("env_1", resp.Envelope.EnvelopeID)
	s.Equal("agent_from", resp.Envelope.FromAgentID)
	s.Equal("agent_to", resp.Envelope.ToAgentID)
}

func (s *EnvelopeFacadeSuite) TestSendRejectsInvalidRouteRequests() {
	s.seedCapsule("kcap_local")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)
	cases := []struct {
		name string
		req  SendEnvelopeRequest
		want string
	}{
		{
			name: "agent without match",
			req: SendEnvelopeRequest{
				CapsuleID:      "kcap_local",
				RecipientEmail: "other@example.com",
				TargetAgent:    model.AgentNameCodex,
			},
			want: "requires --match",
		},
		{
			name: "project without value",
			req: SendEnvelopeRequest{
				CapsuleID:      "kcap_local",
				RecipientEmail: "other@example.com",
				MatchType:      "project",
			},
			want: "route match value is required",
		},
		{
			name: "any with value",
			req: SendEnvelopeRequest{
				CapsuleID:      "kcap_local",
				RecipientEmail: "other@example.com",
				MatchType:      "any",
				MatchValue:     "paxl",
			},
			want: "must be empty",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := envelopeFacade.Send(s.ctx, &tc.req)
			s.Require().Error(err)
			s.Contains(err.Error(), tc.want)
		})
	}
}

func (s *EnvelopeFacadeSuite) TestSendRejectsCallerAgentWithoutTargetAgent() {
	s.seedCapsule("kcap_local")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	_, err := envelopeFacade.Send(s.ctx, &SendEnvelopeRequest{
		CapsuleID:      "kcap_local",
		RecipientEmail: "other@example.com",
		CallerAgent:    model.AgentNameCodex,
	})

	s.Require().Error(err)
	s.Contains(err.Error(), "caller agent delivery requires to_agent_id")
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

func (s *EnvelopeFacadeSuite) TestAcceptAllStoresPendingEnvelopesAsLocalCapsules() {
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
		case req.Method == http.MethodGet &&
			req.URL.Path == "/api/v1/user/usr_1/envelopes":
			s.Equal("pending", req.URL.Query().Get("status"))
			return jsonResponse(`{
				"data":{
					"envelopes":[
						{"envelope_id":"env_1","payload_type":"knowledge_capsule","status":"pending"},
						{"envelope_id":"env_2","payload_type":"knowledge_capsule","status":"pending"}
					]
				},
				"code":200,
				"message":"ok"
			}`), nil
		case req.Method == http.MethodGet &&
			(req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1" ||
				req.URL.Path == "/api/v1/user/usr_1/envelopes/env_2"):
			envelopeID := strings.TrimPrefix(req.URL.Path, "/api/v1/user/usr_1/envelopes/")
			return jsonResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"%s",
						"sender_user_id":"usr_sender",
						"recipient_user_id":"usr_1",
						"payload_type":"knowledge_capsule",
						"payload_json":%s,
						"status":"pending"
					}
				},
				"code":200,
				"message":"ok"
			}`, envelopeID, payload)), nil
		case req.Method == http.MethodPost &&
			(req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/accept" ||
				req.URL.Path == "/api/v1/user/usr_1/envelopes/env_2/accept"):
			envelopeID := strings.TrimSuffix(
				strings.TrimPrefix(req.URL.Path, "/api/v1/user/usr_1/envelopes/"),
				"/accept",
			)
			return jsonResponse(fmt.Sprintf(`{
				"data":{
					"envelope":{
						"envelope_id":"%s",
						"payload_type":"knowledge_capsule",
						"payload_json":{},
						"status":"accepted"
					}
				},
				"code":200,
				"message":"ok"
			}`, envelopeID)), nil
		default:
			return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
		}
	})
	envelopeFacade := NewEnvelopeFacade(client, s.store)

	resp, err := envelopeFacade.AcceptAll(s.ctx, &AcceptAllEnvelopesRequest{})

	s.Require().NoError(err)
	s.Require().Len(resp.Accepted, 2)
	s.Empty(resp.Failures)
	listed, err := s.store.ListKnowledgeCapsules(s.ctx, &store.ListKnowledgeCapsulesRequest{})
	s.Require().NoError(err)
	s.Len(listed.Capsules, 2)
}

func (s *EnvelopeFacadeSuite) TestAcceptStoresRoutedEnvelopeAsPendingHookInjection() {
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_node_id":"remote-node",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"handoff",
			"title":"Remote handoff",
			"summary":"summary",
			"content":"content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		},
		"route":{
			"match_type":"keyword",
			"match_value":"pax-manager capsule test",
			"target_agent":"codex"
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
						"status":"pending"
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
						"status":"accepted"
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
	s.Require().NotNil(resp.Injection)
	s.Equal(resp.Capsule.CapsuleID, resp.Injection.CapsuleID)
	s.Equal("pending", resp.Injection.Status)
	s.Equal("hook", resp.Injection.DeliveryMethod)
	s.Equal(model.AgentNameCodex, resp.Injection.TargetAgent)
	s.Equal("keyword", resp.Injection.RouteMatchType)
	s.Equal("pax-manager capsule test", resp.Injection.RouteMatchValue)
	listed, err := s.store.ListKnowledgeInjections(s.ctx, &store.ListKnowledgeInjectionsRequest{})
	s.Require().NoError(err)
	s.Require().Len(listed.Injections, 1)
	s.Equal(resp.Injection.InjectionID, listed.Injections[0].InjectionID)
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
	s.seedCredentialWithNode("")
}

func (s *EnvelopeFacadeSuite) seedCredentialWithNode(nodeID string) {
	_, err := s.store.SaveAuthCredential(s.ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:  "https://manager.example",
			APIKey:      "paxu_test",
			NodeID:      nodeID,
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
