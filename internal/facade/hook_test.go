package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/require"
)

func TestAgentHookAcceptsInboxRoutesBeforeClaimingInjection(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opened.Store.Close())
	}()
	_, err = opened.Store.SaveAuthCredential(ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:  "https://manager.example",
			APIKey:      "paxu_test",
			UserID:      "usr_1",
			Email:       "cli@example.com",
			DisplayName: "CLI",
			Role:        "user",
		},
	})
	require.NoError(t, err)
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_node_id":"remote-node",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"handoff",
			"title":"Remote routed handoff",
			"summary":"summary",
			"content":"Remote routed content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		},
		"route":{
			"match_type":"keyword",
			"match_value":"handoff",
			"target_agent":"claude"
		}
	}`, "\n", "")
	accepted := false
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			switch req.URL.Query().Get("status") {
			case "pending":
				return jsonResponse(`{
					"data":{"envelopes":[{"envelope_id":"env_1","payload_type":"knowledge_capsule","status":"pending"}]},
					"code":200,
					"message":"ok"
				}`), nil
			case "accepted":
				return jsonResponse(`{
					"data":{"envelopes":[]},
					"code":200,
					"message":"ok"
				}`), nil
			default:
				return nil, fmt.Errorf("unexpected status query: %s", req.URL.Query().Get("status"))
			}
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1":
			return jsonResponse(fmt.Sprintf(`{
				"data":{"envelope":{
					"envelope_id":"env_1",
					"sender_user_id":"usr_sender",
					"recipient_user_id":"usr_1",
					"payload_type":"knowledge_capsule",
					"payload_json":%s,
					"status":"pending"
				}},
				"code":200,
				"message":"ok"
			}`, payload)), nil
		case req.Method == http.MethodPost &&
			req.URL.Path == "/api/v1/user/usr_1/envelopes/env_1/accept":
			accepted = true
			return jsonResponse(`{
				"data":{"envelope":{
					"envelope_id":"env_1",
					"payload_type":"knowledge_capsule",
					"payload_json":{},
					"status":"accepted"
				}},
				"code":200,
				"message":"ok"
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
		}
	})
	hookFacade := &AgentHookFacade{client: client, store: opened.Store}

	consumed, err := hookFacade.Run(ctx, &AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "user-prompt",
		SessionID: "remote-session",
		Prompt:    "please use this handoff",
	})

	require.NoError(t, err)
	require.True(t, accepted)
	require.NotNil(t, consumed.Injection)
	require.Equal(t, "claimed", consumed.Injection.Status)
	require.Equal(t, "claude:remote-session", consumed.Injection.TargetSessionID)
	require.Contains(t, consumed.Message, "Remote routed content")
	listed, err := opened.Store.ListKnowledgeCapsules(ctx, &store.ListKnowledgeCapsulesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Capsules, 1)
	require.Equal(t, "Remote routed handoff", listed.Capsules[0].Title)
}

func TestAgentHookSyncsAcceptedInboxRoutesBeforeClaimingInjection(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opened.Store.Close())
	}()
	_, err = opened.Store.SaveAuthCredential(ctx, &store.SaveAuthCredentialRequest{
		Credential: &model.AuthCredential{
			ManagerURL:  "https://manager.example",
			APIKey:      "paxu_test",
			UserID:      "usr_1",
			Email:       "cli@example.com",
			DisplayName: "CLI",
			Role:        "user",
		},
	})
	require.NoError(t, err)
	payload := strings.ReplaceAll(`{
		"schema_version":"paxl.envelope_payload.knowledge_capsule.v2",
		"capsule":{
			"capsule_id":"kcap_remote",
			"source_node_id":"remote-node",
			"source_session_id":"codex:source",
			"source_agent":"codex",
			"keyword":"monitor",
			"title":"Monitor audit frontend integration",
			"summary":"summary",
			"content":"Monitor audit content",
			"status":"active",
			"created_at":"2026-06-22T00:00:00Z"
		},
		"route":{
			"match_type":"keyword",
			"match_value":"monitor",
			"target_agent":"codex"
		}
	}`, "\n", "")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/user/usr_1/envelopes":
			switch req.URL.Query().Get("status") {
			case "pending":
				return jsonResponse(`{
					"data":{"envelopes":[]},
					"code":200,
					"message":"ok"
				}`), nil
			case "accepted":
				return jsonResponse(`{
					"data":{"envelopes":[{"envelope_id":"env_accepted","payload_type":"knowledge_capsule","status":"accepted"}]},
					"code":200,
					"message":"ok"
				}`), nil
			default:
				return nil, fmt.Errorf("unexpected status query: %s", req.URL.Query().Get("status"))
			}
		case req.Method == http.MethodGet &&
			req.URL.Path == "/api/v1/user/usr_1/envelopes/env_accepted":
			return jsonResponse(fmt.Sprintf(`{
				"data":{"envelope":{
					"envelope_id":"env_accepted",
					"sender_user_id":"usr_sender",
					"recipient_user_id":"usr_1",
					"payload_type":"knowledge_capsule",
					"payload_json":%s,
					"status":"accepted"
				}},
				"code":200,
				"message":"ok"
			}`, payload)), nil
		default:
			return nil, fmt.Errorf("unexpected manager request: %s %s", req.Method, req.URL.Path)
		}
	})
	hookFacade := &AgentHookFacade{client: client, store: opened.Store}

	consumed, err := hookFacade.Run(ctx, &AgentHookRequest{
		Agent:     model.AgentNameCodex,
		Event:     "user-prompt",
		SessionID: "monitor-session",
		Prompt:    "monitor",
	})

	require.NoError(t, err)
	require.NotNil(t, consumed.Injection)
	require.Equal(t, "claimed", consumed.Injection.Status)
	require.Equal(t, "codex:monitor-session", consumed.Injection.TargetSessionID)
	require.Contains(t, consumed.Message, "Monitor audit content")
	listed, err := opened.Store.ListKnowledgeCapsules(ctx, &store.ListKnowledgeCapsulesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Capsules, 1)
	require.Equal(t, "Monitor audit frontend integration", listed.Capsules[0].Title)
}

func TestAgentHookContinuesWhenOneAutoReceiveChannelFails(t *testing.T) {
	previousTimeout := hookChannelPollTimeout
	hookChannelPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() { hookChannelPollTimeout = previousTimeout })
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	for _, profile := range []*model.ChannelProfile{
		{ProfileID: "chp_broken", Name: "broken", Kind: model.ChannelKindOnPrem, URL: "https://broken.internal", APIKey: "tm_broken", Enabled: true, AutoReceive: true},
		{ProfileID: "chp_good", Name: "good", Kind: model.ChannelKindOnPrem, URL: "https://good.internal", APIKey: "tm_good", Enabled: true, AutoReceive: true},
	} {
		_, err := opened.Store.SaveChannelProfile(
			ctx,
			&store.SaveChannelProfileRequest{Profile: profile},
		)
		require.NoError(t, err)
	}
	payload := `{"schema_version":"paxl.envelope_payload.knowledge_capsule.v2","capsule":{"capsule_id":"remote","source_session_id":"codex:source","source_agent":"codex","keyword":"handoff","title":"Remote","summary":"summary","content":"content","status":"active","truncated":false,"original_estimated_chars":7},"route":{"match_type":"keyword","match_value":"handoff","target_agent":"codex"}}`
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "broken.internal" {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		if req.URL.Host != "good.internal" {
			return nil, fmt.Errorf("unexpected host %s", req.URL.Host)
		}
		if req.URL.Path == "/v1/channel/envelopes" {
			if req.URL.Query().Get("status") == "accepted" {
				return jsonResponse(`{"envelopes":[]}`), nil
			}
			return jsonResponse(
				`{"envelopes":[{"envelope_id":"env-good","payload_type":"knowledge_capsule","status":"pending"}]}`,
			), nil
		}
		status := "pending"
		if req.Method == http.MethodPost {
			status = "accepted"
		}
		return jsonResponse(
			fmt.Sprintf(
				`{"envelope":{"envelope_id":"env-good","from_user_id":"sender","from_agent_id":"sender-agent","to_user_id":"user","to_agent_id":"receiver","payload_type":"knowledge_capsule","payload_json":%s,"status":%q,"created_at":"2026-07-22T00:00:00Z"}}`,
				payload,
				status,
			),
		), nil
	})
	hookFacade := &AgentHookFacade{client: client, store: opened.Store}

	result, err := hookFacade.Run(ctx, &AgentHookRequest{
		Agent: model.AgentNameCodex, Event: "user-prompt", SessionID: "session", Prompt: "handoff",
	})

	require.NoError(t, err)
	require.NotNil(t, result.Injection)
	require.Equal(t, "remote_envelope:onprem:chp_good:env-good", result.Injection.SourceSessionID)
}

func TestAgentHookSupportsHermesPreLLMCallContextOutput(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opened.Store.Close())
	}()
	_, err = opened.Store.CreateKnowledgeCapsule(
		ctx,
		&store.CreateKnowledgeCapsuleRequest{Capsule: &model.KnowledgeCapsule{
			CapsuleID:       "kcap_hermes",
			SourceNodeID:    "local-node",
			SourceSessionID: "codex:source",
			SourceAgent:     model.AgentNameCodex,
			Keyword:         "handoff",
			Title:           "Hermes handoff",
			Summary:         "summary",
			Content:         "Hermes should receive this context.",
			Status:          "active",
			CreatedAt:       "2026-06-29T00:00:00Z",
		}},
	)
	require.NoError(t, err)
	_, err = opened.Store.CreateKnowledgeInjection(
		ctx,
		&store.CreateKnowledgeInjectionRequest{Injection: &model.KnowledgeInjection{
			InjectionID:         "kci_hermes",
			CapsuleID:           "kcap_hermes",
			SourceNodeID:        "local-node",
			SourceAgent:         model.AgentNameCodex,
			SourceSessionID:     "codex:source",
			TargetNodeID:        "local-node",
			TargetAgent:         model.AgentNameHermes,
			DeliveryMethod:      "hook",
			DeliveryMessageType: "system_handoff",
			Status:              "pending",
			RouteMatchType:      "any",
		}},
	)
	require.NoError(t, err)
	hookFacade := NewAgentHookFacade(opened.Store)

	claimed, err := hookFacade.Run(ctx, &AgentHookRequest{
		Agent:     model.AgentNameHermes,
		Event:     "pre_llm_call",
		SessionID: "hermes-session",
		Prompt:    "continue",
	})
	require.NoError(t, err)
	require.NotNil(t, claimed.Injection)
	require.Contains(t, claimed.Message, "Hermes should receive this context.")

	delivered, err := hookFacade.Deliver(ctx, &DeliverAgentHookRequest{
		Agent:       model.AgentNameHermes,
		SessionID:   "hermes-session",
		InjectionID: claimed.Injection.InjectionID,
		Message:     claimed.Message,
	})
	require.NoError(t, err)
	var output map[string]string
	require.NoError(t, json.Unmarshal([]byte(delivered.Message), &output))
	require.Contains(t, output["context"], "Hermes should receive this context.")
}

func TestAgentHookTurnEndEventReturnsEmptyWithoutError(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opened.Store.Close())
	}()
	hookFacade := NewAgentHookFacade(opened.Store)

	// Turn-end event with no session in store should not error
	resp, err := hookFacade.Run(ctx, &AgentHookRequest{
		Agent:     model.AgentNameClaude,
		Event:     "turn-end",
		SessionID: "nonexistent-session",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Empty(t, resp.Message)
}

func TestAgentHookTurnEndEventNormalizesStopAndSessionEnd(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opened.Store.Close())
	}()
	hookFacade := NewAgentHookFacade(opened.Store)

	for _, event := range []string{"Stop", "stop", "SessionEnd", "session_end", "turn-end"} {
		resp, err := hookFacade.Run(ctx, &AgentHookRequest{
			Agent:     model.AgentNameClaude,
			Event:     event,
			SessionID: "no-such-session",
		})
		require.NoError(t, err, "event %q should not error", event)
		require.NotNil(t, resp)
		require.Empty(t, resp.Message, "event %q should return empty message", event)
	}
}

func TestAgentHookRejectsUnknownEvent(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opened.Store.Close())
	}()
	hookFacade := NewAgentHookFacade(opened.Store)

	_, err = hookFacade.Run(ctx, &AgentHookRequest{
		Agent: model.AgentNameClaude,
		Event: "unknown_event",
	})
	require.Error(t, err)
}
