package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

const (
	onPremEnvelopePayloadLimit = 128 * 1024
	onPremEnvelopeMessageLimit = 1000
	onPremRequestAttempts      = 3
)

type EnvelopeChannel interface {
	Send(context.Context, *channelSendRequest) (*model.Envelope, error)
	List(context.Context, *channelListRequest) (*channelListResponse, error)
	Get(context.Context, string) (*model.Envelope, error)
	Accept(context.Context, string) (*model.Envelope, error)
	Archive(context.Context, string) (*model.Envelope, error)
	SourceNamespace() string
}

type channelSendRequest struct {
	Request *SendEnvelopeRequest
	Capsule *model.KnowledgeCapsule
	Payload json.RawMessage
}

type channelListRequest struct {
	Status    string
	Direction string
	Limit     int
	Cursor    string
}

type channelListResponse struct {
	Envelopes  []*model.Envelope
	NextCursor string
}

type managerEnvelopeChannel struct {
	facade *EnvelopeFacade
}

func (c *managerEnvelopeChannel) Send(
	ctx context.Context,
	req *channelSendRequest,
) (*model.Envelope, error) {
	response, err := c.facade.sendManagerRequest(ctx, req.Request)
	if err != nil {
		return nil, err
	}
	return response.Envelope, nil
}

func (c *managerEnvelopeChannel) List(
	ctx context.Context,
	req *channelListRequest,
) (*channelListResponse, error) {
	envelopes, err := c.facade.listRemoteEnvelopes(ctx, req.Status, req.Direction, req.Limit)
	return &channelListResponse{Envelopes: envelopes}, err
}

func (c *managerEnvelopeChannel) Get(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	return c.facade.getRemoteEnvelope(ctx, envelopeID)
}

func (c *managerEnvelopeChannel) Accept(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	return c.facade.updateRemoteEnvelope(ctx, envelopeID, "accept")
}

func (c *managerEnvelopeChannel) Archive(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	return c.facade.updateRemoteEnvelope(ctx, envelopeID, "archive")
}

func (c *managerEnvelopeChannel) SourceNamespace() string { return "manager" }

type onPremEnvelopeChannel struct {
	client  AuthHTTPClient
	profile *model.ChannelProfile
}

type onPremEnvelopeDTO struct {
	EnvelopeID     string          `json:"envelope_id"`
	FromUserID     string          `json:"from_user_id"`
	FromAgentID    string          `json:"from_agent_id"`
	ToUserID       string          `json:"to_user_id"`
	ToAgentID      string          `json:"to_agent_id"`
	PayloadType    string          `json:"payload_type"`
	PayloadJSON    json.RawMessage `json:"payload_json"`
	Message        string          `json:"message"`
	IdempotencyKey string          `json:"idempotency_key"`
	Status         string          `json:"status"`
	CreatedAt      string          `json:"created_at"`
	AcceptedAt     string          `json:"accepted_at"`
	ArchivedAt     string          `json:"archived_at"`
}

type onPremEnvelopeResponse struct {
	Envelope onPremEnvelopeDTO `json:"envelope"`
}

type onPremEnvelopeListResponse struct {
	Envelopes  []onPremEnvelopeDTO `json:"envelopes"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func (c *onPremEnvelopeChannel) Send(
	ctx context.Context,
	req *channelSendRequest,
) (*model.Envelope, error) {
	body := map[string]any{
		"to_agent_id": req.Request.ToAgentID, "payload_type": "knowledge_capsule",
		"payload_json": req.Payload, "idempotency_key": req.Request.IdempotencyKey,
	}
	if message := strings.TrimSpace(req.Request.Message); message != "" {
		body["message"] = message
	}
	var response onPremEnvelopeResponse
	if err := doOnPremJSONRetry(ctx, c.client, http.MethodPost, c.profile.URL,
		"/v1/channel/envelopes", c.profile.APIKey, body, &response,
		"send channel envelope", "channel_send"); err != nil {
		return nil, err
	}
	return onPremEnvelopeFromDTO(&response.Envelope), nil
}

func (c *onPremEnvelopeChannel) List(
	ctx context.Context,
	req *channelListRequest,
) (*channelListResponse, error) {
	params := url.Values{}
	if req.Status != "" {
		params.Set("status", req.Status)
	}
	if req.Direction != "" {
		params.Set("direction", req.Direction)
	}
	if req.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", req.Limit))
	}
	if req.Cursor != "" {
		params.Set("cursor", req.Cursor)
	}
	path := "/v1/channel/envelopes"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	permission := "channel_receive"
	if req.Direction == "sent" {
		permission = "channel_send"
	}
	var response onPremEnvelopeListResponse
	if err := doOnPremJSONRetry(ctx, c.client, http.MethodGet, c.profile.URL, path,
		c.profile.APIKey, nil, &response, "list channel envelopes", permission); err != nil {
		return nil, err
	}
	envelopes := make([]*model.Envelope, 0, len(response.Envelopes))
	for index := range response.Envelopes {
		envelopes = append(envelopes, onPremEnvelopeFromDTO(&response.Envelopes[index]))
	}
	return &channelListResponse{Envelopes: envelopes, NextCursor: response.NextCursor}, nil
}

func (c *onPremEnvelopeChannel) Get(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	return c.envelopeAction(ctx, http.MethodGet, envelopeID, "", "get channel envelope")
}

func (c *onPremEnvelopeChannel) Accept(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	return c.envelopeAction(ctx, http.MethodPost, envelopeID, "accept", "accept channel envelope")
}

func (c *onPremEnvelopeChannel) Archive(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	return c.envelopeAction(ctx, http.MethodPost, envelopeID, "archive", "archive channel envelope")
}

func (c *onPremEnvelopeChannel) envelopeAction(
	ctx context.Context,
	method string,
	envelopeID string,
	action string,
	operation string,
) (*model.Envelope, error) {
	path := "/v1/channel/envelopes/" + url.PathEscape(strings.TrimSpace(envelopeID))
	if action != "" {
		path += "/" + action
	}
	var response onPremEnvelopeResponse
	if err := doOnPremJSONRetry(ctx, c.client, method, c.profile.URL, path, c.profile.APIKey,
		nil, &response, operation, "channel_receive"); err != nil {
		return nil, err
	}
	return onPremEnvelopeFromDTO(&response.Envelope), nil
}

func (c *onPremEnvelopeChannel) SourceNamespace() string {
	return "onprem:" + c.profile.ProfileID
}

func onPremEnvelopeFromDTO(dto *onPremEnvelopeDTO) *model.Envelope {
	return &model.Envelope{
		EnvelopeID:      dto.EnvelopeID,
		SenderUserID:    dto.FromUserID,
		FromAgentID:     dto.FromAgentID,
		RecipientUserID: dto.ToUserID,
		ToAgentID:       dto.ToAgentID,
		PayloadType:     dto.PayloadType,
		PayloadJSON:     dto.PayloadJSON,
		Message:         dto.Message,
		IdempotencyKey:  dto.IdempotencyKey,
		Status:          dto.Status,
		CreatedAt:       dto.CreatedAt,
		AcceptedAt:      dto.AcceptedAt,
		ArchivedAt:      dto.ArchivedAt,
	}
}

func validateOnPremSend(req *SendEnvelopeRequest, payload []byte) error {
	if strings.TrimSpace(req.ToAgentID) == "" {
		return fmt.Errorf("send envelope: --to-agent-id is required for onprem channel")
	}
	if strings.TrimSpace(req.RecipientEmail) != "" || strings.TrimSpace(req.FromAgentID) != "" {
		return fmt.Errorf("send envelope: onprem channel does not accept --to or from_agent_id")
	}
	if len(payload) > onPremEnvelopePayloadLimit {
		return fmt.Errorf("send envelope: payload exceeds %d bytes", onPremEnvelopePayloadLimit)
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Message)) > onPremEnvelopeMessageLimit {
		return fmt.Errorf(
			"send envelope: message exceeds %d characters",
			onPremEnvelopeMessageLimit,
		)
	}
	return nil
}

func (f *EnvelopeFacade) channel(ctx context.Context, name string) (EnvelopeChannel, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "manager" {
		return &managerEnvelopeChannel{facade: f}, nil
	}
	if f.store == nil {
		return nil, fmt.Errorf("select envelope channel: store is required")
	}
	got, err := f.store.GetChannelProfile(ctx, &store.GetChannelProfileRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("select envelope channel %q: %w", name, err)
	}
	if got.Profile == nil || !got.Profile.Enabled {
		return nil, fmt.Errorf("envelope channel %q is not connected or enabled", name)
	}
	client, err := channelHTTPClient(f.client, got.Profile.CAFile)
	if err != nil {
		return nil, fmt.Errorf("select envelope channel %q: %w", name, err)
	}
	return &onPremEnvelopeChannel{client: client, profile: got.Profile}, nil
}
