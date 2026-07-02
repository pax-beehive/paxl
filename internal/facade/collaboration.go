package facade

import (
	"context"
	"fmt"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

type LocalCollaborationFacade struct {
	capsules *CapsuleFacade
}

type LocalCommunicationKind string

const (
	LocalCommunicationKindUnknown        LocalCommunicationKind = "unknown"
	LocalCommunicationKindSessionHandoff LocalCommunicationKind = "session_handoff"
	LocalCommunicationKindMemoryHandoff  LocalCommunicationKind = "memory_handoff"
)

type MoveSessionContextRequest struct {
	SourceSessionID string
	SourceAgent     model.AgentName
	TargetSessionID string
	TargetAgent     model.AgentName
}

type MoveSessionContextResponse struct {
	Capsule       *model.KnowledgeCapsule
	Injection     *model.KnowledgeInjection
	Communication *LocalCommunication
	Message       string
}

type ShareSessionMemoryRequest struct {
	SourceSessionID string
	SourceAgent     model.AgentName
	Keyword         string
	Title           string
	Summary         string
	Content         string
	Local           bool
	TargetSessionID string
	TargetAgent     model.AgentName
	NewSession      bool
	MatchType       string
	MatchValue      string
	Route           *LocalMemoryRoute
	ActionItems     []string
}

type ShareSessionMemoryResponse struct {
	Capsule       *model.KnowledgeCapsule
	Injection     *model.KnowledgeInjection
	Communication *LocalCommunication
	Message       string
}

type QueueMemoryDeliveryRequest struct {
	CapsuleID   string
	Route       *LocalMemoryRoute
	ActionItems []string
}

type QueueMemoryDeliveryResponse struct {
	Injection     *model.KnowledgeInjection
	Communication *LocalCommunication
	Message       string
}

type LocalMemoryRouteKind string

const (
	LocalMemoryRouteKindUnknown    LocalMemoryRouteKind = "unknown"
	LocalMemoryRouteKindAny        LocalMemoryRouteKind = "any"
	LocalMemoryRouteKindSession    LocalMemoryRouteKind = "session"
	LocalMemoryRouteKindKeyword    LocalMemoryRouteKind = "keyword"
	LocalMemoryRouteKindProject    LocalMemoryRouteKind = "project"
	LocalMemoryRouteKindNewSession LocalMemoryRouteKind = "new_session"
)

type LocalMemoryRoute struct {
	Kind      LocalMemoryRouteKind
	Agent     model.AgentName
	SessionID string
	Keyword   string
	Project   string
}

type LocalCommunication struct {
	Kind            LocalCommunicationKind
	SourceAgent     model.AgentName
	SourceSessionID string
	TargetAgent     model.AgentName
	TargetSessionID string
	DeliveryMethod  string
}

func NewLocalCollaborationFacade(
	registry *adaptor.Registry,
	sessionStore *store.Store,
) *LocalCollaborationFacade {
	return &LocalCollaborationFacade{
		capsules: NewCapsuleFacade(registry, sessionStore),
	}
}

func (f *LocalCollaborationFacade) MoveSessionContext(
	ctx context.Context,
	req *MoveSessionContextRequest,
	opts ...func(*Option),
) (*MoveSessionContextResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("move session context: request is required")
	}
	if f.capsules == nil {
		return nil, fmt.Errorf("move session context: capsule facade is required")
	}
	resp, err := f.capsules.MirrorSession(ctx, &MirrorSessionRequest{
		SourceSessionID: req.SourceSessionID,
		Agent:           req.SourceAgent,
		TargetSessionID: req.TargetSessionID,
		TargetAgent:     req.TargetAgent,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("mirror session context: %w", err)
	}
	return &MoveSessionContextResponse{
		Capsule:       resp.Capsule,
		Injection:     resp.Injection,
		Communication: localCommunicationFromInjection(resp.Injection),
		Message:       resp.Message,
	}, nil
}

func (f *LocalCollaborationFacade) ShareSessionMemory(
	ctx context.Context,
	req *ShareSessionMemoryRequest,
	opts ...func(*Option),
) (*ShareSessionMemoryResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("share session memory: request is required")
	}
	if f.capsules == nil {
		return nil, fmt.Errorf("share session memory: capsule facade is required")
	}
	created, err := f.capsules.Create(ctx, &CreateCapsuleRequest{
		SourceSessionID: req.SourceSessionID,
		Agent:           req.SourceAgent,
		Keyword:         req.Keyword,
		Title:           req.Title,
		Summary:         req.Summary,
		Content:         req.Content,
		Local:           req.Local,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("create memory capsule: %w", err)
	}
	resp := &ShareSessionMemoryResponse{Capsule: created.Capsule}
	if !shareSessionMemoryRequestsDelivery(req) {
		resp.Communication = &LocalCommunication{
			Kind:            LocalCommunicationKindMemoryHandoff,
			SourceAgent:     created.Capsule.SourceAgent,
			SourceSessionID: created.Capsule.SourceSessionID,
		}
		return resp, nil
	}
	injected, err := f.queueMemoryDelivery(ctx, &QueueMemoryDeliveryRequest{
		CapsuleID:   created.Capsule.CapsuleID,
		Route:       shareSessionMemoryRoute(req),
		ActionItems: req.ActionItems,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("deliver memory capsule: %w", err)
	}
	resp.Injection = injected.Injection
	resp.Communication = localCommunicationFromInjectionKind(
		injected.Injection,
		LocalCommunicationKindMemoryHandoff,
	)
	resp.Message = injected.Message
	return resp, nil
}

func (f *LocalCollaborationFacade) QueueMemoryDelivery(
	ctx context.Context,
	req *QueueMemoryDeliveryRequest,
	opts ...func(*Option),
) (*QueueMemoryDeliveryResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("queue memory delivery: request is required")
	}
	if f.capsules == nil {
		return nil, fmt.Errorf("queue memory delivery: capsule facade is required")
	}
	return f.queueMemoryDelivery(ctx, req, opts...)
}

func (f *LocalCollaborationFacade) queueMemoryDelivery(
	ctx context.Context,
	req *QueueMemoryDeliveryRequest,
	opts ...func(*Option),
) (*QueueMemoryDeliveryResponse, error) {
	injectReq, err := injectCapsuleRequestFromMemoryRoute(req)
	if err != nil {
		return nil, err
	}
	injected, err := f.capsules.Inject(ctx, injectReq, opts...)
	if err != nil {
		return nil, fmt.Errorf("queue memory capsule: %w", err)
	}
	return &QueueMemoryDeliveryResponse{
		Injection: injected.Injection,
		Communication: localCommunicationFromInjectionKind(
			injected.Injection,
			LocalCommunicationKindMemoryHandoff,
		),
		Message: injected.Message,
	}, nil
}

func shareSessionMemoryRequestsDelivery(req *ShareSessionMemoryRequest) bool {
	if req.Route != nil {
		return true
	}
	return req.NewSession ||
		req.TargetAgent != model.AgentNameUnknown && req.TargetAgent != "" ||
		req.TargetSessionID != "" ||
		req.MatchType != ""
}

func shareSessionMemoryRoute(req *ShareSessionMemoryRequest) *LocalMemoryRoute {
	if req.Route != nil {
		return req.Route
	}
	switch {
	case req.NewSession:
		return &LocalMemoryRoute{
			Kind:  LocalMemoryRouteKindNewSession,
			Agent: req.TargetAgent,
		}
	case req.TargetSessionID != "":
		return &LocalMemoryRoute{
			Kind:      LocalMemoryRouteKindSession,
			Agent:     req.TargetAgent,
			SessionID: req.TargetSessionID,
		}
	case req.MatchType != "":
		return &LocalMemoryRoute{
			Kind:    LocalMemoryRouteKind(req.MatchType),
			Agent:   req.TargetAgent,
			Keyword: req.MatchValue,
			Project: req.MatchValue,
		}
	default:
		return nil
	}
}

func injectCapsuleRequestFromMemoryRoute(
	req *QueueMemoryDeliveryRequest,
) (*InjectCapsuleRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("queue memory delivery: request is required")
	}
	if req.Route == nil {
		return nil, fmt.Errorf("queue memory delivery: route is required")
	}
	route := req.Route
	injectReq := &InjectCapsuleRequest{
		CapsuleID:   req.CapsuleID,
		Agent:       route.Agent,
		ActionItems: req.ActionItems,
	}
	switch route.Kind {
	case LocalMemoryRouteKindSession:
		injectReq.TargetSessionID = route.SessionID
	case LocalMemoryRouteKindNewSession:
		injectReq.NewSession = true
	case LocalMemoryRouteKindAny:
		injectReq.MatchType = "any"
	case LocalMemoryRouteKindKeyword:
		injectReq.MatchType = "keyword"
		injectReq.MatchValue = route.Keyword
	case LocalMemoryRouteKindProject:
		injectReq.MatchType = "project"
		injectReq.MatchValue = route.Project
	case LocalMemoryRouteKindUnknown, "":
		return nil, fmt.Errorf("queue memory delivery: route kind is required")
	default:
		return nil, fmt.Errorf("queue memory delivery: unsupported route kind %q", route.Kind)
	}
	return injectReq, nil
}

func localCommunicationFromInjection(injection *model.KnowledgeInjection) *LocalCommunication {
	return localCommunicationFromInjectionKind(injection, LocalCommunicationKindSessionHandoff)
}

func localCommunicationFromInjectionKind(
	injection *model.KnowledgeInjection,
	kind LocalCommunicationKind,
) *LocalCommunication {
	if injection == nil {
		return &LocalCommunication{Kind: LocalCommunicationKindUnknown}
	}
	return &LocalCommunication{
		Kind:            kind,
		SourceAgent:     injection.SourceAgent,
		SourceSessionID: injection.SourceSessionID,
		TargetAgent:     injection.TargetAgent,
		TargetSessionID: injection.TargetSessionID,
		DeliveryMethod:  injection.DeliveryMethod,
	}
}
