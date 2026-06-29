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

const (
	knowledgeCapsuleEnvelopePayloadVersionV1 = "paxl.envelope_payload.knowledge_capsule.v1"
	knowledgeCapsuleEnvelopePayloadVersionV2 = "paxl.envelope_payload.knowledge_capsule.v2"
	envelopeRouteValueLimit                  = 256
)

type EnvelopeFacade struct {
	auth  *AuthFacade
	store *store.Store
}

type SendEnvelopeRequest struct {
	CapsuleID      string
	RecipientEmail string
	Message        string
	MatchType      string
	MatchValue     string
	TargetAgent    model.AgentName
	CallerAgent    model.AgentName
	FromAgentID    string
	ToAgentID      string
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
	Envelope  *model.Envelope
	Capsule   *model.KnowledgeCapsule
	Injection *model.KnowledgeInjection
}

type AcceptAllEnvelopesRequest struct {
	Status          string
	Limit           int
	ContinueOnError bool
}

type AcceptEnvelopeFailure struct {
	EnvelopeID string
	Error      string
}

type AcceptAllEnvelopesResponse struct {
	Accepted []*AcceptEnvelopeResponse
	Failures []*AcceptEnvelopeFailure
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
	Route         *envelopePayloadRoute  `json:"route,omitempty"`
}

type envelopePayloadRoute struct {
	MatchType   string          `json:"match_type"`
	MatchValue  string          `json:"match_value,omitempty"`
	TargetAgent model.AgentName `json:"target_agent,omitempty"`
}

type envelopeRouteResult struct {
	Route *envelopePayloadRoute
}

type envelopeRecipientFields struct {
	RecipientEmail string
	FromAgentID    string
	ToAgentID      string
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
	capsule, err := f.loadLocalCapsule(ctx, req.CapsuleID)
	if err != nil {
		return nil, err
	}
	credential, err := f.auth.requireCredential(ctx)
	if err != nil {
		return nil, err
	}
	recipientFields, err := f.resolveEnvelopeRecipientFields(ctx, credential, req)
	if err != nil {
		return nil, err
	}
	routeResult, err := envelopeRouteFromSendRequest(req)
	if err != nil {
		return nil, err
	}
	payload, err := encodeEnvelopePayload(capsule, routeResult.Route)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"payload_type": "knowledge_capsule",
		"payload_json": json.RawMessage(payload),
		"message":      strings.TrimSpace(req.Message),
	}
	if recipientFields.RecipientEmail != "" {
		body["recipient_email"] = recipientFields.RecipientEmail
	}
	if recipientFields.FromAgentID != "" {
		body["from_agent_id"] = recipientFields.FromAgentID
		body["to_agent_id"] = recipientFields.ToAgentID
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

func (f *EnvelopeFacade) resolveEnvelopeRecipientFields(
	ctx context.Context,
	credential *model.AuthCredential,
	req *SendEnvelopeRequest,
) (*envelopeRecipientFields, error) {
	recipientEmail := strings.TrimSpace(req.RecipientEmail)
	fromAgentID := strings.TrimSpace(req.FromAgentID)
	toAgentID := strings.TrimSpace(req.ToAgentID)
	callerProvided := req.CallerAgent != model.AgentNameUnknown && req.CallerAgent != ""
	if callerProvided && fromAgentID == "" && toAgentID == "" {
		return nil, fmt.Errorf("send envelope: caller agent delivery requires to_agent_id")
	}
	if toAgentID != "" && fromAgentID == "" && callerProvided {
		var err error
		fromAgentID, err = f.resolveCallerAgentID(ctx, credential, req.CallerAgent)
		if err != nil {
			return nil, err
		}
	}
	if (fromAgentID == "") != (toAgentID == "") {
		return nil, fmt.Errorf(
			"send envelope: from_agent_id and to_agent_id must be provided together",
		)
	}
	if recipientEmail == "" && fromAgentID == "" {
		return nil, fmt.Errorf("send envelope: recipient email or to_agent_id is required")
	}
	return &envelopeRecipientFields{
		RecipientEmail: recipientEmail,
		FromAgentID:    fromAgentID,
		ToAgentID:      toAgentID,
	}, nil
}

func (f *EnvelopeFacade) resolveCallerAgentID(
	ctx context.Context,
	credential *model.AuthCredential,
	callerAgent model.AgentName,
) (string, error) {
	if credential == nil || strings.TrimSpace(credential.NodeID) == "" {
		return "", fmt.Errorf("send envelope: current node id is required to resolve caller agent")
	}
	var envelope managerEnvelope[struct {
		Agents []*model.NodeAgent `json:"agents"`
	}]
	nodeID := strings.TrimSpace(credential.NodeID)
	if err := f.auth.managerJSON(
		ctx,
		http.MethodGet,
		credential.ManagerURL,
		userNodePath(credential.UserID)+"/"+url.PathEscape(nodeID)+"/agents",
		credential.APIKey,
		nil,
		&envelope,
	); err != nil {
		return "", fmt.Errorf("list current node agents: %w", err)
	}
	var matches []string
	for _, agent := range envelope.Data.Agents {
		if callerMatchesNodeAgent(callerAgent, agent) {
			matches = append(matches, strings.TrimSpace(agent.AgentID))
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf(
			"send envelope: caller agent %q is not registered on node %s",
			callerAgent,
			nodeID,
		)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf(
			"send envelope: caller agent %q is ambiguous on node %s",
			callerAgent,
			nodeID,
		)
	}
}

func callerMatchesNodeAgent(callerAgent model.AgentName, agent *model.NodeAgent) bool {
	if agent == nil || strings.TrimSpace(agent.AgentID) == "" {
		return false
	}
	caller := string(callerAgent)
	return strings.EqualFold(strings.TrimSpace(agent.AgentType), caller) ||
		strings.EqualFold(strings.TrimSpace(agent.Name), caller)
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
	capsule, route, err := capsuleFromEnvelope(envelope)
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
	var injection *model.KnowledgeInjection
	if route != nil {
		injection, err = f.storeAcceptedEnvelopeRoute(ctx, created.Capsule, route)
		if err != nil {
			return nil, err
		}
	}
	return &AcceptEnvelopeResponse{
		Envelope:  accepted,
		Capsule:   created.Capsule,
		Injection: injection,
	}, nil
}

func (f *EnvelopeFacade) AcceptAll(
	ctx context.Context,
	req *AcceptAllEnvelopesRequest,
	opts ...func(*Option),
) (*AcceptAllEnvelopesResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		req = &AcceptAllEnvelopesRequest{}
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "pending"
	}
	listed, err := f.ListInbox(ctx, &ListInboxRequest{
		Status: status,
		Limit:  req.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list envelopes to accept: %w", err)
	}
	resp := &AcceptAllEnvelopesResponse{}
	for _, envelope := range listed.Envelopes {
		if envelope == nil || strings.TrimSpace(envelope.EnvelopeID) == "" {
			continue
		}
		accepted, err := f.Accept(ctx, &AcceptEnvelopeRequest{
			EnvelopeID: envelope.EnvelopeID,
		})
		if err != nil {
			failure := &AcceptEnvelopeFailure{
				EnvelopeID: envelope.EnvelopeID,
				Error:      err.Error(),
			}
			resp.Failures = append(resp.Failures, failure)
			if !req.ContinueOnError {
				return resp, err
			}
			continue
		}
		resp.Accepted = append(resp.Accepted, accepted)
	}
	return resp, nil
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

func encodeEnvelopePayload(
	capsule *model.KnowledgeCapsule,
	route *envelopePayloadRoute,
) ([]byte, error) {
	schemaVersion := knowledgeCapsuleEnvelopePayloadVersionV1
	if route != nil {
		schemaVersion = knowledgeCapsuleEnvelopePayloadVersionV2
	}
	return json.Marshal(envelopePayload{
		SchemaVersion: schemaVersion,
		Capsule:       envelopePayloadCapsuleFromModel(capsule),
		Route:         route,
	})
}

func envelopeRouteFromSendRequest(req *SendEnvelopeRequest) (envelopeRouteResult, error) {
	matchType := strings.TrimSpace(req.MatchType)
	matchValue := strings.TrimSpace(req.MatchValue)
	if matchType == "" {
		if matchValue != "" || req.TargetAgent != "" {
			return envelopeRouteResult{}, fmt.Errorf(
				"send envelope: --agent, --project, or --keyword requires --match",
			)
		}
		return envelopeRouteResult{}, nil
	}
	route := &envelopePayloadRoute{
		MatchType:   matchType,
		MatchValue:  matchValue,
		TargetAgent: req.TargetAgent,
	}
	if err := validateEnvelopeRoute(route); err != nil {
		return envelopeRouteResult{}, fmt.Errorf("send envelope: %w", err)
	}
	return envelopeRouteResult{Route: route}, nil
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

func capsuleFromEnvelope(
	envelope *model.Envelope,
) (*model.KnowledgeCapsule, *envelopePayloadRoute, error) {
	if envelope.PayloadType != "knowledge_capsule" {
		return nil, nil, fmt.Errorf(
			"accept envelope: unsupported payload type %q",
			envelope.PayloadType,
		)
	}
	var payload envelopePayload
	if err := json.Unmarshal(envelope.PayloadJSON, &payload); err != nil {
		return nil, nil, fmt.Errorf("decode envelope payload: %w", err)
	}
	switch payload.SchemaVersion {
	case knowledgeCapsuleEnvelopePayloadVersionV1:
		if payload.Route != nil {
			return nil, nil, fmt.Errorf("accept envelope: route requires payload schema v2")
		}
	case knowledgeCapsuleEnvelopePayloadVersionV2:
		if err := validateEnvelopeRoute(payload.Route); err != nil {
			return nil, nil, fmt.Errorf("accept envelope: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf(
			"accept envelope: unsupported payload schema %q",
			payload.SchemaVersion,
		)
	}
	capsuleID, err := newLocalID("kcap")
	if err != nil {
		return nil, nil, fmt.Errorf("create accepted capsule id: %w", err)
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
	}, payload.Route, nil
}

func validateEnvelopeRoute(route *envelopePayloadRoute) error {
	if route == nil {
		return fmt.Errorf("route is required")
	}
	matchType := strings.TrimSpace(route.MatchType)
	matchValue := strings.TrimSpace(route.MatchValue)
	switch matchType {
	case "any":
		if matchValue != "" {
			return fmt.Errorf("route match value must be empty for any")
		}
	case "project", "keyword":
		if matchValue == "" {
			return fmt.Errorf("route match value is required")
		}
	default:
		return fmt.Errorf("unsupported route match type %q", route.MatchType)
	}
	if len(matchValue) > envelopeRouteValueLimit {
		return fmt.Errorf("route match value is too long")
	}
	if route.TargetAgent == model.AgentNameUnknown {
		return fmt.Errorf("unsupported route target agent")
	}
	route.MatchType = matchType
	route.MatchValue = matchValue
	return nil
}

func (f *EnvelopeFacade) storeAcceptedEnvelopeRoute(
	ctx context.Context,
	capsule *model.KnowledgeCapsule,
	route *envelopePayloadRoute,
) (*model.KnowledgeInjection, error) {
	injectionID, err := newLocalID("kci")
	if err != nil {
		return nil, fmt.Errorf("create accepted route injection id: %w", err)
	}
	injection := &model.KnowledgeInjection{
		InjectionID:         injectionID,
		CapsuleID:           capsule.CapsuleID,
		SourceNodeID:        capsule.SourceNodeID,
		SourceAgent:         capsule.SourceAgent,
		SourceSessionID:     capsule.SourceSessionID,
		TargetNodeID:        localNodeID(),
		TargetAgent:         route.TargetAgent,
		DeliveryMethod:      "hook",
		DeliveryMessageType: "system_handoff",
		Status:              "pending",
		RouteMatchType:      route.MatchType,
		RouteMatchValue:     route.MatchValue,
	}
	created, err := f.store.CreateKnowledgeInjection(
		ctx,
		&store.CreateKnowledgeInjectionRequest{Injection: injection},
	)
	if err != nil {
		return nil, fmt.Errorf("store accepted route injection: %w", err)
	}
	return created.Injection, nil
}
