package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

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
			require.Equal(t, "pending", req.URL.Query().Get("status"))
			return jsonResponse(`{
				"data":{"envelopes":[{"envelope_id":"env_1","payload_type":"knowledge_capsule","status":"pending"}]},
				"code":200,
				"message":"ok"
			}`), nil
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
