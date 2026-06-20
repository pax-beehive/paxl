package facade

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/pax-oss/paxl/pkg/adaptor"
)

const (
	knowledgeTitleLimit       = 120
	knowledgeSummaryLimit     = 1200
	knowledgeContentLimit     = 6000
	knowledgeExtractLineLimit = 40
)

type CapsuleFacade struct {
	session *SessionFacade
	store   *store.Store
}

type CreateCapsuleRequest struct {
	SourceSessionID string
	Agent           model.AgentName
	Keyword         string
	Local           bool
}

type CreateCapsuleResponse struct {
	Capsule *model.KnowledgeCapsule
}

type ListCapsulesRequest struct {
	Status          string
	Keyword         string
	SourceSessionID string
	Limit           int
}

type ListCapsulesResponse struct {
	Capsules []*model.KnowledgeCapsule
}

type GetCapsuleRequest struct {
	CapsuleID string
}

type GetCapsuleResponse struct {
	Capsule *model.KnowledgeCapsule
}

type ArchiveCapsuleRequest struct {
	CapsuleID string
}

type ArchiveCapsuleResponse struct {
	Capsule *model.KnowledgeCapsule
}

type InjectCapsuleRequest struct {
	CapsuleID       string
	TargetSessionID string
	Agent           model.AgentName
}

type InjectCapsuleResponse struct {
	Injection *model.KnowledgeInjection
	Message   string
}

type ListInjectionsRequest struct {
	TargetSessionID string
	Limit           int
}

type ListInjectionsResponse struct {
	Injections []*model.KnowledgeInjection
}

type MirrorSessionRequest struct {
	SourceSessionID string
	Agent           model.AgentName
	TargetAgent     model.AgentName
	TargetSessionID string
}

type MirrorSessionResponse struct {
	Capsule   *model.KnowledgeCapsule
	Injection *model.KnowledgeInjection
	Message   string
}

func NewCapsuleFacade(registry *adaptor.Registry, sessionStore *store.Store) *CapsuleFacade {
	return &CapsuleFacade{
		session: NewSessionFacade(registry, sessionStore),
		store:   sessionStore,
	}
}

func (f *CapsuleFacade) Create(
	ctx context.Context,
	req *CreateCapsuleRequest,
	opts ...func(*Option),
) (*CreateCapsuleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("create capsule: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("create capsule: store is required")
	}
	option := applyOptions(opts)
	var capsule *model.KnowledgeCapsule
	var err error
	if !req.Local {
		capsule, err = f.buildSourceGeneratedCapsule(ctx, req, option)
		if err != nil {
			return nil, fmt.Errorf("build source-generated capsule: %w", err)
		}
	} else {
		capsule, err = f.buildLocalCapsule(ctx, req, option)
		if err != nil {
			return nil, fmt.Errorf("build local capsule: %w", err)
		}
	}
	created, err := f.store.CreateKnowledgeCapsule(
		ctx,
		&store.CreateKnowledgeCapsuleRequest{Capsule: capsule},
	)
	if err != nil {
		return nil, fmt.Errorf("store knowledge capsule: %w", err)
	}
	return &CreateCapsuleResponse{Capsule: created.Capsule}, nil
}

func (f *CapsuleFacade) List(
	ctx context.Context,
	req *ListCapsulesRequest,
	opts ...func(*Option),
) (*ListCapsulesResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("list capsules: request is required")
	}
	list, err := f.store.ListKnowledgeCapsules(ctx, &store.ListKnowledgeCapsulesRequest{
		Status:          req.Status,
		Keyword:         req.Keyword,
		SourceSessionID: req.SourceSessionID,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list knowledge capsules: %w", err)
	}
	return &ListCapsulesResponse{Capsules: list.Capsules}, nil
}

func (f *CapsuleFacade) Get(
	ctx context.Context,
	req *GetCapsuleRequest,
	opts ...func(*Option),
) (*GetCapsuleResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("get capsule: request is required")
	}
	got, err := f.store.GetKnowledgeCapsule(
		ctx,
		&store.GetKnowledgeCapsuleRequest{CapsuleID: req.CapsuleID},
	)
	if err != nil {
		return nil, fmt.Errorf("get knowledge capsule: %w", err)
	}
	return &GetCapsuleResponse{Capsule: got.Capsule}, nil
}

func (f *CapsuleFacade) Archive(
	ctx context.Context,
	req *ArchiveCapsuleRequest,
	opts ...func(*Option),
) (*ArchiveCapsuleResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("archive capsule: request is required")
	}
	archived, err := f.store.ArchiveKnowledgeCapsule(ctx, &store.ArchiveKnowledgeCapsuleRequest{
		CapsuleID: req.CapsuleID,
	})
	if err != nil {
		return nil, fmt.Errorf("archive knowledge capsule: %w", err)
	}
	return &ArchiveCapsuleResponse{Capsule: archived.Capsule}, nil
}

func (f *CapsuleFacade) Inject(
	ctx context.Context,
	req *InjectCapsuleRequest,
	opts ...func(*Option),
) (*InjectCapsuleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("inject capsule: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("inject capsule: store is required")
	}
	capsule, target, err := f.loadInjectionInputs(ctx, req)
	if err != nil {
		return nil, err
	}
	injectionID, err := newLocalID("kci")
	if err != nil {
		return nil, fmt.Errorf("create injection id: %w", err)
	}
	injection := &model.KnowledgeInjection{
		InjectionID:         injectionID,
		CapsuleID:           capsule.CapsuleID,
		TargetSessionID:     target.ID,
		TargetAgent:         target.Agent,
		DeliveryMethod:      "cli_resume",
		DeliveryMessageType: "system_handoff",
		Status:              "delivered",
	}
	message := renderKnowledgeHandoff(capsule, injection)
	if err := f.deliverInjection(ctx, target, message, applyOptions(opts)); err != nil {
		return nil, fmt.Errorf("deliver knowledge capsule: %w", err)
	}
	created, err := f.store.CreateKnowledgeInjection(
		ctx,
		&store.CreateKnowledgeInjectionRequest{Injection: injection},
	)
	if err != nil {
		return nil, fmt.Errorf("store knowledge injection: %w", err)
	}
	return &InjectCapsuleResponse{Injection: created.Injection, Message: message}, nil
}

func (f *CapsuleFacade) ListInjections(
	ctx context.Context,
	req *ListInjectionsRequest,
	opts ...func(*Option),
) (*ListInjectionsResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("list injections: request is required")
	}
	list, err := f.store.ListKnowledgeInjections(ctx, &store.ListKnowledgeInjectionsRequest{
		TargetSessionID: req.TargetSessionID,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list knowledge injections: %w", err)
	}
	return &ListInjectionsResponse{Injections: list.Injections}, nil
}

func (f *CapsuleFacade) buildSourceGeneratedCapsule(
	ctx context.Context,
	req *CreateCapsuleRequest,
	option *Option,
) (*model.KnowledgeCapsule, error) {
	if strings.TrimSpace(req.Keyword) == "" {
		return nil, fmt.Errorf("keyword is required")
	}
	session, err := f.session.Get(ctx, &GetSessionRequest{
		ID:    req.SourceSessionID,
		Agent: req.Agent,
	}, func(next *Option) {
		next.VerboseWriter = option.VerboseWriter
	})
	if err != nil {
		return nil, fmt.Errorf("load source session: %w", err)
	}
	capsuleID, err := newLocalID("kcap")
	if err != nil {
		return nil, fmt.Errorf("create capsule id: %w", err)
	}
	if err := f.requestSourceCapsule(
		ctx,
		session.Session,
		capsuleID,
		req.Keyword,
		option,
	); err != nil {
		return nil, err
	}
	elements, err := f.refreshSourceElements(ctx, session.Session, option)
	if err != nil {
		return nil, err
	}
	capsule, ok, err := parseGeneratedKnowledgeCapsule(
		capsuleID,
		session.Session,
		req.Keyword,
		elements,
	)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("generated capsule markers were not found for %s", capsuleID)
	}
	return capsule, nil
}

func (f *CapsuleFacade) requestSourceCapsule(
	ctx context.Context,
	session *model.Session,
	capsuleID string,
	keyword string,
	option *Option,
) error {
	adapter, err := f.session.registry.Lookup(ctx, &adaptor.LookupRequest{Name: session.Agent})
	if err != nil {
		return fmt.Errorf("lookup %s adapter: %w", session.Agent, err)
	}
	if _, err := adapter.Adapter.Prompt(
		ctx,
		&adaptor.PromptRequest{
			NativeID: session.NativeID,
			Text:     renderCapsuleGenerationPrompt(capsuleID, keyword),
		},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	); err != nil {
		return fmt.Errorf("prompt source session: %w", err)
	}
	return nil
}

func (f *CapsuleFacade) refreshSourceElements(
	ctx context.Context,
	session *model.Session,
	option *Option,
) ([]*model.Element, error) {
	adapter, err := f.session.registry.Lookup(ctx, &adaptor.LookupRequest{Name: session.Agent})
	if err != nil {
		return nil, fmt.Errorf("lookup %s adapter: %w", session.Agent, err)
	}
	transcript, err := adapter.Adapter.GetSession(
		ctx,
		&adaptor.GetSessionRequest{NativeID: session.NativeID},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	)
	if err != nil {
		return nil, fmt.Errorf("refresh source transcript: %w", err)
	}
	if _, err := f.store.ReplaceSessionElements(ctx, &store.ReplaceSessionElementsRequest{
		SessionID: session.ID,
		Elements:  transcript.Elements,
	}); err != nil {
		return nil, fmt.Errorf("store refreshed source transcript: %w", err)
	}
	return transcript.Elements, nil
}

func (f *CapsuleFacade) MirrorSession(
	ctx context.Context,
	req *MirrorSessionRequest,
	opts ...func(*Option),
) (*MirrorSessionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("mirror session: request is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("mirror session: store is required")
	}
	option := applyOptions(opts)
	capsule, err := f.buildMirrorCapsule(ctx, req, option)
	if err != nil {
		return nil, fmt.Errorf("build mirror capsule: %w", err)
	}
	createdCapsule, err := f.store.CreateKnowledgeCapsule(
		ctx,
		&store.CreateKnowledgeCapsuleRequest{Capsule: capsule},
	)
	if err != nil {
		return nil, fmt.Errorf("store mirror capsule: %w", err)
	}
	injection, message, err := f.deliverMirror(ctx, req, createdCapsule.Capsule, option)
	if err != nil {
		return nil, fmt.Errorf("deliver mirror: %w", err)
	}
	createdInjection, err := f.store.CreateKnowledgeInjection(
		ctx,
		&store.CreateKnowledgeInjectionRequest{Injection: injection},
	)
	if err != nil {
		return nil, fmt.Errorf("store mirror injection: %w", err)
	}
	return &MirrorSessionResponse{
		Capsule:   createdCapsule.Capsule,
		Injection: createdInjection.Injection,
		Message:   message,
	}, nil
}

func (f *CapsuleFacade) buildLocalCapsule(
	ctx context.Context,
	req *CreateCapsuleRequest,
	option *Option,
) (*model.KnowledgeCapsule, error) {
	if strings.TrimSpace(req.Keyword) == "" {
		return nil, fmt.Errorf("keyword is required")
	}
	session, err := f.session.Get(ctx, &GetSessionRequest{
		ID:    req.SourceSessionID,
		Agent: req.Agent,
	}, func(next *Option) {
		next.VerboseWriter = option.VerboseWriter
	})
	if err != nil {
		return nil, fmt.Errorf("load source session: %w", err)
	}
	capsuleID, err := newLocalID("kcap")
	if err != nil {
		return nil, fmt.Errorf("create capsule id: %w", err)
	}
	content, originalChars, truncated := extractKnowledgeContent(req.Keyword, session.Elements)
	if strings.TrimSpace(content) == "" {
		content = "No matching session history was found for this keyword."
	}
	return &model.KnowledgeCapsule{
		CapsuleID:       capsuleID,
		SourceSessionID: session.Session.ID,
		SourceAgent:     session.Session.Agent,
		Keyword:         req.Keyword,
		Title: truncateString(
			"Knowledge capsule: "+req.Keyword,
			knowledgeTitleLimit,
		),
		Summary: truncateString(
			localCapsuleSummary(req.Keyword, session.Session.ID),
			knowledgeSummaryLimit,
		),
		Content:                content,
		Status:                 "active",
		Truncated:              truncated,
		OriginalEstimatedChars: int64(originalChars),
		CreatedAt:              time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (f *CapsuleFacade) buildMirrorCapsule(
	ctx context.Context,
	req *MirrorSessionRequest,
	option *Option,
) (*model.KnowledgeCapsule, error) {
	session, err := f.session.Get(ctx, &GetSessionRequest{
		ID:    req.SourceSessionID,
		Agent: req.Agent,
	}, func(next *Option) {
		next.VerboseWriter = option.VerboseWriter
	})
	if err != nil {
		return nil, fmt.Errorf("load source session: %w", err)
	}
	capsuleID, err := newLocalID("kcap")
	if err != nil {
		return nil, fmt.Errorf("create capsule id: %w", err)
	}
	content, originalChars, truncated := extractSessionContextContent(session.Elements)
	if strings.TrimSpace(content) == "" {
		content = "No session history was found for this mirror."
	}
	return &model.KnowledgeCapsule{
		CapsuleID:       capsuleID,
		SourceSessionID: session.Session.ID,
		SourceAgent:     session.Session.Agent,
		Keyword:         "session mirror",
		Title:           truncateString("Session mirror: "+session.Session.ID, knowledgeTitleLimit),
		Summary: truncateString(
			sessionMirrorSummary(session.Session.ID),
			knowledgeSummaryLimit,
		),
		Content:                content,
		Status:                 "active",
		Truncated:              truncated,
		OriginalEstimatedChars: int64(originalChars),
		CreatedAt:              time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (f *CapsuleFacade) loadInjectionInputs(
	ctx context.Context,
	req *InjectCapsuleRequest,
) (*model.KnowledgeCapsule, *model.Session, error) {
	capsule, err := f.store.GetKnowledgeCapsule(
		ctx,
		&store.GetKnowledgeCapsuleRequest{CapsuleID: req.CapsuleID},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("get capsule %q: %w", req.CapsuleID, err)
	}
	if capsule.Capsule.Status != "active" {
		return nil, nil, fmt.Errorf("capsule %q is not active", req.CapsuleID)
	}
	target, err := f.store.FindSession(ctx, &store.FindSessionRequest{
		ID:    req.TargetSessionID,
		Agent: req.Agent,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("find target session %q: %w", req.TargetSessionID, err)
	}
	return capsule.Capsule, target.Session, nil
}

func (f *CapsuleFacade) deliverMirror(
	ctx context.Context,
	req *MirrorSessionRequest,
	capsule *model.KnowledgeCapsule,
	option *Option,
) (*model.KnowledgeInjection, string, error) {
	mirrorID, err := newLocalID("mir")
	if err != nil {
		return nil, "", fmt.Errorf("create mirror id: %w", err)
	}
	if req.TargetSessionID != "" {
		return f.deliverMirrorToSession(ctx, req, capsule, mirrorID, option)
	}
	return f.deliverMirrorToNewSession(ctx, req, capsule, mirrorID, option)
}

func (f *CapsuleFacade) deliverMirrorToSession(
	ctx context.Context,
	req *MirrorSessionRequest,
	capsule *model.KnowledgeCapsule,
	mirrorID string,
	option *Option,
) (*model.KnowledgeInjection, string, error) {
	target, err := f.store.FindSession(ctx, &store.FindSessionRequest{
		ID:    req.TargetSessionID,
		Agent: req.TargetAgent,
	})
	if err != nil {
		return nil, "", fmt.Errorf("find target session %q: %w", req.TargetSessionID, err)
	}
	message := renderMirrorHandoff(capsule, mirrorID, target.Session.Agent, target.Session.ID)
	if err := f.deliverInjection(ctx, target.Session, message, option); err != nil {
		return nil, "", err
	}
	return &model.KnowledgeInjection{
		InjectionID:         mirrorID,
		CapsuleID:           capsule.CapsuleID,
		TargetSessionID:     target.Session.ID,
		TargetAgent:         target.Session.Agent,
		DeliveryMethod:      "cli_resume",
		DeliveryMessageType: "system_handoff",
		Status:              "delivered",
	}, message, nil
}

func (f *CapsuleFacade) deliverMirrorToNewSession(
	ctx context.Context,
	req *MirrorSessionRequest,
	capsule *model.KnowledgeCapsule,
	mirrorID string,
	option *Option,
) (*model.KnowledgeInjection, string, error) {
	if req.TargetAgent == model.AgentNameUnknown || req.TargetAgent == "" {
		return nil, "", fmt.Errorf("target agent is required")
	}
	message := renderMirrorHandoff(capsule, mirrorID, req.TargetAgent, "")
	adapter, err := f.session.registry.Lookup(ctx, &adaptor.LookupRequest{Name: req.TargetAgent})
	if err != nil {
		return nil, "", fmt.Errorf("lookup %s adapter: %w", req.TargetAgent, err)
	}
	if _, err := adapter.Adapter.StartSession(
		ctx,
		&adaptor.StartSessionRequest{Text: message},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	); err != nil {
		return nil, "", fmt.Errorf("start target session: %w", err)
	}
	return &model.KnowledgeInjection{
		InjectionID:         mirrorID,
		CapsuleID:           capsule.CapsuleID,
		TargetSessionID:     "(new " + string(req.TargetAgent) + " session)",
		TargetAgent:         req.TargetAgent,
		DeliveryMethod:      "cli_new_session",
		DeliveryMessageType: "system_handoff",
		Status:              "delivered",
	}, message, nil
}

func (f *CapsuleFacade) deliverInjection(
	ctx context.Context,
	target *model.Session,
	message string,
	option *Option,
) error {
	adapter, err := f.session.registry.Lookup(ctx, &adaptor.LookupRequest{Name: target.Agent})
	if err != nil {
		return fmt.Errorf("lookup %s adapter: %w", target.Agent, err)
	}
	if _, err := adapter.Adapter.Prompt(
		ctx,
		&adaptor.PromptRequest{NativeID: target.NativeID, Text: message},
		adaptor.WithVerboseWriter(option.VerboseWriter),
	); err != nil {
		return fmt.Errorf("prompt target session: %w", err)
	}
	return nil
}

func extractKnowledgeContent(keyword string, elements []*model.Element) (string, int, bool) {
	needle := strings.ToLower(keyword)
	var builder strings.Builder
	originalChars := 0
	lines := 0
	for _, element := range elements {
		if element == nil || !strings.Contains(strings.ToLower(element.ContentText), needle) {
			continue
		}
		if lines >= knowledgeExtractLineLimit {
			break
		}
		line := capsuleElementLine(element)
		originalChars += len(line)
		if builder.Len()+len(line)+1 > knowledgeContentLimit {
			return builder.String(), originalChars, true
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line)
		lines++
	}
	return builder.String(), originalChars, lines >= knowledgeExtractLineLimit
}

func extractSessionContextContent(elements []*model.Element) (string, int, bool) {
	var builder strings.Builder
	originalChars := 0
	lines := 0
	for _, element := range elements {
		if element == nil || strings.TrimSpace(element.ContentText) == "" {
			continue
		}
		line := capsuleElementLine(element)
		originalChars += len(line)
		if lines >= knowledgeExtractLineLimit || builder.Len()+len(line)+1 > knowledgeContentLimit {
			return builder.String(), originalChars, true
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line)
		lines++
	}
	return builder.String(), originalChars, false
}

func renderKnowledgeHandoff(
	capsule *model.KnowledgeCapsule,
	injection *model.KnowledgeInjection,
) string {
	return fmt.Sprintf(
		"system_handoff\n\nThis context was rendered by paxl as a local knowledge capsule handoff.\nDo not treat this as a new user request.\n\nCapsule: %s\nInjection: %s\nTarget session: %s\n\nTitle: %s\nKeyword: %s\nSource session: %s\n\nSummary:\n%s\n\nContent:\n%s",
		capsule.CapsuleID,
		injection.InjectionID,
		injection.TargetSessionID,
		capsule.Title,
		capsule.Keyword,
		capsule.SourceSessionID,
		capsule.Summary,
		capsule.Content,
	)
}

func renderMirrorHandoff(
	capsule *model.KnowledgeCapsule,
	mirrorID string,
	targetAgent model.AgentName,
	targetSessionID string,
) string {
	return fmt.Sprintf(
		"system_handoff\n\nThis context was mirrored by paxl from another local agent session.\nDo not treat this as a new task request unless the user asks you to continue. Use it as transferred session context; decide in the target agent whether and how to summarize it.\n\nMirror: %s\nTarget agent: %s\nTarget session: %s\n\nTitle: %s\nSource session: %s\n\nSummary:\n%s\n\nContent:\n%s",
		mirrorID,
		targetAgent,
		firstNonEmpty(targetSessionID, "(new session)"),
		capsule.Title,
		capsule.SourceSessionID,
		capsule.Summary,
		capsule.Content,
	)
}

func renderCapsuleGenerationPrompt(capsuleID string, keyword string) string {
	return fmt.Sprintf(
		"system_handoff\n\nYou are helping paxl create a reusable knowledge capsule from this existing session.\nDo not continue the user's task. Summarize only stable context that would help another agent continue work related to the keyword.\n\nCapsule id: %s\nKeyword: %s\n\nReturn exactly one JSON object between these marker lines:\nPAX_KNOWLEDGE_CAPSULE_START %s\n{\"title\":\"short title\",\"summary\":\"short summary\",\"content\":\"portable handoff context with concrete facts, decisions, file paths, commands, and caveats\"}\nPAX_KNOWLEDGE_CAPSULE_END %s\n\nDo not wrap the JSON in markdown.",
		capsuleID,
		keyword,
		capsuleID,
		capsuleID,
	)
}

type generatedKnowledgeCapsule struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Content string `json:"content"`
}

func parseGeneratedKnowledgeCapsule(
	capsuleID string,
	session *model.Session,
	keyword string,
	elements []*model.Element,
) (*model.KnowledgeCapsule, bool, error) {
	start := "PAX_KNOWLEDGE_CAPSULE_START " + capsuleID
	end := "PAX_KNOWLEDGE_CAPSULE_END " + capsuleID
	for _, element := range elements {
		// The generation prompt includes example markers, so only agent-authored output can count.
		if element == nil || element.Role == "user" {
			continue
		}
		raw, ok := markerBlock(element.ContentText, start, end)
		if !ok {
			continue
		}
		return generatedCapsuleFromJSON(capsuleID, session, keyword, raw)
	}
	return nil, false, nil
}

func markerBlock(text string, start string, end string) (string, bool) {
	startIndex := strings.Index(text, start)
	if startIndex < 0 {
		return "", false
	}
	bodyStart := startIndex + len(start)
	endIndex := strings.Index(text[bodyStart:], end)
	if endIndex < 0 {
		return "", false
	}
	return strings.TrimSpace(text[bodyStart : bodyStart+endIndex]), true
}

func generatedCapsuleFromJSON(
	capsuleID string,
	session *model.Session,
	keyword string,
	raw string,
) (*model.KnowledgeCapsule, bool, error) {
	var generated generatedKnowledgeCapsule
	if err := json.Unmarshal([]byte(raw), &generated); err != nil {
		return nil, false, fmt.Errorf("decode generated capsule %s: %w", capsuleID, err)
	}
	content := strings.TrimSpace(generated.Content)
	if content == "" {
		return nil, false, fmt.Errorf("generated capsule %s has empty content", capsuleID)
	}
	truncatedContent := truncateString(content, knowledgeContentLimit)
	return &model.KnowledgeCapsule{
		CapsuleID:       capsuleID,
		SourceSessionID: session.ID,
		SourceAgent:     session.Agent,
		Keyword:         keyword,
		Title: truncateString(
			firstNonEmpty(strings.TrimSpace(generated.Title), "Knowledge capsule: "+keyword),
			knowledgeTitleLimit,
		),
		Summary:   truncateString(strings.TrimSpace(generated.Summary), knowledgeSummaryLimit),
		Content:   truncatedContent,
		Status:    "active",
		Truncated: len([]rune(truncatedContent)) < len([]rune(content)),
		OriginalEstimatedChars: int64(
			len(generated.Title) + len(generated.Summary) + len(generated.Content),
		),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, true, nil
}

func capsuleElementLine(element *model.Element) string {
	return fmt.Sprintf(
		"- [%s %s] %s",
		firstNonEmpty(element.Role, element.Type, "event"),
		firstNonEmpty(element.CompletedAt, element.StartedAt),
		strings.TrimSpace(element.ContentText),
	)
}

func localCapsuleSummary(keyword string, sessionID string) string {
	return fmt.Sprintf(
		"Extracted knowledge related to %q from local session %s. Review source context before relying on this handoff.",
		keyword,
		sessionID,
	)
}

func sessionMirrorSummary(sessionID string) string {
	return fmt.Sprintf(
		"Transferred session context from local session %s without asking the source agent to summarize it.",
		sessionID,
	)
}

func newLocalID(prefix string) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func truncateString(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
