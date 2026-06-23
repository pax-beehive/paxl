package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

const knowledgeCapsuleEnvelopePayloadVersion = "paxl.envelope_payload.knowledge_capsule.v1"

type EnvelopeFacade struct {
	auth  *AuthFacade
	store *store.Store
}

type SendEnvelopeRequest struct {
	CapsuleID      string
	RecipientEmail string
	Message        string
}

type SendEnvelopeResponse struct {
	Envelope *model.Envelope
}

type ListInboxRequest struct {
	Status string
	Limit  int
}

type ListInboxResponse struct {
	Envelopes []*model.Envelope
}

type ListOutboxRequest struct {
	Status string
	Limit  int
}

type ListOutboxResponse struct {
	Envelopes []*model.Envelope
}

type GetEnvelopeRequest struct {
	EnvelopeID string
}

type GetEnvelopeResponse struct {
	Envelope *model.Envelope
}

type AcceptEnvelopeRequest struct {
	EnvelopeID string
}

type AcceptEnvelopeResponse struct {
	Envelope *model.Envelope
	Capsule  *model.KnowledgeCapsule
}

type ArchiveEnvelopeRequest struct {
	EnvelopeID string
}

type ArchiveEnvelopeResponse struct {
	Envelope *model.Envelope
}

type envelopePayload struct {
	SchemaVersion string                 `json:"schema_version"`
	Capsule       envelopePayloadCapsule `json:"capsule"`
}

type envelopePayloadCapsule struct {
	CapsuleID              string          `json:"capsule_id"`
	SourceNodeID           string          `json:"source_node_id,omitempty"`
	SourceSessionID        string          `json:"source_session_id"`
	SourceAgent            model.AgentName `json:"source_agent"`
	Keyword                string          `json:"keyword"`
	Title                  string          `json:"title"`
	Summary                string          `json:"summary"`
	Content                string          `json:"content"`
	Status                 string          `json:"status"`
	Truncated              bool            `json:"truncated"`
	OriginalEstimatedChars int64           `json:"original_estimated_chars"`
	CreatedAt              string          `json:"created_at"`
	ArchivedAt             string          `json:"archived_at,omitempty"`
}

func NewEnvelopeFacade(client AuthHTTPClient, sessionStore *store.Store) *EnvelopeFacade {
	return &EnvelopeFacade{
		auth:  NewAuthFacade(client, sessionStore),
		store: sessionStore,
	}
}

func (f *EnvelopeFacade) Send(
	ctx context.Context,
	req *SendEnvelopeRequest,
	opts ...func(*Option),
) (*SendEnvelopeResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("send envelope: request is required")
	}
	if strings.TrimSpace(req.RecipientEmail) == "" {
		return nil, fmt.Errorf("send envelope: recipient email is required")
	}
	capsule, err := f.loadLocalCapsule(ctx, req.CapsuleID)
	if err != nil {
		return nil, err
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := encodeEnvelopePayload(capsule)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"recipient_email": req.RecipientEmail,
		"payload_type":    "knowledge_capsule",
		"payload_json":    json.RawMessage(payload),
		"message":         strings.TrimSpace(req.Message),
	}
	var envelope managerEnvelope[struct {
		Envelope model.Envelope `json:"envelope"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodPost,
		credential.ManagerURL,
		userEnvelopePath(credential.UserID, ""),
		credential.APIKey,
		body,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &SendEnvelopeResponse{Envelope: &envelope.Data.Envelope}, nil
}

func (f *EnvelopeFacade) ListInbox(
	ctx context.Context,
	req *ListInboxRequest,
	opts ...func(*Option),
) (*ListInboxResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListInboxRequest{Status: "pending"}
	}
	envelopes, err := f.listRemoteEnvelopes(ctx, req.Status, "", req.Limit)
	if err != nil {
		return nil, err
	}
	return &ListInboxResponse{Envelopes: envelopes}, nil
}

func (f *EnvelopeFacade) ListOutbox(
	ctx context.Context,
	req *ListOutboxRequest,
	opts ...func(*Option),
) (*ListOutboxResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &ListOutboxRequest{}
	}
	envelopes, err := f.listRemoteEnvelopes(ctx, req.Status, "sent", req.Limit)
	if err != nil {
		return nil, err
	}
	return &ListOutboxResponse{Envelopes: envelopes}, nil
}

func (f *EnvelopeFacade) listRemoteEnvelopes(
	ctx context.Context,
	status string,
	direction string,
	limit int,
) ([]*model.Envelope, error) {
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	path := userEnvelopePath(credential.UserID, "")
	params := url.Values{}
	if status != "" {
		params.Set("status", status)
	}
	if direction != "" {
		params.Set("direction", direction)
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var envelope managerEnvelope[struct {
		Envelopes []*model.Envelope `json:"envelopes"`
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
	return envelope.Data.Envelopes, nil
}

func (f *EnvelopeFacade) Get(
	ctx context.Context,
	req *GetEnvelopeRequest,
	opts ...func(*Option),
) (*GetEnvelopeResponse, error) {
	_ = applyOptions(opts)
	envelope, err := f.getRemoteEnvelope(ctx, req.EnvelopeID)
	if err != nil {
		return nil, err
	}
	return &GetEnvelopeResponse{Envelope: envelope}, nil
}

func (f *EnvelopeFacade) Accept(
	ctx context.Context,
	req *AcceptEnvelopeRequest,
	opts ...func(*Option),
) (*AcceptEnvelopeResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.EnvelopeID) == "" {
		return nil, fmt.Errorf("accept envelope: envelope id is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("accept envelope: store is required")
	}
	envelope, err := f.getRemoteEnvelope(ctx, req.EnvelopeID)
	if err != nil {
		return nil, err
	}
	capsule, err := capsuleFromEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	created, err := f.store.CreateKnowledgeCapsule(
		ctx,
		&store.CreateKnowledgeCapsuleRequest{Capsule: capsule},
	)
	if err != nil {
		return nil, fmt.Errorf("store accepted capsule: %w", err)
	}
	accepted, err := f.updateRemoteEnvelope(ctx, req.EnvelopeID, "accept")
	if err != nil {
		return nil, err
	}
	return &AcceptEnvelopeResponse{Envelope: accepted, Capsule: created.Capsule}, nil
}

func (f *EnvelopeFacade) Archive(
	ctx context.Context,
	req *ArchiveEnvelopeRequest,
	opts ...func(*Option),
) (*ArchiveEnvelopeResponse, error) {
	_ = applyOptions(opts)
	if req == nil || strings.TrimSpace(req.EnvelopeID) == "" {
		return nil, fmt.Errorf("archive envelope: envelope id is required")
	}
	archived, err := f.updateRemoteEnvelope(ctx, req.EnvelopeID, "archive")
	if err != nil {
		return nil, err
	}
	return &ArchiveEnvelopeResponse{Envelope: archived}, nil
}

func (f *EnvelopeFacade) loadLocalCapsule(
	ctx context.Context,
	capsuleID string,
) (*model.KnowledgeCapsule, error) {
	if f.store == nil {
		return nil, fmt.Errorf("send envelope: store is required")
	}
	if strings.TrimSpace(capsuleID) == "" {
		return nil, fmt.Errorf("send envelope: capsule id is required")
	}
	got, err := f.store.GetKnowledgeCapsule(
		ctx,
		&store.GetKnowledgeCapsuleRequest{CapsuleID: capsuleID},
	)
	if err != nil {
		return nil, fmt.Errorf("load capsule %q: %w", capsuleID, err)
	}
	return got.Capsule, nil
}

func (f *EnvelopeFacade) getRemoteEnvelope(
	ctx context.Context,
	envelopeID string,
) (*model.Envelope, error) {
	if strings.TrimSpace(envelopeID) == "" {
		return nil, fmt.Errorf("get envelope: envelope id is required")
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Envelope model.Envelope `json:"envelope"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userEnvelopePath(credential.UserID, envelopeID),
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &envelope.Data.Envelope, nil
}

func (f *EnvelopeFacade) updateRemoteEnvelope(
	ctx context.Context,
	envelopeID string,
	action string,
) (*model.Envelope, error) {
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	var envelope managerEnvelope[struct {
		Envelope model.Envelope `json:"envelope"`
	}]
	if err := f.auth.managerJSON(
		ctx,
		http.MethodPost,
		credential.ManagerURL,
		userEnvelopePath(credential.UserID, envelopeID)+"/"+action,
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return nil, err
	}
	return &envelope.Data.Envelope, nil
}

func userEnvelopePath(userID string, envelopeID string) string {
	path := "/api/v1/user/" + url.PathEscape(userID) + "/envelopes"
	if envelopeID != "" {
		path += "/" + url.PathEscape(envelopeID)
	}
	return path
}

func encodeEnvelopePayload(capsule *model.KnowledgeCapsule) ([]byte, error) {
	return json.Marshal(envelopePayload{
		SchemaVersion: knowledgeCapsuleEnvelopePayloadVersion,
		Capsule:       envelopePayloadCapsuleFromModel(capsule),
	})
}

func envelopePayloadCapsuleFromModel(capsule *model.KnowledgeCapsule) envelopePayloadCapsule {
	return envelopePayloadCapsule{
		CapsuleID:              capsule.CapsuleID,
		SourceNodeID:           capsule.SourceNodeID,
		SourceSessionID:        capsule.SourceSessionID,
		SourceAgent:            capsule.SourceAgent,
		Keyword:                capsule.Keyword,
		Title:                  capsule.Title,
		Summary:                capsule.Summary,
		Content:                capsule.Content,
		Status:                 capsule.Status,
		Truncated:              capsule.Truncated,
		OriginalEstimatedChars: capsule.OriginalEstimatedChars,
		CreatedAt:              capsule.CreatedAt,
		ArchivedAt:             capsule.ArchivedAt,
	}
}

func capsuleFromEnvelope(envelope *model.Envelope) (*model.KnowledgeCapsule, error) {
	if envelope.PayloadType != "knowledge_capsule" {
		return nil, fmt.Errorf("accept envelope: unsupported payload type %q", envelope.PayloadType)
	}
	var payload envelopePayload
	if err := json.Unmarshal(envelope.PayloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("decode envelope payload: %w", err)
	}
	if payload.SchemaVersion != knowledgeCapsuleEnvelopePayloadVersion {
		return nil, fmt.Errorf(
			"accept envelope: unsupported payload schema %q",
			payload.SchemaVersion,
		)
	}
	capsuleID, err := newLocalID("kcap")
	if err != nil {
		return nil, fmt.Errorf("create accepted capsule id: %w", err)
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)
	capsule := payload.Capsule
	return &model.KnowledgeCapsule{
		CapsuleID:              capsuleID,
		SourceNodeID:           capsule.SourceNodeID,
		SourceSessionID:        "remote_envelope:" + envelope.EnvelopeID,
		SourceAgent:            capsule.SourceAgent,
		Keyword:                capsule.Keyword,
		Title:                  capsule.Title,
		Summary:                capsule.Summary,
		Content:                capsule.Content,
		Status:                 "active",
		Truncated:              capsule.Truncated,
		OriginalEstimatedChars: capsule.OriginalEstimatedChars,
		CreatedAt:              createdAt,
	}, nil
}
