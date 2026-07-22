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
	"github.com/stretchr/testify/require"
)

func TestOnPremEnvelopeSendUsesCredentialIdentityAndStableRetryKey(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	seedChannelProfile(ctx, t, opened.Store, "onprem", "chp_one", "tm_key_secret")
	seedEnvelopeCapsule(ctx, t, opened.Store, "kcap_send")
	requests := 0
	var firstKey string
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/channel/envelopes", req.URL.Path)
		require.Equal(t, "Bearer tm_key_secret", req.Header.Get("Authorization"))
		body := decodeJSONBody(t, req)
		require.Equal(t, "receiver-agent", body["to_agent_id"])
		require.NotContains(t, body, "from_user_id")
		require.NotContains(t, body, "from_agent_id")
		key, ok := body["idempotency_key"].(string)
		require.True(t, ok)
		if requests == 0 {
			firstKey = key
		} else {
			require.Equal(t, firstKey, key)
		}
		payload, ok := body["payload_json"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, knowledgeCapsuleEnvelopePayloadVersionV2, payload["schema_version"])
		requests++
		if requests == 1 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       http.NoBody,
				Header:     make(http.Header),
			}, nil
		}
		encodedPayload, err := json.Marshal(payload)
		require.NoError(t, err)
		return jsonResponse(
			fmt.Sprintf(
				`{"envelope":{"envelope_id":"tm_env_1","from_user_id":"user","from_agent_id":"sender","to_user_id":"receiver-user","to_agent_id":"receiver-agent","payload_type":"knowledge_capsule","payload_json":%s,"idempotency_key":%q,"status":"pending","created_at":"2026-07-22T00:00:00Z"}}`,
				encodedPayload,
				key,
			),
		), nil
	})

	resp, err := NewEnvelopeFacade(client, opened.Store).Send(ctx, &SendEnvelopeRequest{
		Channel: "onprem", CapsuleID: "kcap_send", ToAgentID: "receiver-agent",
		MatchType: "project", MatchValue: "paxl", TargetAgent: model.AgentNameCodex,
		Message: "review",
	})

	require.NoError(t, err)
	require.Equal(t, 2, requests)
	require.Equal(t, "tm_env_1", resp.Envelope.EnvelopeID)
	require.NotEmpty(t, firstKey)
}

func TestOnPremEnvelopeSendDoesNotRetryIdempotencyConflict(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	seedChannelProfile(ctx, t, opened.Store, "onprem", "chp_one", "tm_key_secret")
	seedEnvelopeCapsule(ctx, t, opened.Store, "kcap_send")
	requests := 0
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		_, _ = io.Copy(io.Discard, req.Body)
		return &http.Response{
			StatusCode: http.StatusConflict,
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	})

	_, err = NewEnvelopeFacade(client, opened.Store).Send(ctx, &SendEnvelopeRequest{
		Channel: "onprem", CapsuleID: "kcap_send", ToAgentID: "receiver-agent",
		MatchType: "any", TargetAgent: model.AgentNameCodex,
	})

	require.ErrorContains(t, err, "idempotency key conflicts")
	require.Equal(t, 1, requests)
}

func TestOnPremEnvelopeArchiveUsesReceiverActionEndpoint(t *testing.T) {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/v1/channel/envelopes/env-1/archive", req.URL.Path)
		require.Equal(t, "Bearer tm_key", req.Header.Get("Authorization"))
		return jsonResponse(
			`{"envelope":{"envelope_id":"env-1","payload_type":"knowledge_capsule","payload_json":{},"status":"archived"}}`,
		), nil
	})
	channel := &onPremEnvelopeChannel{
		client: client,
		profile: &model.ChannelProfile{
			ProfileID: "chp_one", URL: "https://memory.internal", APIKey: "tm_key",
		},
	}

	archived, err := channel.Archive(context.Background(), "env-1")

	require.NoError(t, err)
	require.Equal(t, "archived", archived.Status)
}

func TestOnPremOutboxGetUsesSentListBecauseDirectGetIsReceiverOnly(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	seedChannelProfile(ctx, t, opened.Store, "onprem", "chp_one", "tm_key")
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/v1/channel/envelopes", req.URL.Path)
		require.Equal(t, "sent", req.URL.Query().Get("direction"))
		require.Equal(t, "100", req.URL.Query().Get("limit"))
		return jsonResponse(
			`{"envelopes":[{"envelope_id":"env-1","payload_type":"knowledge_capsule","payload_json":{},"status":"pending"}]}`,
		), nil
	})

	got, err := NewEnvelopeFacade(client, opened.Store).Get(ctx, &GetEnvelopeRequest{
		Channel: "onprem", Direction: "sent", EnvelopeID: "env-1",
	})

	require.NoError(t, err)
	require.Equal(t, "env-1", got.Envelope.EnvelopeID)
}

func seedEnvelopeCapsule(
	ctx context.Context,
	t *testing.T,
	sessionStore *store.Store,
	capsuleID string,
) {
	t.Helper()
	_, err := sessionStore.CreateKnowledgeCapsule(
		ctx,
		&store.CreateKnowledgeCapsuleRequest{Capsule: &model.KnowledgeCapsule{
			CapsuleID:              capsuleID,
			SourceSessionID:        "codex:source",
			SourceAgent:            model.AgentNameCodex,
			Keyword:                "onprem",
			Title:                  "On-prem handoff",
			Summary:                "summary",
			Content:                strings.Repeat("x", 64),
			Status:                 "active",
			OriginalEstimatedChars: 64,
			CreatedAt:              "2026-07-22T00:00:00Z",
		}},
	)
	require.NoError(t, err)
}

func TestOnPremAcceptMaterializesBeforeRemoteAcceptAndRecoversLostResponse(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	seedChannelProfile(ctx, t, opened.Store, "onprem", "chp_one", "tm_key_secret")
	payload := `{"schema_version":"paxl.envelope_payload.knowledge_capsule.v2","capsule":{"capsule_id":"remote","source_session_id":"codex:source","source_agent":"codex","keyword":"paxl","title":"Remote","summary":"summary","content":"content","status":"active","truncated":false,"original_estimated_chars":7},"route":{"match_type":"project","match_value":"paxl","target_agent":"codex"}}`
	accepted := false
	acceptAttempts := 0
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodGet:
			status := "pending"
			if accepted {
				status = "accepted"
			}
			return jsonResponse(
				fmt.Sprintf(
					`{"envelope":{"envelope_id":"same-id","from_user_id":"sender-user","from_agent_id":"sender-agent","to_user_id":"user","to_agent_id":"sender","payload_type":"knowledge_capsule","payload_json":%s,"status":%q,"created_at":"2026-07-22T00:00:00Z"}}`,
					payload,
					status,
				),
			), nil
		case http.MethodPost:
			acceptAttempts++
			listed, listErr := opened.Store.ListKnowledgeCapsules(
				ctx,
				&store.ListKnowledgeCapsulesRequest{},
			)
			require.NoError(t, listErr)
			require.Len(t, listed.Capsules, 1, "local capsule must commit before remote accept")
			accepted = true
			return nil, fmt.Errorf("connection lost after server commit")
		default:
			return nil, fmt.Errorf("unexpected request")
		}
	})
	envelopeFacade := NewEnvelopeFacade(client, opened.Store)

	_, err = envelopeFacade.Accept(
		ctx,
		&AcceptEnvelopeRequest{Channel: "onprem", EnvelopeID: "same-id"},
	)
	require.ErrorContains(t, err, "connection lost")
	result, err := envelopeFacade.Accept(
		ctx,
		&AcceptEnvelopeRequest{Channel: "onprem", EnvelopeID: "same-id"},
	)
	require.NoError(t, err)
	require.Equal(t, "accepted", result.Envelope.Status)
	require.Equal(t, 3, acceptAttempts)
	listed, err := opened.Store.ListKnowledgeCapsules(ctx, &store.ListKnowledgeCapsulesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Capsules, 1)
	require.Equal(t, "remote_envelope:onprem:chp_one:same-id", listed.Capsules[0].SourceSessionID)
	injections, err := opened.Store.ListKnowledgeInjections(
		ctx,
		&store.ListKnowledgeInjectionsRequest{Limit: 10},
	)
	require.NoError(t, err)
	require.Len(t, injections.Injections, 1)
}

func TestOnPremProfilesNamespaceSameEnvelopeIDWithoutCollision(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	for _, profile := range []*model.ChannelProfile{
		{ProfileID: "chp_one", Name: "onprem", Kind: model.ChannelKindOnPrem, URL: "https://one.internal", APIKey: "tm_one", Enabled: true},
		{ProfileID: "chp_two", Name: "review", Kind: model.ChannelKindOnPrem, URL: "https://two.internal", APIKey: "tm_two", Enabled: true},
	} {
		_, err := opened.Store.SaveChannelProfile(
			ctx,
			&store.SaveChannelProfileRequest{Profile: profile},
		)
		require.NoError(t, err)
	}
	payload := `{"schema_version":"paxl.envelope_payload.knowledge_capsule.v2","capsule":{"capsule_id":"remote","source_session_id":"codex:source","source_agent":"codex","keyword":"paxl","title":"Remote","summary":"summary","content":"content","status":"active","truncated":false,"original_estimated_chars":7}}`
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := "pending"
		if req.Method == http.MethodPost {
			status = "accepted"
		}
		return jsonResponse(
			fmt.Sprintf(
				`{"envelope":{"envelope_id":"same-id","from_user_id":"sender-user","from_agent_id":"sender-agent","to_user_id":"user","to_agent_id":"receiver","payload_type":"knowledge_capsule","payload_json":%s,"status":%q,"created_at":"2026-07-22T00:00:00Z"}}`,
				payload,
				status,
			),
		), nil
	})
	envelopeFacade := NewEnvelopeFacade(client, opened.Store)

	for _, channel := range []string{"onprem", "review"} {
		_, err := envelopeFacade.Accept(
			ctx,
			&AcceptEnvelopeRequest{Channel: channel, EnvelopeID: "same-id"},
		)
		require.NoError(t, err)
	}
	listed, err := opened.Store.ListKnowledgeCapsules(ctx, &store.ListKnowledgeCapsulesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Capsules, 2)
	require.NotEqual(t, listed.Capsules[0].SourceSessionID, listed.Capsules[1].SourceSessionID)
}

func TestOnPremSendValidatesPayloadMessageAndRouteBeforeHTTP(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentSize int
		message     string
		match       string
		agent       model.AgentName
		want        string
	}{
		{name: "payload", contentSize: 129 * 1024, match: "any", want: "payload exceeds"},
		{name: "message", contentSize: 16, message: strings.Repeat("m", 1001), match: "any", want: "message exceeds"},
		{name: "route", contentSize: 16, match: "broadcast", want: "unsupported route match type"},
		{name: "target agent", contentSize: 16, match: "any", agent: "invalid", want: "target agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			opened, err := store.Open(
				ctx,
				&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
			)
			require.NoError(t, err)
			defer func() { require.NoError(t, opened.Store.Close()) }()
			seedChannelProfile(ctx, t, opened.Store, "onprem", "chp_one", "tm_key")
			_, err = opened.Store.CreateKnowledgeCapsule(
				ctx,
				&store.CreateKnowledgeCapsuleRequest{Capsule: &model.KnowledgeCapsule{
					CapsuleID:              "kcap",
					SourceSessionID:        "codex:source",
					SourceAgent:            model.AgentNameCodex,
					Keyword:                "k",
					Title:                  "title",
					Summary:                "summary",
					Content:                strings.Repeat("x", test.contentSize),
					Status:                 "active",
					OriginalEstimatedChars: int64(test.contentSize),
					CreatedAt:              "2026-07-22T00:00:00Z",
				}},
			)
			require.NoError(t, err)
			requests := 0
			client := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				requests++
				return nil, fmt.Errorf("must not send")
			})

			targetAgent := test.agent
			if targetAgent == "" {
				targetAgent = model.AgentNameCodex
			}
			_, err = NewEnvelopeFacade(client, opened.Store).Send(ctx, &SendEnvelopeRequest{
				Channel: "onprem", CapsuleID: "kcap", ToAgentID: "receiver", MatchType: test.match,
				TargetAgent: targetAgent, Message: test.message,
			})

			require.ErrorContains(t, err, test.want)
			require.Zero(t, requests)
		})
	}
}
